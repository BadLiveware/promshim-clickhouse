package renderer

import (
	"testing"

	"ch-observability/internal/promshim/native"
)

// TestLowerBinaryScalarInvolvingMatchesFragment is the Phase-B byte-equality
// differential guard that locks the extracted lowerBinaryScalarInvolving
// path against the Fragment path for scalar-involving BinaryPlan shapes.
// It covers arithmetic, comparison, folded-scalar, and scalar()-wrapped
// binary forms in both scalar-vector and vector-scalar orientations.
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the flipped Lower dispatch (which now
// routes scalar-involving binaries to lowerBinaryScalarInvolving) — in both
// instant and range modes, and asserts byte-identical SQL + QueryParams.
func TestLowerBinaryScalarInvolvingMatchesFragment(t *testing.T) {
	queries := []string{
		`foo + 1`,
		`foo - 10`,
		`foo * 2`,
		`foo / 100`,
		`foo % 3`,
		`foo ^ 2`,
		`foo == 5`,
		`foo != 5`,
		`foo < 5`,
		`foo <= 5`,
		`foo > 5`,
		`foo >= 5`,
		`1 + foo`,
		`100 - foo`,
		`2 * foo`,
		`foo * (1 + 1)`,
		`foo + pi()`,
		// Skipping `foo / scalar(sum(bar))`: BuildFragment currently rejects
		// this shape ("logical node *logical.BinaryPlan is not lowerable to a
		// native fragment"), so there is no Fragment baseline to compare
		// against. Byte-equality extraction tests cover only queries both
		// paths render today.
	}

	for _, query := range queries {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(query+"/"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)

				// Fragment path.
				fragment, err := native.BuildFragment(root, nativeAnalysis)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				wantRQ, err := RenderFragment(testRenderConfig(), fragment, mode.params)
				if err != nil {
					t.Fatalf("RenderFragment: %v", err)
				}

				// Logical path via the flipped Lower dispatch.
				gotRQ, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}

				if gotRQ.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch for %q (%s)\nwant:\n%s\n\ngot:\n%s", query, mode.name, wantRQ.SQL, gotRQ.SQL)
				}
				if !queryParamsEqualForLogicalHistogram(gotRQ.QueryParams, wantRQ.QueryParams) {
					t.Fatalf("QueryParams mismatch for %q (%s)\nwant: %v\ngot:  %v", query, mode.name, wantRQ.QueryParams, gotRQ.QueryParams)
				}
			})
		}
	}
}

// TestBinaryScalarInvolvingLogicalMatchesFragment is the Phase-5 byte-equality
// differential guard that locks the new direct-render lowerer
// (lowerBinaryScalarInvolvingDirect) to the Fragment path across the three
// scalar-involving BinaryPlan variants: scalar-scalar, scalar-vector, and
// vector-scalar. It expands the Phase-B coverage (arithmetic +,-,*,/,%,^
// and the six comparison ops ==,!=,<,<=,>,>=, the `bool` comparison
// modifier, scalar-expression folds via pi(), folded numeric literal
// operands, and both orientations — scalar on the left and scalar on the
// right) so the new direct dispatch stays honest for every shape that the
// Fragment path renders today. Queries whose Fragment-path rejection is
// itself a Phase-6 follow-up (notably `scalar(sum(bar))` bridging a
// non-literal scalar subquery, which BuildFragment currently rejects as
// "logical node *logical.BinaryPlan is not lowerable to a native fragment")
// are intentionally excluded — byte-equality extraction tests cover only
// queries both paths render today. See also the existing Phase-B test
// TestLowerBinaryScalarInvolvingMatchesFragment above for additional
// shapes that pre-dated this phase.
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the flipped Lower dispatch (which
// routes scalar-involving binaries to lowerBinaryScalarInvolving →
// lowerBinaryScalarInvolvingDirect) — in both instant and range modes, and
// asserts byte-identical SQL + QueryParams.
func TestBinaryScalarInvolvingLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		// ---- Scalar-scalar, folded literals (FragmentKindSyntheticSeries).
		// Covers every arithmetic op and bool-comparison op.
		`1 + 2`,
		`1 - 2`,
		`3 * 4`,
		`10 / 2`,
		`10 % 3`,
		`2 ^ 3`,
		`1 == bool 1`,
		`1 != bool 2`,
		`1 < bool 2`,
		`1 <= bool 2`,
		`2 > bool 1`,
		`2 >= bool 1`,

		// ---- Scalar-scalar, non-folded via pi() (FragmentKindValueTransform
		// or similar non-literal scalar arm). Exercises the
		// syntheticScalarChildTransform / scalar-expression side of the
		// analysis walk.
		`pi() + 1`,
		`1 + pi()`,
		`pi() * 2`,

		// ---- Scalar-vector, arithmetic +,-,*,/,%,^.
		// Vector side is a leaf selector, so these lower to
		// FragmentKindBinaryScalarSourceExpr.
		`5 + foo`,
		`10 - foo`,
		`2 * foo`,
		`100 / foo`,
		`3 % foo`,
		`2 ^ foo`,

		// ---- Scalar-vector, comparison ==,!=,<,<=,>,>= (filter form).
		// Lowers via applyComparisonFilterTransform → FragmentKindValueTransform.
		`5 == foo`,
		`5 != foo`,
		`0 < foo`,
		`0 <= foo`,
		`10 > foo`,
		`10 >= foo`,

		// ---- Scalar-vector, comparison with bool modifier (bool form).
		// Lowers via applyComparisonBoolTransform → FragmentKindValueTransform.
		`5 == bool foo`,
		`5 != bool foo`,
		`0 < bool foo`,
		`0 <= bool foo`,
		`10 > bool foo`,
		`10 >= bool foo`,

		// ---- Vector-scalar, arithmetic +,-,*,/,%,^.
		`foo + 5`,
		`foo - 10`,
		`foo * 2`,
		`foo / 100`,
		`foo % 3`,
		`foo ^ 2`,

		// ---- Vector-scalar, comparison ==,!=,<,<=,>,>= (filter form).
		`foo == 5`,
		`foo != 5`,
		`foo < 5`,
		`foo <= 5`,
		`foo > 0`,
		`foo >= 0`,

		// ---- Vector-scalar, comparison with bool modifier.
		`foo == bool 5`,
		`foo != bool 5`,
		`foo < bool 5`,
		`foo <= bool 5`,
		`foo > bool 0`,
		`foo >= bool 0`,
	}

	for _, query := range queries {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(query+"/"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)

				// Fragment path.
				fragment, err := native.BuildFragment(root, nativeAnalysis)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				wantRQ, err := RenderFragment(testRenderConfig(), fragment, mode.params)
				if err != nil {
					t.Fatalf("RenderFragment: %v", err)
				}

				// Logical path via the flipped Lower dispatch.
				gotRQ, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}

				if gotRQ.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch for %q (%s)\nwant:\n%s\n\ngot:\n%s", query, mode.name, wantRQ.SQL, gotRQ.SQL)
				}
				if !queryParamsEqualForLogicalHistogram(gotRQ.QueryParams, wantRQ.QueryParams) {
					t.Fatalf("QueryParams mismatch for %q (%s)\nwant: %v\ngot:  %v", query, mode.name, wantRQ.QueryParams, gotRQ.QueryParams)
				}
			})
		}
	}
}
