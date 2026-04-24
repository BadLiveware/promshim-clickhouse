package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

// lowerLabelTransform renders label_replace / label_join directly. The child
// vector is lowered via Lower(ctx, child); the mutated tags expression is
// derived from the logical node's Config fields via
// buildLabelTransformTagsExprFromLogical; and the outer SELECT wrapping is
// emitted by renderLabelTransformFromSource.
//
// Hierarchical fallback: if the child kind isn't yet direct-renderable,
// Lower returns errUnsupportedLowerNode and we propagate it so the caller
// falls back to the next execution tier.
//
// Both logical node kinds (LabelReplacePlan and LabelJoinPlan) flow through
// here and are disambiguated by buildLabelTransformTagsExprFromLogical /
// labelTransformChild.
func lowerLabelTransform(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerLabelTransform called with nil node")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: label_transform missing analysis")
	}
	child := labelTransformChild(n)
	if child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: label_transform missing child")
	}
	childRQ, err := Lower(ctx, child)
	if err != nil {
		return RenderedQuery{}, err // bubble errUnsupportedLowerNode
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "label_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	mutatedTagsExpr, err := buildLabelTransformTagsExprFromLogical(n, "label_child.tags")
	if err != nil {
		return RenderedQuery{}, err
	}
	rf, err := renderLabelTransformFromSource(childSQL, childParams, mutatedTagsExpr, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// labelTransformChild returns the child node of a LabelReplacePlan or
// LabelJoinPlan, or nil if n is neither.
func labelTransformChild(n logicalpkg.Node) logicalpkg.Node {
	switch p := n.(type) {
	case *logicalpkg.LabelReplacePlan:
		return p.Child
	case *logicalpkg.LabelJoinPlan:
		return p.Child
	default:
		return nil
	}
}
