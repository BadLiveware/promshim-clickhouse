package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerSubquery lowers a SubqueryPlan (PromQL subquery expressions like
// `up[5m:1m]`) to a RenderedQuery via the existing Fragment renderer
// internals.
//
// Surface 3 uses the "Approach A" dispatch port: rather than rewriting the
// renderer body, the lowerer builds a FragmentKindSubquery NativeFragment
// and delegates to RenderFragment so SQL stays byte-identical to the
// Fragment path. The render body retires with the final cleanup commit once
// all surfaces have ported.
//
// Hierarchical fallback: if the child isn't marked native-lowerable by
// native.Analyze (child.Fragment == nil), the info.Fragment on this node
// will be nil and we return errUnsupportedLowerNode so the caller can fall
// back to the Fragment rendering path wholesale.
func lowerSubquery(ctx LoweringCtx, n *logicalpkg.SubqueryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerSubquery called with nil node")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: subquery missing child node")
	}
	if ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: subquery missing analysis")
	}
	// If native.Analyze didn't mark this node as native-lowerable (e.g.
	// because the child vector isn't lowerable yet), BuildFragment would
	// report a non-sentinel error. Translate that to the Lower sentinel
	// so the caller falls back hierarchically to the Fragment path.
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}
