package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// lowerAggregation lowers any *logicalpkg.AggregationPlan (the single logical
// node kind that produces FragmentKindAggregation) to a RenderedQuery by
// delegating to renderAggregationLogical in aggregation_logical.go.
//
// Phase 6e (Task 13a Phase 6e): renderAggregationLogical no longer
// materializes the aggregation Fragment at the lowerer boundary. The
// fused range+aggregation branch routes through
// tryRenderFusedRangeAggregationLogicalDirect and the non-fused branch
// routes through renderAggregationLogicalBody — both consume the
// AggregationPlan directly.
//
// Hierarchical fallback: if renderAggregationLogical returns
// errUnsupportedLowerNode the caller falls back to the Fragment rendering
// path wholesale.
//
// Covered ops: sum, avg, count, min, max, stddev, stdvar, topk, bottomk,
// quantile, group, count_values, with or without label groupings. The
// aggregation-range-fused path is handled transparently inside
// renderAggregationLogicalDirect via
// tryRenderFusedRangeAggregationLogicalDirect.
func lowerAggregation(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAggregation called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: aggregation missing logical analysis")
	}
	rendered, err := renderAggregationLogical(ctx, n)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
