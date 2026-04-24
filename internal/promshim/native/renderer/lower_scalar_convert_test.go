package renderer

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateLowerGolden is the shared -update flag used by every Lower
// surface's golden test. Surface 1's original local flag was rolled up
// into this package-level flag when Surface 2 landed; future surfaces
// should reuse it rather than declaring their own.
var updateLowerGolden = flag.Bool("update", false, "rewrite golden .sql files for renderer.Lower surface tests")

// scalarConvertCases covers a sampling of PromQL scalar() inputs that
// all lower natively: a bare leaf selector (no rate), a selector with
// label matchers, and scalar() over a range function (rate).
var scalarConvertCases = []struct {
	name  string
	query string
}{
	{name: "bare", query: `scalar(up)`},
	{name: "labelled", query: `scalar(http_requests_total{job="api"})`},
	{name: "rate", query: `scalar(rate(up[5m]))`},
}

// TestLowerScalarConvertGolden locks in the exact SQL for a subset of
// scalarConvertCases. Run with -update to regenerate. We lock both the
// bare and labelled cases in both render modes.
func TestLowerScalarConvertGolden(t *testing.T) {
	goldenCases := scalarConvertCases[:2] // bare + labelled
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
				goldenPath := filepath.Join("testdata", "scalar_convert_"+tc.name+"_"+mode.name+".sql")
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

// TestLowerScalarConvertNilErrors exercises the defensive nil guard in
// lowerScalarConvert. A nil node must return a non-sentinel error
// (callers should not silently fall back to Fragment for a malformed
// plan tree).
func TestLowerScalarConvertNilErrors(t *testing.T) {
	_, err := lowerScalarConvert(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil ScalarConvertPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
