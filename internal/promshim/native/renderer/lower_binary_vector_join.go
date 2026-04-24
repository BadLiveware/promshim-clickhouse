package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

// lowerBinaryVectorJoin renders a vector-vector BinaryPlan directly from the
// logical tree. LHS/RHS are lowered via Lower (bubbling errUnsupportedLowerNode
// if either side's kind isn't yet directly renderable), namespaced under "lhs"
// / "rhs", and handed to storage.Build{Instant,Range}BinaryVectorJoinSQL.
//
// Precondition: both sides have TimeDomain != DomainScalar; the scalar-
// involving path is handled in lowerBinary before this is ever called.
//
// Covered ops: arithmetic (+, -, *, /, %, ^), comparison (==, !=, >, <,
// >=, <= with or without bool), set ops (and, or, unless), with any of the
// supported matching shapes: one-to-one (no modifier), on(...), ignoring(...),
// group_left(...), group_right(...).
//
// Transitional NativeAnalysis tap: in range mode we still read the per-side
// fragment from NativeAnalysis to compute per-child required input bounds via
// native.RequiredInputBounds — the same helper the Fragment path uses. Once
// bounds computation moves to a logical-side helper this tap can be dropped.
//
// Hierarchical fallback: if NativeAnalysis is nil, either child is missing
// analysis, or the operator/matching shape isn't natively supported, we
// return errUnsupportedLowerNode so the caller falls back to the Fragment
// rendering path for the whole query.
func lowerBinaryVectorJoin(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryVectorJoin called with nil")
	}
	if n.LHS == nil || n.RHS == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary vector join missing child node")
	}
	if !native.IsSupportedNativeVectorJoinOp(n.Op, n.VectorMatching) {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	joinShape, ok := native.SupportedNativeVectorJoinShape(n.VectorMatching)
	if !ok {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	lhsNativeInfo := ctx.NativeAnalysis.InfoFor(n.LHS)
	rhsNativeInfo := ctx.NativeAnalysis.InfoFor(n.RHS)
	if lhsNativeInfo == nil || rhsNativeInfo == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	joinCfg := storage.BinaryJoinConfig{
		Op:             n.Op,
		ReturnBool:     n.ReturnBool,
		VectorMatching: native.CloneVectorMatching(n.VectorMatching),
		JoinShape:      joinShape,
	}
	switch ctx.Params.Mode {
	case native.RenderModeInstant:
		lhsSQL, lhsParams, err := lowerBinaryVectorJoinSide(ctx, n.LHS, "lhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		rhsSQL, rhsParams, err := lowerBinaryVectorJoinSide(ctx, n.RHS, "rhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams, err := storage.BuildInstantBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams})
	case native.RenderModeRange:
		lhsBoundsCtx := ctx
		lhsBoundsCtx.Params = rangeSideParams(ctx.Params, lhsNativeInfo.Fragment)
		rhsBoundsCtx := ctx
		rhsBoundsCtx.Params = rangeSideParams(ctx.Params, rhsNativeInfo.Fragment)
		lhsSQL, lhsParams, err := lowerBinaryVectorJoinSide(lhsBoundsCtx, n.LHS, "lhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		rhsSQL, rhsParams, err := lowerBinaryVectorJoinSide(rhsBoundsCtx, n.RHS, "rhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams, err := storage.BuildRangeBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams})
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
	}
}

// lowerBinaryVectorJoinSide lowers one side of a vector-vector binary join
// and namespaces the result under the given alias ("lhs" or "rhs") so the
// rendered SQL is embeddable as a FROM source inside the join body. Matches
// what renderFragmentSubquery does on the Fragment path.
func lowerBinaryVectorJoinSide(ctx LoweringCtx, child logicalpkg.Node, prefix string) (string, map[string]string, error) {
	rendered, err := Lower(ctx, child)
	if err != nil {
		return "", nil, err
	}
	return namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, prefix)
}

// rangeSideParams computes the per-side RenderParams for range-mode binary
// vector join lowering, deriving RequiredStartMS/EndMS from the child's
// fragment via native.RequiredInputBounds (same helper the Fragment path
// uses). Transitional tap: once bounds computation has a logical-side
// counterpart this can swap over without changing the rendered SQL.
func rangeSideParams(outer RenderParams, childFragment *native.NativeFragment) RenderParams {
	requiredStartMS, requiredEndMS, _ := native.RequiredInputBounds(childFragment, nil, native.OptimizationContext{Mode: native.RenderModeRange, StartMS: outer.StartMS, EndMS: outer.EndMS, StepMS: outer.StepMS})
	return RenderParams{
		Mode:                native.RenderModeRange,
		StartMS:             outer.StartMS,
		EndMS:               outer.EndMS,
		StepMS:              outer.StepMS,
		RequiredStartMS:     requiredStartMS,
		RequiredEndMS:       requiredEndMS,
		ResolveSourcePromQL: outer.ResolveSourcePromQL,
	}
}
