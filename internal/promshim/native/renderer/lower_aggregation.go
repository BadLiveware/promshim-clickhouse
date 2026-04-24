package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerAggregation lowers any *logicalpkg.AggregationPlan (the single logical
// node kind that produces FragmentKindAggregation) to a RenderedQuery by
// delegating to renderAggregationLogical in aggregation_logical.go.
//
// The lowerer no longer calls the Fragment builder at the boundary; Fragment
// materialization is performed transitionally inside renderAggregationLogical
// (Phase A4 scaffolding; retires in Phase C).
//
// Hierarchical fallback: if renderAggregationLogical returns
// errUnsupportedLowerNode the caller falls back to the Fragment rendering
// path wholesale.
//
// Covered ops: sum, avg, count, min, max, stddev, stdvar, topk, bottomk,
// quantile, group, count_values, with or without label groupings. The
// aggregation-range-fused path is handled transparently inside
// renderAggregationFragment via tryRenderFusedRangeAggregationFragment.
func lowerAggregation(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAggregation called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: aggregation missing logical analysis")
	}
	rendered, err := renderAggregationLogical(ctx.Config, ctx.Analysis, ctx.NativeAnalysis, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
