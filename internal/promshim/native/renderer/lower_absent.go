package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerAbsent lowers AbsentPlan directly via Lower on the child and the
// shared renderAbsentFromNamespacedChild helper. AbsentOverTimePlan still
// bridges through the Fragment path because its range-mode rendering needs
// the specialized windowed source helper that inspects the child fragment's
// kind (LeafSource vs Subquery).
//
// Hierarchical fallback: if the child cannot be lowered directly, Lower
// returns errUnsupportedLowerNode which bubbles up so the whole query falls
// back to Fragment rendering.
func lowerAbsent(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAbsent called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: absent missing logical analysis")
	}
	switch p := n.(type) {
	case *logicalpkg.AbsentPlan:
		return lowerAbsentPlanDirect(ctx, p)
	case *logicalpkg.AbsentOverTimePlan:
		return lowerAbsentOverTimeViaFragment(ctx, p)
	default:
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAbsent called with unexpected node type %T", n)
	}
}

// lowerAbsentPlanDirect renders AbsentPlan without constructing an
// intermediate NativeFragment. It lowers the child via Lower (bubbling the
// fallback sentinel), namespaces the result under "absent_child", and
// delegates to renderAbsentFromNamespacedChild so SQL stays byte-identical
// to the Fragment path.
func lowerAbsentPlanDirect(ctx LoweringCtx, n *logicalpkg.AbsentPlan) (RenderedQuery, error) {
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: absent missing child")
	}
	childRQ, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "absent_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	tagsSQL := outputMetricTagsSQL(n.OutputMetric)
	rf, err := renderAbsentFromNamespacedChild(tagsSQL, childSQL, childParams, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// lowerAbsentOverTimeViaFragment bridges to the existing Fragment renderer
// by reading the pre-computed Fragment from ctx.NativeAnalysis. Retained
// until the windowed-source helper grows a logical-node counterpart.
func lowerAbsentOverTimeViaFragment(ctx LoweringCtx, n *logicalpkg.AbsentOverTimePlan) (RenderedQuery, error) {
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
