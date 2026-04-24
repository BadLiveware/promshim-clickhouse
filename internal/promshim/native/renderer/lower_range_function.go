package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// lowerRangeFunction lowers any of the seven logical node kinds for
// range functions to a RenderedQuery by delegating to
// renderRangeFunctionLogical in range_logical.go.
//
// All seven plan kinds map to this single shared lowerer:
//   - RangeFunctionPlan  (avg_over_time, sum_over_time, min_over_time, max_over_time,
//     count_over_time, stddev_over_time, stdvar_over_time, last_over_time,
//     present_over_time, mad_over_time, predict_linear, holt_winters)
//   - RatePlan
//   - IncreasePlan
//   - DeltaPlan
//   - ChangesPlan
//   - DerivPlan
//   - QuantileOverTimePlan
func lowerRangeFunction(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerRangeFunction called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: range function missing logical analysis")
	}
	rendered, err := renderRangeFunctionLogical(ctx, n)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
