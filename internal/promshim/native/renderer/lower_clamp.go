package renderer

import (
	"fmt"
	"math"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// lowerClamp lowers a PointwiseFunctionPlan whose Func is one of clamp,
// clamp_min, or clamp_max directly from the logical tree. The main child is
// lowered via Lower and namespaced under "clamp_child"; each scalar bound
// (min/max) is bound via renderClampBoundBindingLogical (literals short-circuit
// to inline floats; other shapes lower via Lower). The final SQL is assembled
// by the shared assembleClampSQL helper — the Fragment path calls the same
// helper with clampBoundBindings produced by renderClampBoundBindingFragment,
// so SQL stays byte-identical.
//
// Hierarchical fallback: any nested Lower call may return
// errUnsupportedLowerNode (child shape not yet directly renderable), which
// bubbles up so the whole query falls back to the Fragment path.
func lowerClamp(ctx LoweringCtx, n *logicalpkg.PointwiseFunctionPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerClamp called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: clamp missing logical analysis")
	}
	if !isClampFuncName(n.Func) {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerClamp called with non-clamp func %q", n.Func)
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: clamp missing child")
	}
	var minChild, maxChild logicalpkg.Node
	switch n.Func {
	case "clamp":
		if len(n.ParamChildren) != 2 || n.ParamChildren[0] == nil || n.ParamChildren[1] == nil {
			return RenderedQuery{}, errUnsupportedLowerNode
		}
		minChild, maxChild = n.ParamChildren[0], n.ParamChildren[1]
	case "clamp_min":
		if len(n.ParamChildren) != 1 || n.ParamChildren[0] == nil {
			return RenderedQuery{}, errUnsupportedLowerNode
		}
		minChild = n.ParamChildren[0]
	case "clamp_max":
		if len(n.ParamChildren) != 1 || n.ParamChildren[0] == nil {
			return RenderedQuery{}, errUnsupportedLowerNode
		}
		maxChild = n.ParamChildren[0]
	}

	childRQ, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "clamp_child")
	if err != nil {
		return RenderedQuery{}, err
	}

	queryParams := map[string]string{}
	mergeRenderedQueryParams(queryParams, childParams)
	minBinding, err := renderClampBoundBindingLogical(ctx, minChild, "clamp_min", math.Inf(-1))
	if err != nil {
		return RenderedQuery{}, err
	}
	mergeClampBoundBindingParams(queryParams, minBinding)
	maxBinding, err := renderClampBoundBindingLogical(ctx, maxChild, "clamp_max", math.Inf(1))
	if err != nil {
		return RenderedQuery{}, err
	}
	mergeClampBoundBindingParams(queryParams, maxBinding)

	rf, err := assembleClampSQL(n.Func, childSQL, queryParams, minBinding, maxBinding, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
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
