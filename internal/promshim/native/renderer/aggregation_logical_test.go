package renderer

import (
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// TestAggregationLogicalMatchesFragment is the Phase-A4 byte-equality
// differential guard that locks the new logical-path rendering to the
// Fragment path for every AggregationPlan shape. It covers all twelve
// aggregation ops (sum, avg, count, min, max, stddev, stdvar, group, topk,
// bottomk, quantile, count_values) plus the grouping variants (no grouping,
// BY grouping, single- and multi-label grouping, WITHOUT grouping), the
// range-fused children (rate / increase inside sum / avg / count by …), and
// scalar-binary children (sum(foo * 2), sum by (x) (foo / 100)).
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the flipped Lower dispatch (which now
// calls renderAggregationLogical instead of BuildFragment at the boundary) —
// in both instant and range modes, and asserts byte-identical SQL +
// QueryParams.
func TestAggregationLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`sum(foo)`,
		`avg(foo)`,
		`count(foo)`,
		`min(foo)`,
		`max(foo)`,
		`stddev(foo)`,
		`stdvar(foo)`,
		`group(foo)`,
		`sum by (x) (foo)`,
		`sum without (x) (foo)`,
		`avg by (x, y) (foo)`,
		`topk(5, foo)`,
		`bottomk(3, foo)`,
		`quantile(0.9, foo)`,
		`count_values("value", foo)`,
		`sum by (x) (rate(foo[5m]))`,
		`avg by (x) (rate(foo[1m]))`,
		`count by (x) (increase(foo[10m]))`,
		`sum(foo * 2)`,
		`sum by (x) (foo / 100)`,
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
