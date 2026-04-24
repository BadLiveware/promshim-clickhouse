package renderer

import (
	"ch-observability/internal/promshim/native"
	"fmt"
	"sort"
	"strings"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
	"ch-observability/internal/promshim/storage/schema"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func outputMetricTagsSQL(metric map[string]string) string {
	if len(metric) == 0 {
		return "CAST([], '" + schema.TagsArrayType + "')"
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, "tuple("+sqlStringLiteral(key)+", "+sqlStringLiteral(metric[key])+")")
	}
	return "CAST([" + strings.Join(items, ", ") + "], '" + schema.TagsArrayType + "')"
}

func sqlStringLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}

func buildNativeWrapperSQL(query *sqlb.Select) (string, error) {
	sql, params, err := query.Build()
	if err != nil {
		return "", err
	}
	if len(params) != 0 {
		return "", fmt.Errorf("native wrapper SQL unexpectedly produced params: %#v", params)
	}
	return sql + schema.QuerySuffix, nil
}

func renderSQLExprNoParams(expr sqlb.Expr) string {
	sql, params, err := sqlb.BuildExpr(expr)
	if err != nil {
		panic(err)
	}
	if len(params) != 0 {
		panic(fmt.Errorf("sqlb expression unexpectedly produced params: %#v", params))
	}
	return sql
}

func rawRenderedSubquerySource(sql string) sqlb.RawSource {
	return rawRenderedSubquerySourceWithAlias(sql, "")
}

func rawRenderedSubquerySourceWithAlias(sql, alias string) sqlb.RawSource {
	return sqlb.RawSource{SQL: "(\n" + localIndentSQL(trimRenderedQuerySQL(sql), 4) + "\n)", Alias: alias}
}

func localIndentSQL(sql string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}

func wrapInstantSourceQuery(sourceSQL, valueExpr, tagsExpr string) (string, error) {
	sourceTagsExpr, err := storage.CompileSourceTagsTemplate(tagsExpr, sqlb.Ident("tags"))
	if err != nil {
		return "", err
	}
	sourceValueExpr, err := storage.CompileSourceValueTemplate(valueExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
	if err != nil {
		return "", err
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sourceTagsExpr, Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sourceValueExpr, Alias: "value"}},
		From:    rawRenderedSubquerySource(sourceSQL),
	}
	return buildNativeWrapperSQL(query)
}

func wrapRangeSourceQuery(sourceSQL, valueExpr, tagsExpr string) (string, error) {
	sourceTagsExpr, err := storage.CompileSourceTagsTemplate(tagsExpr, sqlb.Ident("tags"))
	if err != nil {
		return "", err
	}
	sourceValueExpr, err := storage.CompileSourceValueTemplate(valueExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
	if err != nil {
		return "", err
	}
	sourceValueSQL, params, err := sqlb.BuildExpr(sourceValueExpr)
	if err != nil {
		return "", err
	}
	if len(params) != 0 {
		return "", fmt.Errorf("range wrapper source value template unexpectedly produced params: %#v", params)
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sourceTagsExpr, Alias: "tags"}, {Expr: sqlb.RawLit{V: "arrayMap(point -> (point.1, " + sourceValueSQL + "), time_series)"}, Alias: "time_series"}},
		From:    rawRenderedSubquerySource(sourceSQL),
	}
	return buildNativeWrapperSQL(query)
}

func selectorEffectiveMatchers(selector *native.SelectorSource) []*labels.Matcher {
	if selector == nil {
		return nil
	}
	if len(selector.PushedMatchers) > 0 {
		return native.CloneMatchers(selector.PushedMatchers)
	}
	matchers := native.CloneMatchers(selector.Matchers)
	matchers = append(matchers, native.CloneMatchers(selector.InferredMatchers)...)
	return matchers
}

func alignSubqueryStepStart(windowStartMS, stepMS int64) int64 {
	if stepMS <= 0 {
		return windowStartMS
	}
	aligned := (windowStartMS / stepMS) * stepMS
	if aligned < windowStartMS {
		aligned += stepMS
	}
	return aligned
}

// logicalRangeRequiredBoundsForChild walks the logical plan tree
// returning the range envelope widened by the base range-vector
// selector's lookback/offset. Plan shapes without a discoverable base
// range-vector selector leave the (startMS, endMS) envelope unchanged.
func logicalRangeRequiredBoundsForChild(child logicalpkg.Node, startMS, endMS int64) (int64, int64) {
	lookbackMS, offsetMS, ok := logicalBaseSelectorBounds(child)
	if !ok {
		return startMS, endMS
	}
	return startMS - offsetMS - lookbackMS, endMS - offsetMS
}

// logicalBaseSelectorBounds walks a logical plan until it finds a
// LeafExprPlan whose underlying parser.Expr is a selector, and returns
// that selector's effective (lookback, offset) in milliseconds. The
// mapping matches native.BuildSelectorSource: a MatrixSelector
// contributes matrix.Range as lookback; a plain VectorSelector
// contributes DefaultInstantSelectorLookback (the instant-mode staleness
// window). Offset comes from VectorSelector.OriginalOffset in both
// cases. The walk mirrors native.BaseSelectorSource: it descends child
// links across every plan kind that can host a selector leaf in its
// subtree, stopping at the first matching leaf.
func logicalBaseSelectorBounds(n logicalpkg.Node) (lookbackMS, offsetMS int64, ok bool) {
	switch p := n.(type) {
	case nil:
		return 0, 0, false
	case *logicalpkg.LeafExprPlan:
		switch sel := p.Expr.(type) {
		case *parser.MatrixSelector:
			vec, matches := sel.VectorSelector.(*parser.VectorSelector)
			if !matches {
				return 0, 0, false
			}
			return sel.Range.Milliseconds(), vec.OriginalOffset.Milliseconds(), true
		case *parser.VectorSelector:
			return native.DefaultInstantSelectorLookback.Milliseconds(), sel.OriginalOffset.Milliseconds(), true
		default:
			return 0, 0, false
		}
	case *logicalpkg.AggregationPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.RangeFunctionPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.RatePlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.IncreasePlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.DeltaPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.ChangesPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.DerivPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.QuantileOverTimePlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.SubqueryPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.ScalarConvertPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.InfoPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.AbsentPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.AbsentOverTimePlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.HistogramProjectionPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.HistogramQuantilePlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.HistogramFractionPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.HistogramQuantilesPlan:
		if lb, off, found := logicalBaseSelectorBounds(p.Child); found {
			return lb, off, true
		}
		for _, q := range p.ParamChildren {
			if lb, off, found := logicalBaseSelectorBounds(q); found {
				return lb, off, true
			}
		}
		return 0, 0, false
	case *logicalpkg.SortPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.RoundPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.LabelReplacePlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.LabelJoinPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.PointwiseFunctionPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.UnaryPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.VectorPlan:
		return logicalBaseSelectorBounds(p.Child)
	case *logicalpkg.BinaryPlan:
		if lb, off, found := logicalBaseSelectorBounds(p.LHS); found {
			return lb, off, true
		}
		return logicalBaseSelectorBounds(p.RHS)
	default:
		return 0, 0, false
	}
}

// logicalBaseLeafNode walks a logical plan until it finds the base
// LeafExprPlan whose underlying parser.Expr is a selector. Returns nil
// when the subtree has no such leaf. The walk mirrors
// logicalBaseSelectorBounds / native.BaseSelectorSource.
//
// Used by helpers that need the base leaf's LoweringInfo (for
// LeafSelector, SourceExpr, etc.) rather than a summarized value like
// (lookback, offset).
func logicalBaseLeafNode(n logicalpkg.Node) *logicalpkg.LeafExprPlan {
	switch p := n.(type) {
	case nil:
		return nil
	case *logicalpkg.LeafExprPlan:
		switch p.Expr.(type) {
		case *parser.MatrixSelector, *parser.VectorSelector:
			return p
		default:
			return nil
		}
	case *logicalpkg.AggregationPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.RangeFunctionPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.RatePlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.IncreasePlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.DeltaPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.ChangesPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.DerivPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.QuantileOverTimePlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.SubqueryPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.ScalarConvertPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.InfoPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.AbsentPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.AbsentOverTimePlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.HistogramProjectionPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.HistogramQuantilePlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.HistogramFractionPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.HistogramQuantilesPlan:
		if leaf := logicalBaseLeafNode(p.Child); leaf != nil {
			return leaf
		}
		for _, q := range p.ParamChildren {
			if leaf := logicalBaseLeafNode(q); leaf != nil {
				return leaf
			}
		}
		return nil
	case *logicalpkg.SortPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.RoundPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.LabelReplacePlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.LabelJoinPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.PointwiseFunctionPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.UnaryPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.VectorPlan:
		return logicalBaseLeafNode(p.Child)
	case *logicalpkg.BinaryPlan:
		if leaf := logicalBaseLeafNode(p.LHS); leaf != nil {
			return leaf
		}
		return logicalBaseLeafNode(p.RHS)
	default:
		return nil
	}
}

// logicalResolvedAnchorTimeMS mirrors native.resolvedFragmentAnchorTimeMS on
// the logical plan tree. When any node in the subtree pins evaluation to an @
// timestamp or start()/end(), the pinned time is returned; otherwise the
// boolean is false and the outer range envelope is used unchanged. The walk
// covers every plan kind that native.resolvedFragmentAnchorTimeMS descends
// into on the fragment side.
func logicalResolvedAnchorTimeMS(n logicalpkg.Node, ctx native.OptimizationContext) (int64, bool) {
	switch p := n.(type) {
	case nil:
		return 0, false
	case *logicalpkg.LeafExprPlan:
		switch sel := p.Expr.(type) {
		case *parser.MatrixSelector:
			vec, matches := sel.VectorSelector.(*parser.VectorSelector)
			if !matches {
				return 0, false
			}
			return resolvedVectorSelectorAnchorTimeMS(vec, ctx)
		case *parser.VectorSelector:
			return resolvedVectorSelectorAnchorTimeMS(sel, ctx)
		default:
			return 0, false
		}
	case *logicalpkg.SubqueryPlan:
		if p.Timestamp != nil {
			return *p.Timestamp, true
		}
		switch p.StartOrEnd {
		case parser.START:
			if ctx.Mode == native.RenderModeRange {
				return ctx.StartMS, true
			}
			return ctx.EvaluationTimeMS, true
		case parser.END:
			if ctx.Mode == native.RenderModeRange {
				return ctx.EndMS, true
			}
			return ctx.EvaluationTimeMS, true
		}
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.AggregationPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.RangeFunctionPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.RatePlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.IncreasePlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.DeltaPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.ChangesPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.DerivPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.QuantileOverTimePlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.ScalarConvertPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.InfoPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.AbsentPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.AbsentOverTimePlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.HistogramProjectionPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.HistogramQuantilePlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.HistogramFractionPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.HistogramQuantilesPlan:
		if ts, ok := logicalResolvedAnchorTimeMS(p.Child, ctx); ok {
			return ts, true
		}
		for _, q := range p.ParamChildren {
			if ts, ok := logicalResolvedAnchorTimeMS(q, ctx); ok {
				return ts, true
			}
		}
		return 0, false
	case *logicalpkg.SortPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.RoundPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.LabelReplacePlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.LabelJoinPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.PointwiseFunctionPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.UnaryPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.VectorPlan:
		return logicalResolvedAnchorTimeMS(p.Child, ctx)
	case *logicalpkg.BinaryPlan:
		if ts, ok := logicalResolvedAnchorTimeMS(p.LHS, ctx); ok {
			return ts, true
		}
		return logicalResolvedAnchorTimeMS(p.RHS, ctx)
	default:
		return 0, false
	}
}

func resolvedVectorSelectorAnchorTimeMS(sel *parser.VectorSelector, ctx native.OptimizationContext) (int64, bool) {
	if sel == nil {
		return 0, false
	}
	if sel.Timestamp != nil {
		return *sel.Timestamp, true
	}
	switch sel.StartOrEnd {
	case parser.START:
		if ctx.Mode == native.RenderModeRange {
			return ctx.StartMS, true
		}
		return ctx.EvaluationTimeMS, true
	case parser.END:
		if ctx.Mode == native.RenderModeRange {
			return ctx.EndMS, true
		}
		return ctx.EvaluationTimeMS, true
	}
	return 0, false
}

// logicalRequiredInputBounds derives the [startMS, endMS] envelope for a
// logical subtree: lookback/offset via logicalBaseSelectorBounds and anchor
// resolution via logicalResolvedAnchorTimeMS. Returns ok=false when the
// subtree has no discoverable selector leaf, so the caller falls back on the
// outer envelope.
// LogicalRequiredInputBounds is the exported wrapper around
// logicalRequiredInputBounds. External callers (e.g. local/native_subtree)
// use it to derive the required [startMS, endMS] envelope from a logical
// plan tree.
func LogicalRequiredInputBounds(n logicalpkg.Node, ctx native.OptimizationContext) (int64, int64, bool) {
	return logicalRequiredInputBounds(n, ctx)
}

func logicalRequiredInputBounds(n logicalpkg.Node, ctx native.OptimizationContext) (int64, int64, bool) {
	lookbackMS, offsetMS, ok := logicalBaseSelectorBounds(n)
	if !ok {
		return 0, 0, false
	}
	switch ctx.Mode {
	case native.RenderModeInstant:
		anchorMS := ctx.EvaluationTimeMS
		if resolved, resolvedOK := logicalResolvedAnchorTimeMS(n, ctx); resolvedOK {
			anchorMS = resolved
		}
		endMS := anchorMS - offsetMS
		startMS := endMS - lookbackMS
		return startMS, endMS, true
	case native.RenderModeRange:
		if anchorMS, resolvedOK := logicalResolvedAnchorTimeMS(n, ctx); resolvedOK {
			endMS := anchorMS - offsetMS
			startMS := endMS - lookbackMS
			return startMS, endMS, true
		}
		endMS := ctx.EndMS - offsetMS
		startMS := ctx.StartMS - offsetMS - lookbackMS
		return startMS, endMS, true
	default:
		if ctx.EvaluationTimeMS == 0 && ctx.StartMS == 0 && ctx.EndMS == 0 {
			return 0, 0, false
		}
		endMS := ctx.EvaluationTimeMS - offsetMS
		startMS := endMS - lookbackMS
		return startMS, endMS, true
	}
}

// renderLogicalSubquery renders a logical.Node via renderer.Lower — the full
// logical-plan dispatcher — and then namespaces the result so the caller can
// embed the SQL inside a parent subquery without colliding on placeholder
// names.
//
// The returned SQL has the trailing FORMAT/SETTINGS lines stripped
// (trimRenderedQuerySQL).
func renderLogicalSubquery(cfg storage.QueryConfig, node logicalpkg.Node, logicalAnalysis *logicalpkg.Analysis, nativeAnalysis *native.Analysis, params RenderParams, prefix string) (string, map[string]string, error) {
	rendered, err := Lower(LoweringCtx{
		Config:         cfg,
		Analysis:       logicalAnalysis,
		NativeAnalysis: nativeAnalysis,
		Params:         params,
	}, node)
	if err != nil {
		return "", nil, err
	}
	return namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, prefix)
}

func namespaceRenderedQuery(sql string, queryParams map[string]string, prefix string) (string, map[string]string, error) {
	if prefix == "" {
		return sql, queryParams, nil
	}
	renamed := map[string]string{}
	for key, value := range queryParams {
		placeholderKey := strings.TrimPrefix(key, "param_")
		if placeholderKey == key {
			return "", nil, fmt.Errorf("native query parameter %q is missing param_ prefix", key)
		}
		newPlaceholderKey := prefix + "_" + placeholderKey
		newKey := "param_" + newPlaceholderKey
		sql = strings.ReplaceAll(sql, "{"+placeholderKey+":", "{"+newPlaceholderKey+":")
		renamed[newKey] = value
	}
	return sql, renamed, nil
}
