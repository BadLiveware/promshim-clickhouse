package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerAggregation lowers any *logicalpkg.AggregationPlan (the single logical
// node kind that produces FragmentKindAggregation) to a RenderedQuery via the
// existing Fragment renderer internals.
//
// Surface 8 uses the "Approach A" dispatch port: the lowerer reads the
// pre-computed Fragment from ctx.NativeAnalysis.InfoFor(n).Fragment, validates
// the kind, then delegates to renderAggregationFragment so SQL stays
// byte-identical to the Fragment path. The render body retires with the final
// cleanup commit once all surfaces have ported.
//
// The aggregation-range-fused path (where the aggregation's source fragment is
// a FragmentKindRangeFunction) is handled naturally inside
// renderAggregationFragment via tryRenderFusedRangeAggregationFragment — no
// separate dispatch is required here.
//
// Covered ops: sum, avg, count, min, max, stddev, stdvar, topk, bottomk,
// quantile, group, count_values, with or without label groupings.
//
// Hierarchical fallback: if native.Analyze didn't mark this node as
// native-lowerable, nativeInfo.Fragment will be nil and we return
// errUnsupportedLowerNode so the caller falls back to the Fragment rendering
// path wholesale.
func lowerAggregation(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAggregation called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: aggregation missing logical analysis")
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindAggregation {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderAggregationFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
