package renderer

import (
	"testing"

	"ch-observability/internal/promshim/native"
)

// TestAggregationLogicalMatchesFragment is the byte-equality differential
// guard that locks the new logical-path aggregation rendering to the
// Fragment path for every AggregationPlan shape. It covers all twelve
// aggregation ops (sum, avg, count, min, max, stddev, stdvar, group, topk,
// bottomk, quantile, count_values) plus the grouping variants (no grouping,
// BY grouping, single- and multi-label grouping, WITHOUT grouping), the
// range-function child shapes — both the fused SUM branch (sum [by (...)]
// over rate/increase/avg_over_time, including the subquery-range child) and
// the non-fused branches (avg/count over rate/increase/deriv which are not
// SUM) — and scalar-involving binary children (sum(foo * 2), sum by (x)
// (foo / 100)).
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the flipped Lower dispatch (which now
// calls renderAggregationLogical / renderAggregationLogicalDirect instead of
// BuildFragment at the boundary) — in both instant and range modes, and
// asserts byte-identical SQL + QueryParams.
//
// Phase 4 (Task 13a Phase 4) extension: the query list explicitly covers
// each of the 12 aggregation ops as the root op AND exercises both
// branches of renderAggregationLogicalDirect. In range mode, fusable
// SUM-over-range shapes (sum [by (...)] (rate(...))) enter the
// canFuseRangeAggregationLogicalDirect branch and route through
// tryRenderFusedRangeAggregationLogicalDirect without BuildFragment'ing
// at the lowerer boundary. Non-fusable shapes (non-SUM ops, plain
// selectors, scalar-binary children, instant mode) fall into the
// transitional non-fused branch that still materializes a single
// aggregation Fragment internally. Both branches must stay byte-equal
// with the Fragment path.
func TestAggregationLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		// ---- 12 aggregation ops as root op, leaf selector child.
		`sum(foo)`,
		`avg(foo)`,
		`count(foo)`,
		`min(foo)`,
		`max(foo)`,
		`stddev(foo)`,
		`stdvar(foo)`,
		`group(foo)`,
		`topk(5, foo)`,
		`bottomk(3, foo)`,
		`quantile(0.9, foo)`,
		`count_values("value", foo)`,

		// ---- Grouping variants (BY single/multi-label, WITHOUT).
		`sum by (x) (foo)`,
		`sum without (x) (foo)`,
		`avg by (x, y) (foo)`,
		`max without (y) (foo)`,
		`topk by (x) (3, foo)`,
		`quantile by (x) (0.9, foo)`,

		// ---- Grouping aggregation child: child-of-child is an
		// AggregationPlan. Exercises the non-fused branch across groupings.
		`sum by (x) (sum by (x, y) (foo))`,
		`max without (y) (avg by (x, y) (foo))`,

		// ---- Range-function child, FUSED shapes (range mode only).
		// These hit canFuseRangeAggregationLogicalDirect=true and route
		// through tryRenderFusedRangeAggregationLogicalDirect without a
		// lowerer-boundary BuildFragment. Covers leaf + subquery ranges
		// under bare sum / sum by / sum without.
		`sum(rate(foo[5m]))`,
		`sum by (x) (rate(foo[5m]))`,
		`sum by (x, job) (rate(foo[5m]))`,
		`sum without (x) (rate(foo[5m]))`,
		`sum by (x) (increase(foo[10m]))`,
		`sum by (x) (avg_over_time(foo[5m]))`,
		`sum by (x) (rate(sum by (x) (foo)[5m:30s]))`,

		// ---- Range-function child, NON-fused shapes. Not SUM, so they
		// take the non-fused branch but still have a range-function
		// child, exercising that branch of the AggregationPlan lowerer.
		`avg by (x) (rate(foo[1m]))`,
		`count by (x) (increase(foo[10m]))`,
		`min by (x) (deriv(foo[5m]))`,

		// ---- Scalar-involving binary children (BinaryPlan under the
		// aggregation). These never fuse and exercise the non-fused
		// branch together with scalar-binary source wrappers.
		`sum(foo * 2)`,
		`sum by (x) (foo / 100)`,
		`avg by (x) (foo + 10)`,
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
