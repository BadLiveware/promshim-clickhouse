package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// lowerRound lowers a RoundPlan to a RenderedQuery via the existing
// Fragment renderer internals.
//
// Surface 14 uses the "Approach A" dispatch port: rather than rewriting
// the renderer body, the lowerer reads the pre-computed Fragment from
// ctx.NativeAnalysis.InfoFor(n).Fragment, then delegates to renderFragment
// so SQL stays byte-identical to the Fragment path.
//
// RoundPlan always produces FragmentKindValueTransform when natively
// lowerable. We do NOT hard-code a Kind check; we accept whatever
// Fragment native.Analyze computed. If no Fragment is present (node not
// natively lowerable), errUnsupportedLowerNode is returned so the caller
// falls back to the Fragment rendering path for the whole query.
func lowerRound(ctx LoweringCtx, n *logicalpkg.RoundPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerRound called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: round missing logical analysis")
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
