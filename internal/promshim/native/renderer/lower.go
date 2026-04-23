package renderer

import (
	"errors"
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

// errUnsupportedLowerNode is returned by Lower when the node kind is
// not yet handled by the new lowering dispatcher. Callers inspect this
// with IsUnsupportedByLower and fall back to the existing Fragment
// rendering path for the whole query — Lower and Fragment are never
// mixed within a single query.
var errUnsupportedLowerNode = errors.New("renderer: node kind not yet supported by Lower — fall back to Fragment path")

// LoweringCtx bundles the per-query inputs Lower needs. It is
// immutable for the duration of a Lower call; per-kind lowerers treat
// it as read-only.
type LoweringCtx struct {
	Config         storage.QueryConfig
	Analysis       *logicalpkg.Analysis
	NativeAnalysis *native.Analysis
	Params         RenderParams
}

// Lower translates a logical.Node into a RenderedQuery via a
// type-switch dispatch. Unsupported kinds return
// errUnsupportedLowerNode so callers can fall back hierarchically to
// Fragment dispatch for the whole query.
//
// Phase 1 scope: LeafExprPlan, ScalarLiteralPlan, and scalar-trivial
// BinaryPlan. Everything else (including vector-vector BinaryPlan)
// returns the sentinel.
func Lower(ctx LoweringCtx, node logicalpkg.Node) (RenderedQuery, error) {
	if node == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: Lower called with nil node")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: Lower requires an Analysis")
	}
	switch n := node.(type) {
	case *logicalpkg.LeafExprPlan:
		return lowerLeaf(ctx, n)
	case *logicalpkg.ScalarLiteralPlan:
		return lowerScalarLiteral(ctx, n)
	case *logicalpkg.BinaryPlan:
		return lowerBinary(ctx, n)
	default:
		return RenderedQuery{}, errUnsupportedLowerNode
	}
}

// IsUnsupportedByLower reports whether err is the Lower fallback
// sentinel — i.e. the caller should dispatch through the Fragment
// rendering path instead.
func IsUnsupportedByLower(err error) bool { return errors.Is(err, errUnsupportedLowerNode) }

// lowerLeaf handles a LeafExprPlan by reusing the existing
// Fragment-rendering pipeline. Task 6 will replace the body with
// direct emit/ calls; Phase 1 is scaffolding.
func lowerLeaf(ctx LoweringCtx, n *logicalpkg.LeafExprPlan) (RenderedQuery, error) {
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}

// lowerScalarLiteral handles a ScalarLiteralPlan by delegating to the
// existing Fragment render. See lowerLeaf for the Phase 1 rationale.
func lowerScalarLiteral(ctx LoweringCtx, n *logicalpkg.ScalarLiteralPlan) (RenderedQuery, error) {
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}

// lowerBinary handles the scalar-involving forms of BinaryPlan. Any
// other shape (vector-vector) returns the fallback sentinel so the
// caller can re-render through the Fragment path.
func lowerBinary(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	lhsInfo := ctx.Analysis.InfoFor(n.LHS)
	rhsInfo := ctx.Analysis.InfoFor(n.RHS)
	if lhsInfo == nil || rhsInfo == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary node missing analysis")
	}
	// Phase 1 scope: at least one side must be a scalar. Vector-vector
	// binaries fall back to Fragment rendering.
	if lhsInfo.TimeDomain != logicalpkg.DomainScalar && rhsInfo.TimeDomain != logicalpkg.DomainScalar {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}
