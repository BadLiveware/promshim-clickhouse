package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// absentCases covers AbsentPlan (absent) and AbsentOverTimePlan
// (absent_over_time) across the canonical queries listed in the
// Surface 11 spec.
var absentCases = []struct {
	name  string
	query string
}{
	// AbsentPlan — bare metric selector
	{
		name:  "absent_up",
		query: `absent(up)`,
	},
	// AbsentPlan — metric selector with label matcher
	{
		name:  "absent_nonexistent_job",
		query: `absent(nonexistent_metric{job="foo"})`,
	},
	// AbsentOverTimePlan — simple 5m range
	{
		name:  "absent_over_time_up",
		query: `absent_over_time(up[5m])`,
	},
	// AbsentOverTimePlan — longer range
	{
		name:  "absent_over_time_rate_requests",
		query: `absent_over_time(rate_requests_total[1h])`,
	},
}

// TestLowerAbsentMatchesFragment is the byte-identical differential guard:
// for every case × mode, lower through renderer.Lower and through
// native.BuildFragment + RenderFragment, then fail on any diff.
func TestLowerAbsentMatchesFragment(t *testing.T) {
	for _, tc := range absentCases {
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

// TestLowerAbsentGolden locks in the exact SQL for all cases in both
// instant and range modes. Run with -update to regenerate golden files.
func TestLowerAbsentGolden(t *testing.T) {
	for _, tc := range absentCases {
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
				goldenPath := filepath.Join("testdata", "lower_absent", tc.name+"_"+mode.name+".sql")
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

// TestLowerAbsentNilErrors exercises the defensive nil guard in
// lowerAbsent. A nil node must return a non-sentinel error.
func TestLowerAbsentNilErrors(t *testing.T) {
	_, err := lowerAbsent(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
