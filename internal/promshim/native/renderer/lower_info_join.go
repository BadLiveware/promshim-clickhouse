package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerInfoJoin lowers an InfoPlan to a RenderedQuery via the existing
// Fragment renderer internals.
//
// Surface 12 uses the "Approach A" dispatch port: the lowerer reads the
// pre-computed Fragment from ctx.NativeAnalysis.InfoFor(n).Fragment,
// validates the kind is FragmentKindInfoJoin, then delegates to
// renderInfoJoinFragment so SQL stays byte-identical to the Fragment path.
//
// Hierarchical fallback: if native.Analyze didn't mark this node as
// native-lowerable (e.g. because the child selector isn't lowerable),
// nativeInfo.Fragment will be nil and we return errUnsupportedLowerNode
// so the caller falls back to the Fragment rendering path wholesale.
func lowerInfoJoin(ctx LoweringCtx, n *logicalpkg.InfoPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerInfoJoin called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: info missing logical analysis")
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindInfoJoin {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderInfoJoinFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
