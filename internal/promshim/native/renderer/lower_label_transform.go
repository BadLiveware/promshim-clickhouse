package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerLabelTransform lowers a LabelReplacePlan or LabelJoinPlan to a
// RenderedQuery via the existing Fragment renderer internals.
//
// Surface 5 uses the "Approach A" dispatch port: rather than rewriting the
// renderer body, the lowerer builds a FragmentKindLabelTransform NativeFragment
// and delegates to RenderFragment so SQL stays byte-identical to the Fragment
// path. The render body retires with the final cleanup commit once all surfaces
// have ported.
//
// Hierarchical fallback: if the child isn't marked native-lowerable by
// native.Analyze (child.Fragment == nil), the info.Fragment on this node
// will be nil and we return errUnsupportedLowerNode so the caller can fall
// back to the Fragment rendering path wholesale.
//
// Both logical node kinds (LabelReplacePlan and LabelJoinPlan) produce
// FragmentKindLabelTransform and are handled uniformly here.
func lowerLabelTransform(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerLabelTransform called with nil node")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: label_transform missing analysis")
	}
	if ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: label_transform missing analysis for node")
	}
	// If native.Analyze didn't mark this node as native-lowerable (e.g.
	// because the child vector isn't lowerable yet), BuildFragment would
	// report a non-sentinel error. Translate that to the Lower sentinel
	// so the caller falls back hierarchically to the Fragment path.
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindLabelTransform {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}
