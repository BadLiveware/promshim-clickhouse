package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ch-observability/internal/promshim/native"
)

// aggregationCases covers the full spread of aggregation shapes:
//   - simple ops without grouping (sum, count)
//   - ops with BY grouping (sum by, avg without)
//   - selection aggregations (topk, bottomk, quantile)
//   - count_values (synthesizes a new label dimension)
//   - aggregation-range-fused paths (sum/avg without over rate/range functions)
//
// The differential guard runs each query × mode through both Lower and the
// Fragment path and fails on any SQL diff. Golden files lock a representative
// subset (first 5 canonical + both fused cases).
var aggregationCases = []struct {
	name  string
	query string
}{
	// — simple ops, no grouping —
	{name: "sum_up_instant", query: `sum(up)`},
	{name: "count_up_instant", query: `count(up)`},
	// — BY grouping —
	{name: "sum_by_job_up", query: `sum by (job) (up)`},
	// — WITHOUT grouping —
	{name: "avg_without_instance_up", query: `avg without (instance) (up)`},
	// — selection aggregation —
	{name: "topk_3_up", query: `topk(3, up)`},
	// — quantile —
	{name: "quantile_095_rate", query: `quantile(0.95, rate(http_requests_total[5m]))`},
	// — count_values (synthesizes output label) —
	{name: "count_values_le_up", query: `count_values("le", up)`},
	// — aggregation-range-fused: sum by + rate —
	{name: "sum_by_job_rate_fused", query: `sum by (job) (rate(http_requests_total[5m]))`},
	// — aggregation-range-fused: avg without + range function —
	{name: "sum_without_instance_rate_fused", query: `sum without (instance) (rate(cpu[5m]))`},
}

// goldenAggregationCases selects the subset of aggregationCases that receive
// golden files: first 5 canonical shapes plus both fused cases.
var goldenAggregationCases = []int{0, 1, 2, 3, 4, 7, 8}

// TestLowerAggregationMatchesFragment is the byte-identical differential guard
// for the aggregation surface: for every case in every render mode, lower the
// plan twice — once through renderer.Lower, once through native.BuildFragment +
// RenderFragment — and fail on any diff. The goldens only lock the shape down;
// this test is what makes them meaningful.
func TestLowerAggregationMatchesFragment(t *testing.T) {
	for _, tc := range aggregationCases {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(tc.name+"_"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
				lowerCtx := LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}
				lowerRQ, err := Lower(lowerCtx, root)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				fragment, err := native.BuildFragment(root, nativeAnalysis)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				fragmentRQ, err := RenderFragment(testRenderConfig(), fragment, mode.params)
				if err != nil {
					t.Fatalf("RenderFragment: %v", err)
				}
				if lowerRQ.SQL != fragmentRQ.SQL {
					t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", lowerRQ.SQL, fragmentRQ.SQL)
				}
				if len(lowerRQ.QueryParams) != len(fragmentRQ.QueryParams) {
					t.Errorf("QueryParams len differs: Lower=%v Fragment=%v", lowerRQ.QueryParams, fragmentRQ.QueryParams)
				}
				for k, v := range fragmentRQ.QueryParams {
					if lowerRQ.QueryParams[k] != v {
						t.Errorf("QueryParams[%q] differs: Lower=%q Fragment=%q", k, lowerRQ.QueryParams[k], v)
					}
				}
			})
		}
	}
}

// TestLowerAggregationGolden locks in the exact SQL for the first five
// canonical shapes plus both fused cases in both render modes.
// Run with -update to regenerate golden files.
func TestLowerAggregationGolden(t *testing.T) {
	for _, idx := range goldenAggregationCases {
		tc := aggregationCases[idx]
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(tc.name+"_"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
				rq, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				goldenPath := filepath.Join("testdata", "lower_aggregation", tc.name+"_"+mode.name+".sql")
				if *updateLowerGolden {
					if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
						t.Fatalf("mkdir testdata: %v", err)
					}
					if err := os.WriteFile(goldenPath, []byte(rq.SQL), 0o644); err != nil {
						t.Fatalf("write golden: %v", err)
					}
					return
				}
				want, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("read golden (run with -update to create): %v", err)
				}
				if string(want) != rq.SQL {
					t.Errorf("SQL differs from golden %s\nwant:\n%s\ngot:\n%s", goldenPath, want, rq.SQL)
				}
			})
		}
	}
}

// TestLowerAggregationNilErrors exercises the defensive nil guard in
// lowerAggregation. A nil node must return a non-sentinel error (callers should
// not silently fall back to Fragment for a malformed plan tree).
func TestLowerAggregationNilErrors(t *testing.T) {
	_, err := lowerAggregation(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil AggregationPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
