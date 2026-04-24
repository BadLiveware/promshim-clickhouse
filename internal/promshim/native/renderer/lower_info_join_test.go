package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// infoJoinCases covers the two supported info() shapes across instant and
// range modes:
//   - Default target_info join: info(up)
//   - Explicit regex metric-name selector: info(up, {__name__=~".+_info"})
var infoJoinCases = []struct {
	name  string
	query string
}{
	// Default implicit target_info join — no selector argument.
	{
		name:  "info_up_default",
		query: `info(up)`,
	},
	// Default implicit target_info join with labelled child selector.
	{
		name:  "info_up_job_default",
		query: `info(up{job="api"})`,
	},
	// Explicit regex metric-name selector.
	{
		name:  "info_up_regex_selector",
		query: `info(up, {__name__=~".+_info"})`,
	},
	// Explicit regex selector with labelled child.
	{
		name:  "info_up_job_regex_selector",
		query: `info(up{job="api"}, {__name__=~".+_info"})`,
	},
}

// TestLowerInfoJoinGolden locks in the exact SQL for all cases in both
// instant and range modes. Run with -update to regenerate golden files.
func TestLowerInfoJoinGolden(t *testing.T) {
	for _, tc := range infoJoinCases {
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
				goldenPath := filepath.Join("testdata", "lower_info_join", tc.name+"_"+mode.name+".sql")
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

// TestLowerInfoJoinNilErrors exercises the defensive nil guard in
// lowerInfoJoin. A nil node must return a non-sentinel error.
func TestLowerInfoJoinNilErrors(t *testing.T) {
	_, err := lowerInfoJoin(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
