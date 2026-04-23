package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// rangeFunctionCases covers all seven logical plan kinds that produce
// FragmentKindRangeFunction. Each row is tested in both instant and range
// render modes by the differential guard. The first five canonical shapes are
// also locked by golden files.
var rangeFunctionCases = []struct {
	name  string
	query string
}{
	// — RatePlan —
	{name: "rate_5m", query: `rate(http_requests_total[5m])`},
	// — IncreasePlan —
	{name: "increase_1h", query: `increase(http_requests_total[1h])`},
	// — DeltaPlan —
	{name: "delta_10m", query: `delta(cpu_usage[10m])`},
	// — DerivPlan —
	{name: "deriv_10m", query: `deriv(cpu_usage[10m])`},
	// — ChangesPlan —
	{name: "changes_1h", query: `changes(process_starts[1h])`},
	// — RangeFunctionPlan (avg_over_time) —
	{name: "avg_over_time_5m", query: `avg_over_time(up[5m])`},
	// — RangeFunctionPlan (sum_over_time) — instant mode is primary
	{name: "sum_over_time_5m", query: `sum_over_time(up[5m])`},
	// — RangeFunctionPlan (max_over_time) —
	{name: "max_over_time_5m", query: `max_over_time(up[5m])`},
	// — QuantileOverTimePlan —
	{name: "quantile_over_time_095_5m", query: `quantile_over_time(0.95, request_duration[5m])`},
}

// TestLowerRangeFunctionMatchesFragment is the byte-identical differential
// guard for the range function surface: for every case in every render mode,
// lower the plan twice — once through renderer.Lower, once through
// native.BuildFragment + RenderFragment — and fail on any diff. The goldens
// only lock the shape down; this test is what makes them meaningful.
func TestLowerRangeFunctionMatchesFragment(t *testing.T) {
	for _, tc := range rangeFunctionCases {
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

// TestLowerRangeFunctionGolden locks in the exact SQL for the first five
// canonical shapes (rate, increase, delta, deriv, changes) in both render
// modes. The remaining variants are covered by the differential guard alone.
// Run with -update to regenerate golden files.
func TestLowerRangeFunctionGolden(t *testing.T) {
	goldenCases := rangeFunctionCases[:5] // rate, increase, delta, deriv, changes
	for _, tc := range goldenCases {
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
				goldenPath := filepath.Join("testdata", "lower_range_function", tc.name+"_"+mode.name+".sql")
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

// TestLowerRangeFunctionNilErrors exercises the defensive nil guard in
// lowerRangeFunction. A nil node must return a non-sentinel error (callers
// should not silently fall back to Fragment for a malformed plan tree).
func TestLowerRangeFunctionNilErrors(t *testing.T) {
	_, err := lowerRangeFunction(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
