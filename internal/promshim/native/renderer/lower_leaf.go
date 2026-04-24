package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

// renderLeafLogical renders a top-level LeafExprPlan directly to a
// renderedFragment without constructing an intermediate NativeFragment.
// It replicates the non-folded LeafSource branch of renderSourceFragment,
// reusing buildSelectorSource and the storage.Build*QuerySQL helpers to
// lock byte-identity with the Fragment path.
//
// Pure (non-folded) leaves always have ValueExpr="{value}" and
// TagsExpr="{tags}", which means sourceWrapperIsIdentity returns true
// and the wrap step is skipped. This function produces the same SQL as
// the "identity wrapper" branch of renderSourceFragment.
//
// When cachedSelector is non-nil it is preferred over a freshly built
// one. Callers thread the cached leaf selector from NativeAnalysis so
// tag-narrowing mutations (applied in-place by upstream passes such as
// narrowHistogramChildAnalysisInPlace or applySelectorProjection) flow
// through to the rendered SQL. This mirrors the Fragment-side behavior
// where RenderFragment on the cached leaf Fragment picks up the same
// narrowed SelectorSource.
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

	switch params.Mode {
	case native.RenderModeInstant:
		if selector != nil {
			storageSel := nativeSelectorToStorage(selector)
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		}
		// Delegated PromQL leaf — mirrors renderSourceFragment lines 33-45.
		// renderSourceFragment always calls wrapInstantSourceQuery for the
		// delegated path (no identity check), so we match that here.
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
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		}
		// Delegated PromQL leaf — mirrors renderSourceFragment lines 61-73.
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
// storage SQL builders). This mirrors what renderAggregationSource does
// in source.go, but operates directly on the logical leaf's selector.
func nativeSelectorToStorage(sel *native.SelectorSource) storage.SelectorSource {
	return storage.SelectorSource{
		Kind:              storage.SelectorKind(sel.Kind),
		MetricName:        sel.MetricName,
		Matchers:          selectorEffectiveMatchers(sel),
		NeedTags:          selectorNeedsTags(sel),
		RequireFullTags:   sel.RequireFullTags,
		RequiredTagLabels: append([]string(nil), sel.RequiredTagLabels...),
		LookbackMS:        sel.Lookback.Milliseconds(),
		OffsetMS:          sel.Offset.Milliseconds(),
	}
}

// renderAggregationSourceView is the pure-logical analog of
// renderAggregationSource. Instead of dereferencing a NativeFragment's
// Selector / ValueExpr / TagsExpr / SourcePromQL fields, it reads them
// from a SourceExprView on the analysis-side LoweringInfo.
//
// The returned storage.AggregationSource is byte-identical to what
// renderAggregationSource produces for the equivalent Fragment (the
// view's Selector is the same *SelectorSource pointer stored in
// info.Fragment.Selector, and the remaining fields are value-typed
// mirrors captured at Analyze time).
func renderAggregationSourceView(view *native.SourceExprView, params RenderParams) (storage.AggregationSource, error) {
	if view == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation source view is nil")
	}
	if view.Selector != nil {
		storageSel := nativeSelectorToStorage(view.Selector)
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
// analysis-side SourceExprView without constructing a NativeFragment.
// Mirrors renderSourceFragment in source.go byte-for-byte: the shape of
// the generated SQL depends on whether the view carries a Selector (the
// selector-backed path) or only a SourcePromQL leaf (the delegated-
// resolver path), and whether the wrapper is the identity ({value},
// {tags}, !DropsMetric).
func renderSourceExprView(cfg storage.QueryConfig, view *native.SourceExprView, params RenderParams) (renderedFragment, error) {
	if view == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderSourceExprView called with nil view")
	}
	isIdentity := view.ValueExpr == "{value}" && view.TagsExpr == "{tags}" && !view.DropsMetric
	switch params.Mode {
	case native.RenderModeInstant:
		if view.Selector != nil {
			storageSel := nativeSelectorToStorage(view.Selector)
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
