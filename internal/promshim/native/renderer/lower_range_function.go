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
// Surface 7 uses the "Approach A" dispatch port: rather than rewriting the
// renderer body, the lowerer reads the pre-computed Fragment from
// ctx.NativeAnalysis.InfoFor(n).Fragment, validates the kind, then delegates
// to renderRangeFunctionFragment so SQL stays byte-identical to the Fragment
// path. The render body retires with the final cleanup commit once all surfaces
// have ported.
//
// Hierarchical fallback: if native.Analyze didn't mark this node as
// native-lowerable (e.g. because the child selector isn't lowerable yet),
// nativeInfo.Fragment will be nil and we return errUnsupportedLowerNode so the
// caller falls back to the Fragment rendering path wholesale.
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
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindRangeFunction {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderRangeFunctionFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
