package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerClamp lowers a PointwiseFunctionPlan whose Func is one of clamp,
// clamp_min, or clamp_max to a RenderedQuery via the existing Fragment
// renderer internals.
//
// Surface 6 uses the "Approach A" dispatch port: rather than rewriting
// the renderer body, the lowerer reads the pre-computed Fragment from
// ctx.NativeAnalysis.InfoFor(n).Fragment, validates the kind, then
// delegates to renderClampTransformFragment so SQL stays byte-identical
// to the Fragment path. The render body retires with the final cleanup
// commit once all surfaces have ported.
//
// Hierarchical fallback: if native.Analyze didn't mark this node as
// native-lowerable (e.g. because the child or bounds aren't lowerable),
// nativeInfo.Fragment will be nil and we return errUnsupportedLowerNode
// so the caller falls back to the Fragment rendering path wholesale.
func lowerClamp(ctx LoweringCtx, n *logicalpkg.PointwiseFunctionPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerClamp called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: clamp missing logical analysis")
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindClampTransform {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderClampTransformFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}

// isClampFuncName reports whether name is one of the three clamp
// variants. Duplicated from native.isClampPointwiseFunction so the
// renderer package does not depend on the unexported helper. Keep in
// sync with analysis.go until the native package is retired.
func isClampFuncName(name string) bool {
	switch name {
	case "clamp", "clamp_min", "clamp_max":
		return true
	default:
		return false
	}
}
