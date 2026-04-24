package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerBinaryScalarInvolving lowers a BinaryPlan where at least one side
// is DomainScalar (scalar-vector, vector-scalar, or scalar-scalar).
//
// Phase 6f (Task 13a Phase 6f): the scoped native.BuildFragment call
// inside lowerBinaryScalarInvolvingDirect has been retired in favour of
// a cached-fragment read off ctx.NativeAnalysis. Since
// BuildFragment(n, analysis) is definitionally
// CloneFragment(analysis.InfoFor(n).Fragment) and Analyze's BinaryPlan
// walk (analysis.go:112+) already populates info.Fragment for every
// lowerable scalar-involving shape, the lowerer no longer needs to
// re-build the fragment at the boundary. The downstream Fragment kinds
// this shape routes through (BinaryScalarSourceExpr, ValueTransform,
// SyntheticSeries, plus the folded LeafSource shortcut) still render
// via renderFragment for now; they retire together with source.go and
// renderer.go's renderFragment in Task 13 Step 2/3.
//
// The public signature is unchanged so the lower.go dispatch keeps
// consuming it without any change.
func lowerBinaryScalarInvolving(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryScalarInvolving called with nil")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary missing logical analysis")
	}
	return lowerBinaryScalarInvolvingDirect(ctx, n)
}

// lowerBinaryScalarInvolvingDirect is the Phase-5 direct-render counterpart
// of the old Fragment-boundary body. It inspects the scalar/vector domain
// of the two sides via the logical Analysis side-map (LHS/RHS TimeDomain
// plus a concrete ScalarLiteralPlan check) and dispatches on the three
// variants — scalar-scalar, scalar-vector, and vector-scalar — so the
// shape discrimination is visible at the lowerer layer instead of being
// hidden inside BuildFragment's analysis walk.
//
// The scalar-involving BinaryPlan shape lowers via several downstream
// Fragment kinds depending on the child shape and op: FragmentKind-
// BinaryScalarSourceExpr when the vector side is a pushdownable selector
// (scalar-vector + vector-scalar arithmetic over a leaf/source),
// FragmentKindValueTransform when the op lowers via a value-transform
// wrapper (comparison filters/bool, scalar-expression arms, synthetic-
// scalar arms), FragmentKindSyntheticSeries for folded scalar-scalar, and
// occasionally FragmentKindLeafSource via the folded-literal shortcut.
// All of these are served by renderFragment's top-level switch.
//
// Phase 6f retires the scoped native.BuildFragment call that used to
// materialize the BinaryPlan Fragment here. Since BuildFragment(n,
// analysis) is definitionally CloneFragment(analysis.InfoFor(n).Fragment)
// — see native/builder.go — and Analyze's BinaryPlan walk
// (analysis.go:112+) already populates info.Fragment for every lowerable
// scalar-involving shape (BinaryScalarSourceExpr, ValueTransform,
// SyntheticSeries, folded LeafSource), the lowerer reads the cached
// fragment off ctx.NativeAnalysis and clones it directly. The downstream
// Fragment kinds themselves retire in Task 13 Step 2/3 alongside
// source.go and renderFragment.
//
// Hierarchical fallback: if the cached fragment is missing (e.g. `foo /
// scalar(sum(bar))` — scalar() children outside of ScalarLiteralPlan are
// not yet lowerable to a native fragment) or the Fragment path surfaces
// an error downstream, we return errUnsupportedLowerNode / the downstream
// error so the caller falls back to the Fragment rendering path
// wholesale.
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

	// Scalar-involving BinaryPlan lowers to one of four downstream shapes
	// (see analysis.go:124+):
	//
	//   - BinaryScalarSourceExpr: scalar-vector / vector-scalar arithmetic
	//     over a pushdownable selector child. Lowers via SourceExprView
	//     (source-expression semantics identical to UnarySourceExpr).
	//   - SyntheticSeries: folded scalar-scalar pair, or synthetic-scalar
	//     arm that doesn't wrap the vector side. Lowers via
	//     renderSyntheticDateFragment / renderScalarLiteralFragment.
	//   - ValueTransform: comparison filters/bool, scalar-expression arms,
	//     synthetic-scalar/vector arms that wrap the vector side in a
	//     value-transform outer SELECT. Currently still reads info.Fragment;
	//     retires in a follow-up commit once ValueTransformView is lifted.
	//   - LeafSource (folded): the rare folded-literal shortcut where the
	//     entire expression collapses to a plain selector leaf.
	//
	// The SourceExpr-bearing cases are served pure-logical via
	// renderSourceExprView; the remaining cases still clone info.Fragment
	// and dispatch through renderFragment.
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	info := ctx.NativeAnalysis.InfoFor(n)
	if info == nil || info.Fragment == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	// BinaryScalarSourceExpr / LeafSource (folded): render off SourceExprView
	// with the same anchored-range gate lowerPointwiseFunction uses, keeping
	// these cases off the Fragment path entirely.
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
	fragment := native.CloneFragment(info.Fragment)
	rf, err := renderFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}
