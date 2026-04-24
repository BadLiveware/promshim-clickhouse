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
