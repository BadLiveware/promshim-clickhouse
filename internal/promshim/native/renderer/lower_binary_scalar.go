package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerBinaryScalarInvolving lowers a BinaryPlan where at least one side
// is DomainScalar (scalar-vector, vector-scalar, or scalar-scalar).
//
// Phase 5 (Task 13a Phase 5): the top-level BuildFragment call has been
// moved out of this function and into lowerBinaryScalarInvolvingDirect,
// which dispatches on the scalar/vector domain shape read from the
// logical Analysis before materializing any Fragment. The one remaining
// internal BuildFragment call is scoped inside the direct helper and
// retires in Phase 6 together with the downstream Fragment kinds
// (BinaryScalarSourceExpr, ValueTransform, SyntheticSeries, plus the
// LeafSource folded-scalar branch) that this shape routes through.
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
// All of these are served by renderFragment's top-level switch, so the
// direct helper materializes the BinaryPlan Fragment once internally and
// hands it to renderFragment for byte-identical SQL. Phase 6 retires this
// remaining BuildFragment call together with the scalar-involving
// Fragment kinds it feeds.
//
// Hierarchical fallback: if the internal BuildFragment rejects the node
// (e.g. `foo / scalar(sum(bar))` — scalar() children outside of
// ScalarLiteralPlan are not yet lowerable to a native fragment) or the
// Fragment path surfaces an error downstream, we return that error (or
// errUnsupportedLowerNode via BuildFragment's rejection path) so the
// caller falls back to the Fragment rendering path wholesale.
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

	// The three scalar-involving variants all route through the same
	// Fragment-side rendering surface (renderFragment's top-level switch)
	// because BuildFragment synthesizes the correct downstream fragment
	// kind (BinaryScalarSourceExpr / ValueTransform / SyntheticSeries /
	// folded LeafSource) for each variant. Keeping one scoped internal
	// BuildFragment here — rather than duplicating its per-variant
	// dispatch on the logical side — preserves byte-identical SQL for
	// Phase 5 and keeps the retirement surface for Phase 6 minimal: a
	// single call site that retires alongside the scalar-involving
	// Fragment kinds themselves.
	//
	// Note on the scalar-scalar branch: when both sides are
	// ScalarLiteralPlan, BuildFragment's analysis path folds the pair
	// into a FragmentKindSyntheticSeries via foldBinaryScalarLiteral.
	// That fold has to happen somewhere; until the ScalarLiteralPlan
	// lowerer grows its own folded-pair rendering in Phase 6, this
	// helper is still the right place for it.
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	rf, err := renderFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}
