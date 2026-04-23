package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerAbsent lowers either an AbsentPlan or an AbsentOverTimePlan to a
// RenderedQuery via the existing Fragment renderer internals.
//
// Surface 11 uses the "Approach A" dispatch port: the lowerer reads the
// pre-computed Fragment from ctx.NativeAnalysis.InfoFor(n).Fragment,
// validates the kind is FragmentKindAbsent, then delegates to
// renderAbsentFragment so SQL stays byte-identical to the Fragment path.
// The render body retires with the final cleanup commit once all surfaces
// have ported.
//
// Hierarchical fallback: if native.Analyze didn't mark this node as
// native-lowerable (e.g. because the child selector isn't lowerable),
// nativeInfo.Fragment will be nil and we return errUnsupportedLowerNode
// so the caller falls back to the Fragment rendering path wholesale.
//
// Supported plans: AbsentPlan (→ absent), AbsentOverTimePlan (→ absent_over_time).
func lowerAbsent(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAbsent called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: absent missing logical analysis")
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindAbsent {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderAbsentFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
