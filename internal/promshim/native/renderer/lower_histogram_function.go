package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// lowerHistogramFunction lowers any of the three histogram-function plan
// kinds (HistogramQuantilePlan, HistogramFractionPlan,
// HistogramQuantilesPlan) to a RenderedQuery by delegating to
// renderHistogramFunctionLogical in histogram_logical.go.
//
// The lowerer no longer calls the Fragment builder at the boundary; Fragment
// materialization is performed transitionally inside
// renderHistogramFunctionLogical (Phase A2 scaffolding; retires in Phase C).
//
// Hierarchical fallback: if renderHistogramFunctionLogical returns
// errUnsupportedLowerNode the caller falls back to the Fragment rendering
// path wholesale.
//
// Supported functions: histogram_quantile, histogram_fraction,
// histogram_quantiles.
func lowerHistogramFunction(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerHistogramFunction called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: histogram function missing logical analysis")
	}
	rendered, err := renderHistogramFunctionLogical(ctx.Config, ctx.NativeAnalysis, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
