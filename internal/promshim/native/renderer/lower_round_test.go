package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// roundCases covers the RoundPlan shapes that lower natively:
//
//   - "round(up)"                          — instant + range (default toNearest=1)
//   - "round(up, 0.5)"                     — instant + range (toNearest=0.5)
//   - "round(rate(http_requests_total[5m]))" — range (round over a range fn)
//
// TestLowerRoundGolden locks a representative subset into .sql files.
var roundCases = []struct {
	name  string
	query string
}{
	{name: "round_up", query: `round(up)`},
	{name: "round_up_0_5", query: `round(up, 0.5)`},
	{name: "round_rate", query: `round(rate(http_requests_total[5m]))`},
}

// goldenRoundCases selects the subset that receive golden files.
var goldenRoundCases = []int{0, 1, 2}

// TestLowerRoundGolden locks in the exact SQL for the golden subset in both
// instant and range modes. Run with -update to regenerate.
func TestLowerRoundGolden(t *testing.T) {
	for _, idx := range goldenRoundCases {
		tc := roundCases[idx]
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
				goldenPath := filepath.Join("testdata", "lower_round", tc.name+"_"+mode.name+".sql")
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

// TestLowerRoundNilErrors exercises the defensive nil guard in lowerRound. A
// nil node must return a non-sentinel error.
func TestLowerRoundNilErrors(t *testing.T) {
	_, err := lowerRound(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil RoundPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

