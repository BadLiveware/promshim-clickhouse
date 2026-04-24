package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
)

// lowerBinaryScalarInvolving lowers a BinaryPlan where at least one
// side is DomainScalar (scalar-vector, vector-scalar, or scalar-scalar).
// All four downstream shapes dispatch off LoweringInfo side-maps:
//
//   - BinaryScalarSourceExpr / folded LeafSource: served via
//     info.SourceExpr through renderSourceExprView (anchored-range
//     aware).
//   - SyntheticSeries (folded scalar-scalar literal): served via
//     info.SyntheticSeries through renderScalarLiteralFragment.
//   - ValueTransform (comparison filters/bool, scalar-expression arms,
//     synthetic-scalar/vector arms wrapping the vector side): served
//     via info.ValueTransform — recurse Lower through the vector-side
//     child and wrap with renderValueTransformFromSource.
func lowerBinaryScalarInvolving(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryScalarInvolving called with nil")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary missing logical analysis")
	}
	return lowerBinaryScalarInvolvingDirect(ctx, n)
}

// lowerBinaryScalarInvolvingDirect inspects the scalar/vector domain
// of the two sides via the logical Analysis side-map (LHS/RHS
// TimeDomain) and dispatches directly off the view fields on
// LoweringInfo populated by Analyze's BinaryPlan walk. If none of the
// view fields are populated (e.g. `foo / scalar(sum(bar))` — scalar()
// children outside of ScalarLiteralPlan are not lowerable here), we
// return errUnsupportedLowerNode so Lower falls back to the next
// execution tier.
func lowerBinaryScalarInvolvingDirect(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	lhsInfo := ctx.Analysis.InfoFor(n.LHS)
	rhsInfo := ctx.Analysis.InfoFor(n.RHS)
	if lhsInfo == nil || rhsInfo == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary node missing analysis")
	}
	lhsScalar := lhsInfo.TimeDomain == logicalpkg.DomainScalar
	rhsScalar := rhsInfo.TimeDomain == logicalpkg.DomainScalar
	if !lhsScalar && !rhsScalar {
		// Caller should have routed vector-vector shapes to
		// lowerBinaryVectorJoin; guard defensively so the tier boundary
		// stays honest.
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryScalarInvolvingDirect: neither side is scalar")
	}

	// Scalar-involving BinaryPlan lowers to one of four downstream
	// shapes:
	//
	//   - BinaryScalarSourceExpr: scalar-vector / vector-scalar
	//     arithmetic over a pushdownable selector child. Served via
	//     info.SourceExpr through renderSourceExprView.
	//   - SyntheticSeries: folded scalar-scalar pair. Served via
	//     info.SyntheticSeries through renderScalarLiteralFragment.
	//   - ValueTransform: comparison filters/bool, scalar-expression
	//     arms, synthetic-scalar/vector arms that wrap the vector side
	//     in a value-transform outer SELECT. Served via
	//     info.ValueTransform — Lower recurses through the vector-side
	//     child and renderValueTransformFromSource applies the wrap.
	//   - LeafSource (folded): the rare folded-literal shortcut where
	//     the entire expression collapses to a plain selector leaf.
	//     Served via info.SourceExpr.
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	info := ctx.NativeAnalysis.InfoFor(n)
	if info == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	// BinaryScalarSourceExpr / LeafSource (folded): render off SourceExprView
	// with the same anchored-range gate lowerPointwiseFunction uses.
	if info.SourceExpr != nil {
		var rf renderedFragment
		var err error
		if ctx.Params.Mode == native.RenderModeRange && info.Shape.HasFixedTemporalAnchor {
			anchorMS, ok := logicalResolvedAnchorTimeMS(n, native.OptimizationContext{
				Mode:             native.RenderModeRange,
				EvaluationTimeMS: ctx.Params.EvaluationTimeMS,
				StartMS:          ctx.Params.StartMS,
				EndMS:            ctx.Params.EndMS,
				StepMS:           ctx.Params.StepMS,
			})
			if !ok {
				return RenderedQuery{}, fmt.Errorf("anchored native range rendering requires a resolved anchor")
			}
			rf, err = renderAnchoredRangeSourceExprView(ctx.Config, info.SourceExpr, info.OutputKind, ctx.Params, anchorMS)
		} else {
			rf, err = renderSourceExprView(ctx.Config, info.SourceExpr, ctx.Params)
		}
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(rf)
	}
	// SyntheticSeries (folded scalar-scalar): both BinaryPlan sides folded to
	// numeric literals (e.g. `1 + 2`, `pi() + pi()` via syntheticLiteralValue).
	// The analysis-time shape is captured as a SyntheticSeriesView and
	// rendered directly via renderScalarLiteralFragment.
	if info.SyntheticSeries != nil {
		rf, err := renderScalarLiteralFragment(info.SyntheticSeries.Value, ctx.Params)
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(rf)
	}
	// ValueTransform: comparison filters/bool, scalar-expression arms,
	// synthetic-scalar (pi()/time()) arms that wrap the vector side in a
	// value-transform outer SELECT. The view on LoweringInfo carries
	// ValueExpr / FilterExpr / DropsMetric plus a VectorChildOnLeft flag
	// that identifies which BinaryPlan side carries the non-scalar child.
	// Recurse Lower through that side to produce the child SQL, then
	// wrap via the shared renderValueTransformFromSource helper.
	if info.ValueTransform != nil {
		childNode := n.RHS
		if info.ValueTransform.VectorChildOnLeft {
			childNode = n.LHS
		}
		childRQ, err := Lower(ctx, childNode)
		if err != nil {
			return RenderedQuery{}, err
		}
		rf, err := renderValueTransformFromSource(
			trimRenderedQuerySQL(childRQ.SQL),
			childRQ.QueryParams,
			info.ValueTransform.ValueExpr,
			info.ValueTransform.FilterExpr,
			info.ValueTransform.DropsMetric,
			ctx.Params,
		)
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(rf)
	}
	return RenderedQuery{}, errUnsupportedLowerNode
}
