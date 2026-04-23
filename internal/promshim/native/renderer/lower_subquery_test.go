package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// subqueryCases covers subquery inputs that all lower natively: a bare leaf
// selector subquery, a subquery with label matchers, and a subquery over a
// scalar-transformed child. The differential guard runs each query through
// both Lower and the Fragment path and fails on any SQL diff.
var subqueryCases = []struct {
	name  string
	query string
}{
	{name: "bare", query: `up[5m:1m]`},
	{name: "labelled", query: `http_requests_total{job="api"}[5m:1m]`},
	{name: "scalar_mul", query: `(up * 2)[10m:30s]`},
	{name: "no_step", query: `up[5m:]`},
}

// TestLowerSubqueryMatchesFragment is the byte-identical differential guard
// for the subquery surface: for every case in every render mode, lower the
// plan twice — once through renderer.Lower, once through
// native.BuildFragment + RenderFragment — and fail on any diff. The goldens
// only lock the shape down; this test is what makes them meaningful.
func TestLowerSubqueryMatchesFragment(t *testing.T) {
	for _, tc := range subqueryCases {
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

// TestLowerSubqueryGolden locks in the exact SQL for a subset of the
// differential cases. Run with -update to regenerate. We lock the bare and
// labelled cases in both render modes; the other variants are covered by
// the differential guard alone.
func TestLowerSubqueryGolden(t *testing.T) {
	goldenCases := subqueryCases[:2] // bare + labelled
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
				goldenPath := filepath.Join("testdata", "subquery_"+tc.name+"_"+mode.name+".sql")
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

// TestLowerSubqueryNilErrors exercises the defensive nil guard in
// lowerSubquery. A nil node must return a non-sentinel error (callers
// should not silently fall back to Fragment for a malformed plan tree).
func TestLowerSubqueryNilErrors(t *testing.T) {
	_, err := lowerSubquery(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil SubqueryPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
