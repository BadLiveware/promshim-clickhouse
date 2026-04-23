package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ch-observability/internal/promshim/native"
)

// clampCases covers the three clamp variants that lower natively.
//   - clamp with both scalar literal bounds (instant + range)
//   - clamp_min with a literal lower bound over a range function (range)
//   - clamp_max with a literal upper bound over a direct selector (instant)
//
// The differential guard runs each query × mode through both Lower and the
// Fragment path and fails on any SQL diff. Golden files lock a representative
// subset.
var clampCases = []struct {
	name  string
	query string
}{
	{name: "clamp_up_0_10", query: `clamp(up, 0, 10)`},
	{name: "clamp_min_rate", query: `clamp_min(rate(http_requests_total[5m]), 0)`},
	{name: "clamp_max_node_load1", query: `clamp_max(node_load1, 5)`},
	{name: "clamp_up_neg1_1", query: `clamp(up, -1, 1)`},
}

// TestLowerClampMatchesFragment is the byte-identical differential guard for
// the clamp surface: for every case in every render mode, lower the plan twice
// — once through renderer.Lower, once through native.BuildFragment +
// RenderFragment — and fail on any diff. The goldens only lock the shape down;
// this test is what makes them meaningful.
func TestLowerClampMatchesFragment(t *testing.T) {
	for _, tc := range clampCases {
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

// TestLowerClampGolden locks in the exact SQL for a representative subset of
// the differential cases. Run with -update to regenerate. We lock clamp_up_0_10
// and clamp_max_node_load1 in both render modes; the other variants are covered
// by the differential guard alone.
func TestLowerClampGolden(t *testing.T) {
	goldenCases := []struct {
		name  string
		query string
	}{
		clampCases[0], // clamp_up_0_10
		clampCases[2], // clamp_max_node_load1
	}
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
				goldenPath := filepath.Join("testdata", "lower_clamp", tc.name+"_"+mode.name+".sql")
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

// TestLowerClampNilErrors exercises the defensive nil guard in lowerClamp. A
// nil node must return a non-sentinel error (callers should not silently fall
// back to Fragment for a malformed plan tree).
func TestLowerClampNilErrors(t *testing.T) {
	_, err := lowerClamp(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil PointwiseFunctionPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
