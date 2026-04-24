package renderer

import (
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// TestRangeFunctionLogicalMatchesFragment is the Phase-A3 byte-equality
// differential guard that locks the new logical-path rendering to the
// Fragment path. It exercises every range-function plan shape that produces
// FragmentKindRangeFunction — all seven plan kinds (RatePlan, IncreasePlan,
// DeltaPlan, ChangesPlan, DerivPlan, RangeFunctionPlan (all *_over_time
// variants, predict_linear, holt_winters), and QuantileOverTimePlan) — plus
// subquery-child shapes for each kind that flow through the
// FragmentKindSubquery branch of renderRangeFunctionFragment.
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the flipped Lower dispatch (which now
// calls renderRangeFunctionLogical instead of BuildFragment at the boundary)
// — in both instant and range modes, and asserts byte-identical SQL +
// QueryParams.
//
// Phase 3 (Task 13a Phase 3) extension: the query list explicitly covers
// both the leaf-selector child fast path (LeafExprPlan wrapping a
// MatrixSelector) and the subquery child non-fast path (SubqueryPlan) for
// each of the seven range-function plan kinds, so a regression in either
// structural branch of the direct helper (rangeFunctionChildNode lookup
// or the internal transitional BuildFragment) is caught byte-equal.
func TestRangeFunctionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		// ---- Leaf-selector child (fast path): LeafExprPlan{MatrixSelector}
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

		// ---- Subquery child (non-fast path): SubqueryPlan inside the
		// range-function plan. Each kind exercises its own
		// rangeFunctionChildNode branch so the subquery hand-off stays
		// byte-equal with the Fragment path's FragmentKindSubquery branch.
		`rate(sum by (x) (foo)[5m:30s])`,
		`increase(sum by (x) (foo)[5m:30s])`,
		`delta(sum by (x) (foo)[5m:30s])`,
		`changes(sum by (x) (foo)[5m:30s])`,
		`deriv(sum by (x) (foo)[5m:30s])`,
		`avg_over_time(sum by (x) (foo)[5m:30s])`,
		`sum_over_time(sum by (x) (foo)[5m:30s])`,
		`quantile_over_time(0.9, sum by (x) (foo)[5m:30s])`,
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

// TestFusedRangeAggregationLogicalMatchesFragment is the Phase-3
// byte-equality differential guard for the fused range+aggregation
// helpers. It exercises the direct-render path
// (canFuseRangeAggregationLogicalDirect + tryRenderFusedRangeAggregationLogicalDirect)
// against the Fragment-side equivalents
// (canFuseRangeAggregationFragment + tryRenderFusedRangeAggregationFragment)
// and asserts:
//
//   - both capability checks agree on which queries fuse (fusable queries
//     return true from both predicates, non-fusable queries return false),
//   - for fusable queries, the direct tryRender path emits byte-identical
//     SQL + QueryParams to the Fragment path.
//
// The fusion shape is strictly `sum [by (...)] (range_function(leaf[dur]))`
// or `sum [by (...)] (range_function(subquery))` in range mode. Negative
// cases (instant mode, non-SUM op, missing range child, etc.) are covered
// by the notFusable set to guard against accidentally widening the
// capability surface during the port.
func TestFusedRangeAggregationLogicalMatchesFragment(t *testing.T) {
	// Fusable shapes — both helpers must agree with the Fragment path and
	// must produce byte-identical SQL.
	fusable := []string{
		`sum by (x) (rate(foo[5m]))`,
		`sum by (x, job) (rate(foo[5m]))`,
		`sum (rate(foo[5m]))`,
		`sum by (x) (increase(foo[5m]))`,
		`sum by (x) (avg_over_time(foo[5m]))`,
		// Param-carrying range functions (exercises rangeFunctionParamNumbers
		// extracting ParamNumber from RangeFunctionPlan and from
		// QuantileOverTimePlan in the logical port).
		`sum by (x) (quantile_over_time(0.9, foo[5m]))`,
		`sum by (x) (predict_linear(foo[5m], 60))`,
		`sum by (x) (holt_winters(foo[5m], 0.8, 0.9))`,
		// Subquery-child range-function fused under sum.
		`sum by (x) (rate(sum by (x) (foo)[5m:30s]))`,
		`sum (rate(sum by (x) (foo)[5m:30s]))`,
		`sum by (x) (increase(sum by (x) (foo)[5m:30s]))`,
		`sum by (x) (avg_over_time(sum by (x) (foo)[5m:30s]))`,
		`sum by (x) (quantile_over_time(0.9, sum by (x) (foo)[5m:30s]))`,
	}
	for _, query := range fusable {
		t.Run("fusable/"+query, func(t *testing.T) {
			root, logicalAnalysis, nativeAnalysis := buildLowerInputs(t, query)
			agg, ok := root.(*logicalpkg.AggregationPlan)
			if !ok {
				t.Fatalf("expected AggregationPlan, got %T", root)
			}

			params := testRenderParamsRange()

			// Capability agreement: logical-direct and fragment-side must
			// both return true for fusable shapes.
			if !canFuseRangeAggregationLogicalDirect(agg, params) {
				t.Fatalf("canFuseRangeAggregationLogicalDirect=false for fusable query %q", query)
			}
			fragment, err := native.BuildFragment(agg, nativeAnalysis)
			if err != nil {
				t.Fatalf("BuildFragment: %v", err)
			}
			if !canFuseRangeAggregationFragment(fragment, params) {
				t.Fatalf("canFuseRangeAggregationFragment=false for fusable query %q", query)
			}

			// Byte-equal SQL between direct and fragment rendering of the
			// fused shape.
			gotRF, gotOK, gotErr := tryRenderFusedRangeAggregationLogicalDirect(testRenderConfig(), agg, logicalAnalysis, nativeAnalysis, params)
			if gotErr != nil {
				t.Fatalf("tryRenderFusedRangeAggregationLogicalDirect: %v", gotErr)
			}
			if !gotOK {
				t.Fatalf("tryRenderFusedRangeAggregationLogicalDirect returned ok=false for fusable query %q", query)
			}
			wantRF, wantOK, wantErr := tryRenderFusedRangeAggregationFragment(testRenderConfig(), fragment, params)
			if wantErr != nil {
				t.Fatalf("tryRenderFusedRangeAggregationFragment: %v", wantErr)
			}
			if !wantOK {
				t.Fatalf("tryRenderFusedRangeAggregationFragment returned ok=false for fusable query %q", query)
			}

			gotRQ, err := finalizeRenderedFragment(gotRF)
			if err != nil {
				t.Fatalf("finalizeRenderedFragment (got): %v", err)
			}
			wantRQ, err := finalizeRenderedFragment(wantRF)
			if err != nil {
				t.Fatalf("finalizeRenderedFragment (want): %v", err)
			}
			if gotRQ.SQL != wantRQ.SQL {
				t.Fatalf("fused SQL mismatch for %q\nwant:\n%s\n\ngot:\n%s", query, wantRQ.SQL, gotRQ.SQL)
			}
			if !queryParamsEqualForLogicalHistogram(gotRQ.QueryParams, wantRQ.QueryParams) {
				t.Fatalf("fused QueryParams mismatch for %q\nwant: %v\ngot:  %v", query, wantRQ.QueryParams, gotRQ.QueryParams)
			}
		})
	}

	// Non-fusable shapes — both helpers must return false and the direct
	// tryRender path must return ok=false without error.
	notFusable := []struct {
		name  string
		query string
	}{
		{name: "avg-by-rate-not-sum", query: `avg by (x) (rate(foo[5m]))`},
		{name: "min-by-rate-not-sum", query: `min by (x) (rate(foo[5m]))`},
		{name: "sum-without-range-child", query: `sum by (x) (foo)`},
		{name: "sum-of-plain-selector", query: `sum by (x) (up)`},
	}
	for _, tc := range notFusable {
		t.Run("notFusable/"+tc.name, func(t *testing.T) {
			root, logicalAnalysis, nativeAnalysis := buildLowerInputs(t, tc.query)
			agg, ok := root.(*logicalpkg.AggregationPlan)
			if !ok {
				// If the root is not an aggregation at all, the helpers
				// must still politely decline.
				if canFuseRangeAggregationLogicalDirect(nil, testRenderParamsRange()) {
					t.Fatalf("canFuseRangeAggregationLogicalDirect(nil) returned true")
				}
				return
			}

			params := testRenderParamsRange()
			if canFuseRangeAggregationLogicalDirect(agg, params) {
				t.Fatalf("canFuseRangeAggregationLogicalDirect=true for non-fusable %q", tc.query)
			}
			fragment, err := native.BuildFragment(agg, nativeAnalysis)
			if err != nil {
				t.Fatalf("BuildFragment: %v", err)
			}
			if canFuseRangeAggregationFragment(fragment, params) {
				t.Fatalf("canFuseRangeAggregationFragment=true for non-fusable %q", tc.query)
			}
			if _, got, err := tryRenderFusedRangeAggregationLogicalDirect(testRenderConfig(), agg, logicalAnalysis, nativeAnalysis, params); err != nil || got {
				t.Fatalf("tryRenderFusedRangeAggregationLogicalDirect: got=(ok=%v err=%v) for non-fusable %q, want (ok=false err=nil)", got, err, tc.query)
			}
		})
	}

	// Instant mode must never fuse even for fusable range shapes. Guards
	// against the params.Mode check being accidentally dropped.
	t.Run("notFusable/instant-mode", func(t *testing.T) {
		root, _, _ := buildLowerInputs(t, `sum by (x) (rate(foo[5m]))`)
		agg, ok := root.(*logicalpkg.AggregationPlan)
		if !ok {
			t.Fatalf("expected AggregationPlan, got %T", root)
		}
		if canFuseRangeAggregationLogicalDirect(agg, testRenderParamsInstant()) {
			t.Fatalf("canFuseRangeAggregationLogicalDirect=true in instant mode for sum by (x)(rate(foo[5m]))")
		}
	})
}
