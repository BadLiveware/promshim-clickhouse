package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// histogramProjectionCases covers all five supported histogram projection
// functions. The metric http_request_duration_seconds_bucket is used as it
// matches the fixture employed by the native analysis tests.
var histogramProjectionCases = []struct {
	name  string
	query string
}{
	{name: "histogram_count", query: `histogram_count(http_request_duration_seconds_bucket{job="api"})`},
	{name: "histogram_sum", query: `histogram_sum(http_request_duration_seconds_bucket{job="api"})`},
	{name: "histogram_avg", query: `histogram_avg(http_request_duration_seconds_bucket{job="api"})`},
	{name: "histogram_stddev", query: `histogram_stddev(http_request_duration_seconds_bucket{job="api"})`},
	{name: "histogram_stdvar", query: `histogram_stdvar(http_request_duration_seconds_bucket{job="api"})`},
}

// TestLowerHistogramProjectionGolden locks in the exact SQL for all five
// functions in instant mode. Range mode is covered by the differential guard
// alone. Run with -update to regenerate golden files.
func TestLowerHistogramProjectionGolden(t *testing.T) {
	for _, tc := range histogramProjectionCases {
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
				goldenPath := filepath.Join("testdata", "lower_histogram_projection", tc.name+"_"+mode.name+".sql")
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

// TestLowerHistogramProjectionNilErrors exercises the defensive nil guard in
// lowerHistogramProjection. A nil node must return a non-sentinel error
// (callers should not silently fall back to Fragment for a malformed plan tree).
func TestLowerHistogramProjectionNilErrors(t *testing.T) {
	_, err := lowerHistogramProjection(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
