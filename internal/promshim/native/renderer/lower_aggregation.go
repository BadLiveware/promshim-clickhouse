package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

// lowerAggregation lowers any *logicalpkg.AggregationPlan to a
// RenderedQuery by delegating to renderAggregationLogical in
// aggregation_logical.go. The fused range+aggregation branch routes
// through tryRenderFusedRangeAggregationLogicalDirect and the
// non-fused branch through renderAggregationLogicalBody.
//
// Covered ops: sum, avg, count, min, max, stddev, stdvar, topk, bottomk,
// quantile, group, count_values, with or without label groupings.
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
