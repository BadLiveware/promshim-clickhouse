package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerHistogramFunction lowers any of the three histogram-function plan
// kinds (HistogramQuantilePlan, HistogramFractionPlan,
// HistogramQuantilesPlan) to a RenderedQuery via the existing Fragment
// renderer internals.
//
// Transitional dispatch port: the lowerer builds the Fragment on demand via
// native.BuildFragment (which reuses the analysis side-map when present or
// rebuilds it otherwise), validates the kind is FragmentKindHistogramFunction,
// then delegates to renderHistogramFunctionFragment so SQL stays
// byte-identical to the Fragment path. Once the histogram body ports to
// logical children, this lowerer can drop the Fragment materialization.
//
// Hierarchical fallback: if BuildFragment rejects the node (e.g. because the
// child selector isn't lowerable) we return errUnsupportedLowerNode so the
// caller falls back to the Fragment rendering path wholesale.
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
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindHistogramFunction {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderHistogramFunctionFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
