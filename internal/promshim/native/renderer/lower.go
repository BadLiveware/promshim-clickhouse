package renderer

import (
	"errors"
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
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
	case *logicalpkg.PointwiseFunctionPlan:
		return lowerPointwiseFunction(ctx, n)
	case *logicalpkg.ScalarBuiltinPlan:
		return lowerScalarBuiltin(ctx, n)
	case *logicalpkg.ScalarConvertPlan:
		return lowerScalarConvert(ctx, n)
	case *logicalpkg.SubqueryPlan:
		return lowerSubquery(ctx, n)
	case *logicalpkg.SortPlan:
		return lowerSortTransform(ctx, n)
	case *logicalpkg.LabelReplacePlan:
		return lowerLabelTransform(ctx, n)
	case *logicalpkg.LabelJoinPlan:
		return lowerLabelTransform(ctx, n)
	case *logicalpkg.AggregationPlan:
		return lowerAggregation(ctx, n)
	case *logicalpkg.RangeFunctionPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.RatePlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.IncreasePlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.DeltaPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.ChangesPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.DerivPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.QuantileOverTimePlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.HistogramProjectionPlan:
		return lowerHistogramProjection(ctx, n)
	case *logicalpkg.HistogramQuantilePlan:
		return lowerHistogramFunction(ctx, n)
	case *logicalpkg.HistogramFractionPlan:
		return lowerHistogramFunction(ctx, n)
	case *logicalpkg.HistogramQuantilesPlan:
		return lowerHistogramFunction(ctx, n)
	case *logicalpkg.AbsentPlan:
		return lowerAbsent(ctx, n)
	case *logicalpkg.AbsentOverTimePlan:
		return lowerAbsent(ctx, n)
	case *logicalpkg.InfoPlan:
		return lowerInfoJoin(ctx, n)
	case *logicalpkg.UnaryPlan:
		return lowerUnary(ctx, n)
	case *logicalpkg.RoundPlan:
		return lowerRound(ctx, n)
	case *logicalpkg.VectorPlan:
		return lowerVector(ctx, n)
	default:
		return RenderedQuery{}, errUnsupportedLowerNode
	}
}

// IsUnsupportedByLower reports whether err is the Lower fallback
// sentinel — i.e. the caller should dispatch through the Fragment
// rendering path instead.
func IsUnsupportedByLower(err error) bool { return errors.Is(err, errUnsupportedLowerNode) }

// lowerLeaf handles a top-level LeafExprPlan by rendering SQL directly
// from the logical plan, without constructing an intermediate NativeFragment.
// renderLeafLogical replicates the non-folded LeafSource branch of
// renderSourceFragment, reusing buildSelectorSource and the
// storage.Build*QuerySQL helpers to lock byte-identity with the Fragment path.
func lowerLeaf(ctx LoweringCtx, n *logicalpkg.LeafExprPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerLeaf called with nil")
	}
	// Prefer the cached leaf selector from NativeAnalysis when available.
	// Upstream passes (narrowHistogramChildAnalysisInPlace,
	// applySelectorProjection) mutate that cached selector in place to
	// record tag-narrowing decisions; re-using the cached pointer here
	// lets the narrowed shape flow into the rendered SQL, which matches
	// the Fragment-side behavior where RenderFragment on the cached leaf
	// Fragment renders off the same narrowed SelectorSource.
	var cachedSelector *native.SelectorSource
	if ctx.NativeAnalysis != nil {
		if info := ctx.NativeAnalysis.InfoFor(n); info != nil && info.Fragment != nil {
			cachedSelector = info.Fragment.Selector
		}
	}
	rf, err := renderLeafLogical(ctx.Config, n, ctx.Params, cachedSelector)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// lowerBinary handles BinaryPlan in two branches:
//   - Scalar-involving (at least one side is DomainScalar): delegates to the
//     existing BuildFragment + RenderFragment pipeline, unchanged from Phase 1.
//   - Vector-vector (both sides are non-scalar): delegates to
//     lowerBinaryVectorJoin, which direct-renders each side via Lower and
//     assembles the join via storage.Build{Instant,Range}BinaryVectorJoinSQL.
func lowerBinary(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinary called with nil")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary missing logical analysis")
	}
	lhsInfo := ctx.Analysis.InfoFor(n.LHS)
	rhsInfo := ctx.Analysis.InfoFor(n.RHS)
	if lhsInfo == nil || rhsInfo == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary node missing analysis")
	}
	if lhsInfo.TimeDomain == logicalpkg.DomainScalar || rhsInfo.TimeDomain == logicalpkg.DomainScalar {
		return lowerBinaryScalarInvolving(ctx, n)
	}
	// Vector-vector path: Surface 13 (Approach A).
	return lowerBinaryVectorJoin(ctx, n)
}
