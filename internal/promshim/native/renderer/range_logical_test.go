package renderer

import (
	"testing"

	"ch-observability/internal/promshim/native"
)

// TestRangeFunctionLogicalMatchesFragment is the Phase-A3 byte-equality
// differential guard that locks the new logical-path rendering to the
// Fragment path. It exercises every range-function plan shape that produces
// FragmentKindRangeFunction — all seven plan kinds (RatePlan, IncreasePlan,
// DeltaPlan, ChangesPlan, DerivPlan, RangeFunctionPlan (all *_over_time
// variants, predict_linear, holt_winters), and QuantileOverTimePlan) — plus
// the subquery-child case that flows through the FragmentKindSubquery branch
// of renderRangeFunctionFragment.
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the flipped Lower dispatch (which now
// calls renderRangeFunctionLogical instead of BuildFragment at the boundary)
// — in both instant and range modes, and asserts byte-identical SQL +
// QueryParams.
func TestRangeFunctionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`rate(foo[5m])`,
		`increase(foo[1h])`,
		`delta(foo[10m])`,
		`changes(foo[5m])`,
		`deriv(foo[5m])`,
		`avg_over_time(foo[5m])`,
		`sum_over_time(foo[5m])`,
		`min_over_time(foo[5m])`,
		`max_over_time(foo[5m])`,
		`count_over_time(foo[5m])`,
		`stddev_over_time(foo[5m])`,
		`stdvar_over_time(foo[5m])`,
		`last_over_time(foo[5m])`,
		`present_over_time(foo[5m])`,
		`quantile_over_time(0.9, foo[5m])`,
		`predict_linear(foo[5m], 60)`,
		`holt_winters(foo[5m], 0.8, 0.9)`,
		`rate(sum by (x) (foo)[5m:30s])`,
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
