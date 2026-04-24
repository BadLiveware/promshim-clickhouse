package renderer

import (
	"fmt"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

// renderHistogramProjectionLogical renders a HistogramProjectionPlan.
// Tag-narrowing for a grouping-aggregation child is threaded through
// RenderParams using decideHistogramChildNarrowing; non-aggregation
// children leave params untouched so the underlying SelectorSource
// keeps governing.
func renderHistogramProjectionLogical(cfg storage.QueryConfig, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, n *logicalpkg.HistogramProjectionPlan, params RenderParams) (renderedFragment, error) {
	if n == nil || n.Child == nil {
		return renderedFragment{}, fmt.Errorf("renderer: histogram projection requires a child")
	}

	// Thread tag-narrowing for a grouping-aggregation child via
	// RenderParams. Non-aggregation children leave the params untouched
	// so the underlying SelectorSource keeps governing.
	childParams := params
	if aggChild, ok := n.Child.(*logicalpkg.AggregationPlan); ok {
		requireFull, labels := decideHistogramChildNarrowing(aggChild)
		childParams.RequireFullTags = requireFull
		childParams.RequiredTagLabels = labels
	}

	histograms, err := renderClassicHistogramGroupsQueryLogical(cfg, n.Child, logicalAnalysis, analysis, childParams, "histogram_projection_child")
	if err != nil {
		return renderedFragment{}, err
	}
	valueExpr, err := classicHistogramProjectionValueExpr(sqlb.Ident("buckets"), n.Func)
	if err != nil {
		return renderedFragment{}, err
	}
	return histogramOutputFragment(histograms, valueExpr, params.Mode, "histogram_projection_steps"), nil
}

// renderHistogramFunctionLogical renders a histogram-function plan node
// (HistogramQuantilePlan, HistogramFractionPlan, or HistogramQuantilesPlan).
// Tag-narrowing for a grouping-aggregation child is threaded through
// RenderParams using histogramFunctionChildAggregation +
// decideHistogramChildNarrowing, mirroring the projection approach in
// renderHistogramProjectionLogical.
func renderHistogramFunctionLogical(cfg storage.QueryConfig, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, n logicalpkg.Node, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: histogram function requires a node")
	}
	return renderHistogramFunctionLogicalDirect(cfg, n, logicalAnalysis, analysis, params)
}

// renderHistogramFunctionLogicalDirect reads the child and per-function
// scalars from the logical plan, delegates the child histogram
// materialization to renderClassicHistogramGroupsQueryLogical, and
// dispatches on the histogram-function kind. The histogram_quantiles
// branch iterates per-quantile scalar bindings off
// HistogramQuantilesPlan.ParamChildren and lowers them through
// renderHistogramQuantilesLogical.
func renderHistogramFunctionLogicalDirect(cfg storage.QueryConfig, node logicalpkg.Node, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, params RenderParams) (renderedFragment, error) {
	childNode, funcName, ok := histogramFunctionChildNode(node)
	if !ok || childNode == nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}

	// Compute narrowing from the histogram-function's child if it is a
	// grouping aggregation. Non-aggregation children leave params untouched.
	childParams := params
	if aggChild := histogramFunctionChildAggregation(node); aggChild != nil {
		requireFull, labels := decideHistogramChildNarrowing(aggChild)
		childParams.RequireFullTags = requireFull
		childParams.RequiredTagLabels = labels
	}

	histograms, err := renderClassicHistogramGroupsQueryLogical(cfg, childNode, logicalAnalysis, analysis, childParams, "histogram_function_child")
	if err != nil {
		return renderedFragment{}, err
	}

	switch funcName {
	case "histogram_quantile":
		q, qok := node.(*logicalpkg.HistogramQuantilePlan)
		if !qok {
			return renderedFragment{}, errUnsupportedLowerNode
		}
		prepared, err := renderPreparedClassicHistogramQuery(histograms, "histogram_quantile_prepared")
		if err != nil {
			return renderedFragment{}, err
		}
		rowsSQL := renderHistogramQuantileRowsSQL(trimRenderedQuerySQL(prepared.SQL), storage.NativeFloatLiteral(q.Quantile), "", "histogram_quantile")
		finalSQL := wrapHistogramRowsForMode(rowsSQL, params.Mode, "histogram_quantile_rows")
		if params.Mode == native.RenderModeRange && histogramChildUsesOnlyLETagsLogical(childNode) {
			finalSQL = wrapHistogramRowsForConstantEmptyTagsRange(rowsSQL, "histogram_quantile_rows")
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(finalSQL), ExtraParams: prepared.QueryParams}, nil
	case "histogram_quantiles":
		q, qok := node.(*logicalpkg.HistogramQuantilesPlan)
		if !qok {
			return renderedFragment{}, errUnsupportedLowerNode
		}
		prepared, err := renderPreparedClassicHistogramQuery(histograms, "histogram_quantiles_prepared")
		if err != nil {
			return renderedFragment{}, err
		}
		ctx := LoweringCtx{
			Config:         cfg,
			Analysis:       logicalAnalysis,
			NativeAnalysis: analysis,
			Params:         params,
		}
		return renderHistogramQuantilesLogical(ctx, q, params, prepared)
	case "histogram_fraction":
		f, fok := node.(*logicalpkg.HistogramFractionPlan)
		if !fok {
			return renderedFragment{}, errUnsupportedLowerNode
		}
		valueExpr := classicHistogramFractionValueExpr(sqlb.Ident("buckets"), f.Lower, f.Upper)
		return histogramOutputFragment(histograms, valueExpr, params.Mode, "histogram_function_steps"), nil
	default:
		return renderedFragment{}, fmt.Errorf("histogram function %q is not implemented yet", funcName)
	}
}

// histogramFunctionChildNode returns the child logical node and the
// histogram-function name for any of the three histogram-function plan
// kinds. Returns ok=false for unrelated nodes.
func histogramFunctionChildNode(n logicalpkg.Node) (logicalpkg.Node, string, bool) {
	switch h := n.(type) {
	case *logicalpkg.HistogramQuantilePlan:
		return h.Child, "histogram_quantile", true
	case *logicalpkg.HistogramFractionPlan:
		return h.Child, "histogram_fraction", true
	case *logicalpkg.HistogramQuantilesPlan:
		return h.Child, "histogram_quantiles", true
	default:
		return nil, "", false
	}
}

// histogramFunctionChildAggregation returns the immediate AggregationPlan
// child of any histogram-function plan kind, or nil when the child is not a
// grouping aggregation. If the user wraps a grouping aggregation in a range
// function (e.g. sum by (le)(rate(...))), the range function is already
// inside the aggregation, so the immediate child is the AggregationPlan.
// If there is no aggregation at all (e.g. histogram_quantile(q, rate(...))),
// the immediate child is the range-function node itself and nil is returned
// so narrowing is correctly skipped.
func histogramFunctionChildAggregation(n logicalpkg.Node) *logicalpkg.AggregationPlan {
	switch h := n.(type) {
	case *logicalpkg.HistogramQuantilePlan:
		return asAggregation(h.Child)
	case *logicalpkg.HistogramFractionPlan:
		return asAggregation(h.Child)
	case *logicalpkg.HistogramQuantilesPlan:
		return asAggregation(h.Child)
	default:
		return nil
	}
}

// asAggregation returns n as an *AggregationPlan, or nil if it is not one.
func asAggregation(n logicalpkg.Node) *logicalpkg.AggregationPlan {
	if agg, ok := n.(*logicalpkg.AggregationPlan); ok {
		return agg
	}
	return nil
}

// histogramChildUsesOnlyLETagsLogical is the logical-plan analog of
// histogramChildUsesOnlyLETags. It reports whether the immediate child
// is a grouping aggregation `sum/avg/... by (le) (...)` whose output
// carries only the "le" label, which drives the renderer's identity-tag
// shortcut (empty output tags, timestamp-only grouping).
//
// The helper peers through the logical tree via a Go type assertion and
// defers to childAggregationUsesOnlyLETags for the shape check.
// Non-aggregation children return false, which preserves the full-tag
// code path in renderClassicHistogramGroupsQueryLogical.
func histogramChildUsesOnlyLETagsLogical(childNode logicalpkg.Node) bool {
	agg, ok := childNode.(*logicalpkg.AggregationPlan)
	if !ok {
		return false
	}
	return childAggregationUsesOnlyLETags(agg)
}

// renderClassicHistogramGroupsQueryLogical normalizes classic histogram
// bucket vectors into one grouped row per histogram identity and timestamp.
// It reads the child via a logical.Node + analysis.
//
// Tag-narrowing for a grouping aggregation child flows through
// RenderParams (set by decideHistogramChildNarrowing on the caller
// side), which the downstream SQL builders honor via
// applyRenderParamsNarrowing in renderLeafLogical / renderSourceExprView /
// renderAggregationSourceView.
func renderClassicHistogramGroupsQueryLogical(cfg storage.QueryConfig, childNode logicalpkg.Node, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, params RenderParams, prefix string) (renderedFragment, error) {
	if childNode == nil {
		return renderedFragment{}, fmt.Errorf("classic histogram materialization requires a child")
	}

	onlyLETags := histogramChildUsesOnlyLETagsLogical(childNode)

	leRaw := sqlb.RawLit{V: "tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2)"}
	upperBoundExpr := sqlb.RawLit{V: "multiIf(le_raw IN ['+Inf', 'Inf', '+inf', 'inf'], inf, le_raw IN ['-Inf', '-inf'], -inf, toFloat64OrNull(le_raw))"}
	histogramTagsExpr := sqlb.Expr(sqlb.RawLit{V: "arrayFilter(tag -> tag.1 != 'le' AND tag.1 != '__name__', tags)"})
	if onlyLETags {
		histogramTagsExpr = emit.EmptyTagsArray()
	}
	whereExpr := sqlb.RawLit{V: "le_raw != '' AND upper_bound IS NOT NULL"}

	var (
		flattened   *sqlb.Select
		childParams map[string]string
	)
	switch params.Mode {
	case native.RenderModeInstant:
		if childRowsSQL, namespacedParams, ok, err := tryRenderHistogramChildRowsSQLLogical(cfg, childNode, logicalAnalysis, analysis, params, prefix); err != nil {
			return renderedFragment{}, err
		} else if ok {
			childParams = namespacedParams
			innerRows := &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: sqlb.Ident("tags"), Alias: "tags"},
					{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
					{Expr: sqlb.Ident("value"), Alias: "value"},
					{Expr: leRaw, Alias: "le_raw"},
				},
				From: rawRenderedSubquerySourceWithAlias(childRowsSQL, "histogram_child_rows"),
			}
			flattened = &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: histogramTagsExpr, Alias: "histogram_tags"},
					{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
					{Expr: upperBoundExpr, Alias: "upper_bound"},
					{Expr: sqlb.RawLit{V: "ifNull(toFloat64(value), nan)"}, Alias: "cumulative_count"},
				},
				From:  sqlb.SubSelect{S: innerRows, Alias: "histogram_points"},
				Where: whereExpr,
			}
		} else {
			childSQL, namespacedParams, err := renderLogicalSubquery(cfg, childNode, logicalAnalysis, analysis, params, prefix)
			if err != nil {
				return renderedFragment{}, err
			}
			childParams = namespacedParams
			innerPoints := &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: sqlb.Ident("tags"), Alias: "tags"},
					{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
					{Expr: sqlb.Ident("value"), Alias: "value"},
					{Expr: leRaw, Alias: "le_raw"},
				},
				From: rawRenderedSubquerySourceWithAlias(childSQL, "histogram_child"),
			}
			flattened = &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: histogramTagsExpr, Alias: "histogram_tags"},
					{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
					{Expr: upperBoundExpr, Alias: "upper_bound"},
					{Expr: sqlb.RawLit{V: "ifNull(toFloat64(value), nan)"}, Alias: "cumulative_count"},
				},
				From:  sqlb.SubSelect{S: innerPoints, Alias: "histogram_points"},
				Where: whereExpr,
			}
		}
	case native.RenderModeRange:
		if childRowsSQL, namespacedParams, ok, err := tryRenderHistogramChildRowsSQLLogical(cfg, childNode, logicalAnalysis, analysis, params, prefix); err != nil {
			return renderedFragment{}, err
		} else if ok {
			childParams = namespacedParams
			innerRows := &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: sqlb.Ident("tags"), Alias: "tags"},
					{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
					{Expr: sqlb.Ident("value"), Alias: "value"},
					{Expr: leRaw, Alias: "le_raw"},
				},
				From: rawRenderedSubquerySourceWithAlias(childRowsSQL, "histogram_child_rows"),
			}
			flattened = &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: histogramTagsExpr, Alias: "histogram_tags"},
					{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
					{Expr: upperBoundExpr, Alias: "upper_bound"},
					{Expr: sqlb.RawLit{V: "ifNull(toFloat64(value), nan)"}, Alias: "cumulative_count"},
				},
				From:  sqlb.SubSelect{S: innerRows, Alias: "histogram_points"},
				Where: whereExpr,
			}
		} else {
			childSQL, namespacedParams, err := renderLogicalSubquery(cfg, childNode, logicalAnalysis, analysis, params, prefix)
			if err != nil {
				return renderedFragment{}, err
			}
			childParams = namespacedParams
			innerSeries := &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: sqlb.Ident("tags"), Alias: "tags"},
					{Expr: sqlb.Ident("time_series"), Alias: "time_series"},
					{Expr: leRaw, Alias: "le_raw"},
				},
				From: rawRenderedSubquerySourceWithAlias(childSQL, "histogram_child"),
			}
			flattened = &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: histogramTagsExpr, Alias: "histogram_tags"},
					{Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"},
					{Expr: upperBoundExpr, Alias: "upper_bound"},
					{Expr: sqlb.RawLit{V: "ifNull(toFloat64(point.2), nan)"}, Alias: "cumulative_count"},
				},
				From: sqlb.ArrayJoin{
					Base:  sqlb.SubSelect{S: innerSeries, Alias: "histogram_series"},
					Expr:  sqlb.RawLit{V: "histogram_series.time_series"},
					Alias: "point",
				},
				Where: whereExpr,
			}
		}
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}

	coalescedColumns := []sqlb.ColExpr{
		{Expr: sqlb.Ident("histogram_tags"), Alias: "histogram_tags"},
		{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
		{Expr: sqlb.Ident("upper_bound"), Alias: "upper_bound"},
		{Expr: sqlb.Call{Name: "sum", Args: []sqlb.Expr{sqlb.Ident("cumulative_count")}}, Alias: "cumulative_count"},
	}
	coalescedGroupBy := []sqlb.Expr{sqlb.Ident("histogram_tags"), sqlb.Ident("timestamp"), sqlb.Ident("upper_bound")}
	groupedColumns := []sqlb.ColExpr{
		{Expr: sqlb.Ident("histogram_tags"), Alias: "tags"},
		{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
		{Expr: sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((upper_bound, cumulative_count)))"}, Alias: "buckets"},
	}
	groupedGroupBy := []sqlb.Expr{sqlb.Ident("histogram_tags"), sqlb.Ident("timestamp")}
	if onlyLETags {
		coalescedColumns[0] = sqlb.ColExpr{Expr: emit.EmptyTagsArray(), Alias: "histogram_tags"}
		coalescedGroupBy = []sqlb.Expr{sqlb.Ident("timestamp"), sqlb.Ident("upper_bound")}
		groupedColumns[0] = sqlb.ColExpr{Expr: emit.EmptyTagsArray(), Alias: "tags"}
		groupedGroupBy = []sqlb.Expr{sqlb.Ident("timestamp")}
	}

	coalesced := &sqlb.Select{
		Columns: coalescedColumns,
		From:    sqlb.SubSelect{S: flattened, Alias: "histogram_rows"},
		GroupBy: coalescedGroupBy,
	}

	grouped := &sqlb.Select{
		Columns: groupedColumns,
		From:    sqlb.SubSelect{S: coalesced, Alias: "coalesced_histogram_rows"},
		GroupBy: groupedGroupBy,
	}
	return renderedFragment{Select: grouped, ExtraParams: childParams}, nil
}
