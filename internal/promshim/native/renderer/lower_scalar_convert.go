package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerScalarConvert renders scalar(vec) directly. The child vector is
// lowered via Lower(ctx, n.Child); the outer scalar-convert wrapping is
// shared with the Fragment path via renderScalarConvertFromSource, which
// locks byte-identity between the two paths structurally.
//
// Hierarchical fallback: if the child kind isn't yet direct-renderable,
// Lower returns errUnsupportedLowerNode and we propagate it so the caller
// falls back to the Fragment rendering path wholesale.
func lowerScalarConvert(ctx LoweringCtx, n *logicalpkg.ScalarConvertPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerScalarConvert called with nil")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: scalar_convert missing child")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: scalar_convert missing analysis")
	}
	childSource, childParams, err := renderLoweredChildAsSource(ctx, n.Child, "scalar_child")
	if err != nil {
		return RenderedQuery{}, err // bubble errUnsupportedLowerNode when child can't lower
	}
	rf, err := renderScalarConvertFromSource(childSource, childParams, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}
