package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// labelTransformCases covers label_replace and label_join variants that lower
// natively: label_replace on a direct selector, label_replace on a range
// function result, label_join with two source labels, and label_join with
// three source labels over a rate.
var labelTransformCases = []struct {
	name  string
	query string
}{
	{name: "label_replace_simple", query: `label_replace(up, "new_label", "$1", "job", "(.*)")`},
	{name: "label_replace_rate", query: `label_replace(rate(http_requests_total[5m]), "method_lower", "$1", "method", "(.+)")`},
	{name: "label_join_two", query: `label_join(up, "combined", "-", "job", "instance")`},
	{name: "label_join_three", query: `label_join(rate(http_requests_total[5m]), "src", "/", "job", "instance", "method")`},
}

// TestLowerLabelTransformGolden locks in the exact SQL for a representative
// subset of labelTransformCases. Run with -update to regenerate. We lock
// label_replace_simple and label_join_two in both render modes.
func TestLowerLabelTransformGolden(t *testing.T) {
	goldenCases := []struct {
		name  string
		query string
	}{
		labelTransformCases[0], // label_replace_simple
		labelTransformCases[2], // label_join_two
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
				goldenPath := filepath.Join("testdata", "label_transform_"+tc.name+"_"+mode.name+".sql")
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

// TestLowerLabelTransformNilErrors exercises the defensive nil guard in
// lowerLabelTransform. A nil node must return a non-sentinel error
// (callers should not silently fall back to Fragment for a malformed
// plan tree).
func TestLowerLabelTransformNilErrors(t *testing.T) {
	_, err := lowerLabelTransform(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
