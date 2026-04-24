package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerHistogramFunction lowers any of the three histogram-function plan
// kinds (HistogramQuantilePlan, HistogramFractionPlan,
// HistogramQuantilesPlan) to a RenderedQuery by delegating to
// renderHistogramFunctionLogical in histogram_logical.go.
//
// Hierarchical fallback: if renderHistogramFunctionLogical returns
// errUnsupportedLowerNode the caller falls back to the next execution tier.
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
	rendered, err := renderHistogramFunctionLogical(ctx.Config, ctx.Analysis, ctx.NativeAnalysis, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
