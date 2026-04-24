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
// Transitional dispatch port: the lowerer builds the Fragment on demand via
// native.BuildFragment (which reuses the analysis side-map when present or
// rebuilds it otherwise), validates the kind, then delegates to
// renderAggregationFragment so SQL stays byte-identical to the Fragment path.
// Once renderAggregationFragment and the fused range-aggregation helpers port
// to logical children, this lowerer can drop the Fragment materialization.
//
// The aggregation-range-fused path (where the aggregation's source fragment is
// a FragmentKindRangeFunction) is handled naturally inside
// renderAggregationFragment via tryRenderFusedRangeAggregationFragment — no
// separate dispatch is required here.
//
// Covered ops: sum, avg, count, min, max, stddev, stdvar, topk, bottomk,
// quantile, group, count_values, with or without label groupings.
//
// Hierarchical fallback: if BuildFragment rejects the node (e.g. because the
// child selector isn't lowerable) we return errUnsupportedLowerNode so the
// caller falls back to the Fragment rendering path wholesale.
func lowerAggregation(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAggregation called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: aggregation missing logical analysis")
	}
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindAggregation {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderAggregationFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
