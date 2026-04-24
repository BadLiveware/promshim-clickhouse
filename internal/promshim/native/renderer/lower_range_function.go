package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerRangeFunction lowers any of the seven logical node kinds that produce
// FragmentKindRangeFunction to a RenderedQuery via the existing Fragment
// renderer internals.
//
// Transitional dispatch port: the lowerer builds the Fragment on demand via
// native.BuildFragment (which reuses the analysis side-map when present or
// rebuilds it otherwise), validates the kind, then delegates to
// renderRangeFunctionFragment so SQL stays byte-identical to the Fragment
// path. Once renderRangeFunctionFragment and its structural fast paths port
// to logical children, this lowerer can drop the Fragment materialization.
//
// Hierarchical fallback: if BuildFragment rejects the node (e.g. because the
// child selector isn't lowerable yet) we return errUnsupportedLowerNode so
// the caller falls back to the Fragment rendering path wholesale.
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
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindRangeFunction {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderRangeFunctionFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
