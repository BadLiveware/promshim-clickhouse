package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerBinaryVectorJoin handles the vector-vector case of a BinaryPlan by
// bridging to renderBinaryJoinFragment via the pre-computed NativeAnalysis
// Fragment (Approach A, Surface 13).
//
// Precondition: both sides have TimeDomain != DomainScalar.  The scalar-
// involving path is handled in lowerBinary before this is ever called.
//
// Covered ops: arithmetic (+, -, *, /, %, ^), comparison (==, !=, >, <,
// >=, <= with or without bool), set ops (and, or, unless), with any of the
// supported matching shapes: one-to-one (no modifier), on(...), ignoring(...),
// group_left(...), group_right(...).
//
// Hierarchical fallback: if NativeAnalysis is nil, or the analysis didn't mark
// this node lowerable (Fragment is nil or wrong kind), we return
// errUnsupportedLowerNode so the caller falls back to the Fragment rendering
// path for the whole query.
func lowerBinaryVectorJoin(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryVectorJoin called with nil")
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindBinaryVectorJoin {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderBinaryJoinFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
