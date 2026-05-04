package renderer

import (
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/model/labels"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

// renderLeafLogical renders a top-level LeafExprPlan directly to a
// renderedFragment via BuildSelectorSource and the storage.Build*QuerySQL
// helpers.
//
// Pure (non-folded) leaves always have ValueExpr="{value}" and
// TagsExpr="{tags}", so no output wrapper is applied.
//
// When cachedSelector is non-nil it is preferred over a freshly built
// one. Callers thread the cached leaf selector from NativeAnalysis to
// avoid re-running BuildSelectorSource. Tag-narrowing flows through
// RenderParams and is layered on via applyRenderParamsNarrowing.
func renderLeafLogical(cfg storage.QueryConfig, leaf *logicalpkg.LeafExprPlan, params RenderParams, cachedSelector *native.SelectorSource) (renderedFragment, error) {
	if leaf == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderLeafLogical called with nil leaf")
	}

	selector := cachedSelector
	if selector == nil {
		built, err := native.BuildSelectorSource(leaf.Expr)
		if err != nil {
			return renderedFragment{}, fmt.Errorf("renderer: leaf selector analysis failed: %w", err)
		}
		selector = built
	}

	// If this metric is materialized, route the leaf query to the
	// materialized table (MergeTree) instead of the TimeSeries table.
	if params.ResolveTableOverride != nil && selector != nil {
		if override := params.ResolveTableOverride(selector.MetricName); override != "" {
			return renderMaterializedLeaf(cfg, override, selector, params)
		}
	}

	switch params.Mode {
	case native.RenderModeInstant:
		if selector != nil {
			storageSel := nativeSelectorToStorage(selector)
			applyRenderParamsNarrowing(&storageSel, params)
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		}
		// Delegated PromQL leaf: the wrapper is always applied, even
		// when ValueExpr/TagsExpr are the identity pair, because
		// wrapInstantSourceQuery is the only way to pull the leaf's
		// rows through the standard {tags, timestamp, value} column
		// shape the rest of the renderer expects.
		if params.ResolveSourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(leaf.Expr)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		wrappedSQL, err := wrapInstantSourceQuery(sql, "{value}", "{tags}")
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil

	case native.RenderModeRange:
		if selector != nil {
			storageSel := nativeSelectorToStorage(selector)
			applyRenderParamsNarrowing(&storageSel, params)
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		}
		// Delegated PromQL leaf: same wrapper-always-applied rule as
		// the instant branch above.
		if params.ResolveSourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(leaf.Expr)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		wrappedSQL, err := wrapRangeSourceQuery(sql, "{value}", "{tags}")
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil

	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

// nativeSelectorToStorage converts a native.SelectorSource (from the
// native analysis package) into a storage.SelectorSource (used by the
// storage SQL builders). Tag-narrowing is not copied here — narrowing
// now flows exclusively through RenderParams and is layered on top by
// applyRenderParamsNarrowing. When no RenderParams narrowing is present
// the storage selector's zero-valued RequireFullTags / RequiredTagLabels
// fall through selectorTagsExpr to the full-tags base path, matching the
// Prometheus default.
func nativeSelectorToStorage(sel *native.SelectorSource) storage.SelectorSource {
	return storage.SelectorSource{
		Kind:       storage.SelectorKind(sel.Kind),
		MetricName: sel.MetricName,
		Matchers:   selectorEffectiveMatchers(sel),
		NeedTags:   true,
		LookbackMS: sel.Lookback.Milliseconds(),
		OffsetMS:   sel.Offset.Milliseconds(),
	}
}

// applyRenderParamsNarrowing mutates a storage.SelectorSource to honor
// the tag-narrowing carried on RenderParams. Parents that grouping-
// narrow the child (histogram projection/function) set RequireFullTags=
// false + RequiredTagLabels=grouping on params, or (selection /
// count_values aggregations) force RequireFullTags=true. RenderParams
// is the single source of truth for narrowing on the Logical path.
func applyRenderParamsNarrowing(sel *storage.SelectorSource, params RenderParams) {
	sel.RangeInstantStrategy = params.Physical.RangeInstantSelector.Strategy
	if params.RequireFullTags {
		sel.RequireFullTags = true
		sel.RequiredTagLabels = nil
		return
	}
	if len(params.RequiredTagLabels) > 0 {
		sel.RequireFullTags = false
		sel.RequiredTagLabels = append([]string(nil), params.RequiredTagLabels...)
	}
}

// renderAggregationSourceView reads Selector / ValueExpr / TagsExpr /
// SourcePromQL from a SourceExprView on the analysis-side LoweringInfo
// and returns a storage.AggregationSource suitable for the storage SQL
// builders.
func renderAggregationSourceView(view *native.SourceExprView, params RenderParams) (storage.AggregationSource, error) {
	if view == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation source view is nil")
	}
	if view.Selector != nil {
		storageSel := nativeSelectorToStorage(view.Selector)
		applyRenderParamsNarrowing(&storageSel, params)
		return storage.AggregationSource{
			Selector:  &storageSel,
			ValueExpr: view.ValueExpr,
			TagsExpr:  view.TagsExpr,
		}, nil
	}
	if view.SourcePromQL == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation source view is missing its PromQL leaf")
	}
	if params.ResolveSourcePromQL == nil {
		return storage.AggregationSource{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
	}
	promQL, err := params.ResolveSourcePromQL(view.SourcePromQL)
	if err != nil {
		return storage.AggregationSource{}, err
	}
	return storage.AggregationSource{PromQLLeaf: promQL, ValueExpr: view.ValueExpr, TagsExpr: view.TagsExpr}, nil
}

// renderSourceExprView renders a source-expression view (LeafSource,
// UnarySourceExpr, or BinaryScalarSourceExpr) directly from the
// analysis-side SourceExprView. The shape of the generated SQL depends
// on whether the view carries a Selector (the selector-backed path) or
// only a SourcePromQL leaf (the delegated-resolver path), and whether
// the wrapper is the identity ({value}, {tags}, !DropsMetric).
func renderSourceExprView(cfg storage.QueryConfig, view *native.SourceExprView, params RenderParams) (renderedFragment, error) {
	if view == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderSourceExprView called with nil view")
	}
	isIdentity := view.ValueExpr == "{value}" && view.TagsExpr == "{tags}" && !view.DropsMetric
	switch params.Mode {
	case native.RenderModeInstant:
		if view.Selector != nil {
			storageSel := nativeSelectorToStorage(view.Selector)
			applyRenderParamsNarrowing(&storageSel, params)
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return renderedFragment{}, err
			}
			if isIdentity {
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
			}
			wrappedSQL, err := wrapInstantSourceQuery(sql, view.ValueExpr, view.TagsExpr)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || view.SourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(view.SourcePromQL)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		wrappedSQL, err := wrapInstantSourceQuery(sql, view.ValueExpr, view.TagsExpr)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		if view.Selector != nil {
			storageSel := nativeSelectorToStorage(view.Selector)
			applyRenderParamsNarrowing(&storageSel, params)
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return renderedFragment{}, err
			}
			if isIdentity {
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
			}
			wrappedSQL, err := wrapRangeSourceQuery(sql, view.ValueExpr, view.TagsExpr)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || view.SourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(view.SourcePromQL)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		wrappedSQL, err := wrapRangeSourceQuery(sql, view.ValueExpr, view.TagsExpr)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

// renderMaterializedLeaf renders a simple SELECT against the materialized
// recording-rule table (a plain MergeTree with (tags, timestamp, value)
// columns). This avoids the TimeSeries-specific id/tags_to_columns machinery
// that doesn't apply to the materialized table.
func renderMaterializedLeaf(cfg storage.QueryConfig, table string, selector *native.SelectorSource, params RenderParams) (renderedFragment, error) {
	type renderSQL int
	const (
		instant renderSQL = iota
		range_
	)

	var kind renderSQL
	switch params.Mode {
	case native.RenderModeInstant:
		kind = instant
	case native.RenderModeRange:
		kind = range_
	default:
		return renderedFragment{}, fmt.Errorf("renderMaterializedLeaf: unsupported mode %q", params.Mode)
	}

	qName := "`" + cfg.Database + "`.`" + table + "`"

	// Build matcher conditions from the selector's label matchers.
	// The materialized table stores tags as Array(Tuple(String, String))
	// in the tags column; __name__ is the first element.
	matcherSQL, err := buildMaterializedLeafMatchers(selector)
	if err != nil {
		return renderedFragment{}, err
	}

	var sql string
	switch kind {
	case instant:
		sql = fmt.Sprintf(
			"SELECT %s AS tags, fromUnixTimestamp64Milli({eval_ms:Int64}) AS timestamp, any(value) AS value "+
				"FROM %s "+
				"WHERE %s AND "+
				"timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND "+
				"timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})",
			trimTagList(selector),
			qName,
			matcherSQL,
		)
	case range_:
		sql = fmt.Sprintf(
			"SELECT %s AS tags, timestamp, value "+
				"FROM %s "+
				"WHERE %s AND "+
				"timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND "+
				"timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})",
			trimTagList(selector),
			qName,
			matcherSQL,
		)
	}

	queryParams := map[string]string{
		"eval_ms":           fmt.Sprintf("%d", params.EvaluationTimeMS),
		"required_start_ms": fmt.Sprintf("%d", params.RequiredStartMS),
		"required_end_ms":   fmt.Sprintf("%d", params.RequiredEndMS),
	}

	return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
}

// buildMaterializedLeafMatchers builds WHERE conditions for the materialized
// table from the selector's label matchers. Metrics are stored with __name__
// as tags[].1; other matchers check has(tags, tuple('key', 'value')).
func buildMaterializedLeafMatchers(selector *native.SelectorSource) (string, error) {
	parts := []string{fmt.Sprintf("arrayExists(tag -> tag.1 = '__name__' AND tag.2 = %s, tags)", escapeLiteral(selector.MetricName))}
	for _, m := range selector.Matchers {
		if m.Name == "__name__" {
			continue
		}
		switch m.Type {
		case labels.MatchEqual:
			parts = append(parts, fmt.Sprintf("has(tags, tuple(%s, %s))",
				escapeLiteral(m.Name), escapeLiteral(m.Value)))
		case labels.MatchNotEqual:
			parts = append(parts, fmt.Sprintf("NOT has(tags, tuple(%s, %s))",
				escapeLiteral(m.Name), escapeLiteral(m.Value)))
		case labels.MatchRegexp:
			parts = append(parts, fmt.Sprintf("arrayExists(tag -> tag.1 = %s AND match(tag.2, %s), tags)",
				escapeLiteral(m.Name), escapeLiteral(m.Value)))
		case labels.MatchNotRegexp:
			parts = append(parts, fmt.Sprintf("NOT arrayExists(tag -> tag.1 = %s AND match(tag.2, %s), tags)",
				escapeLiteral(m.Name), escapeLiteral(m.Value)))
		}
	}
	return strings.Join(parts, " AND "), nil
}

// trimTagList generates an arrayFilter expression that strips __name__ from
// tags, matching the leaf selector output format. For the materialized table
// we always return full tags minus __name__ since tag narrowing isn't supported.
func trimTagList(selector *native.SelectorSource) string {
	return "arrayFilter(tag -> tag.1 != '__name__', tags)"
}

// escapeLiteral returns a single-quoted, properly escaped ClickHouse string
// literal for use in SQL queries.
func escapeLiteral(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return "'" + s + "'"
}
