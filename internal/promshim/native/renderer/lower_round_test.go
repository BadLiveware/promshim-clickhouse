package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// roundCases covers the RoundPlan shapes that lower natively:
//
//   - "round(up)"                          — instant + range (default toNearest=1)
//   - "round(up, 0.5)"                     — instant + range (toNearest=0.5)
//   - "round(rate(http_requests_total[5m]))" — range (round over a range fn)
//
// The differential guard (TestLowerRoundMatchesFragment) verifies that Lower
// produces byte-identical SQL to the Fragment path for every case × mode.
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

// TestLowerRoundMatchesFragment is the byte-identical differential guard for
// Surface 14 (RoundPlan): for every case in every render mode, lower the plan
// twice — once through renderer.Lower, once through native.BuildFragment +
// RenderFragment — and fail on any diff.
func TestLowerRoundMatchesFragment(t *testing.T) {
	for _, tc := range roundCases {
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

// TestBuildRoundValueExprMatchesApply locks byte-identity between
// buildRoundValueExpr (reads the logical node's Decimals directly) and the
// ValueExpr template constructed inside applyRoundValueTransform (reachable
// here via native.BuildFragment on a RoundPlan). The direct-render path uses
// the former; the Fragment path uses the latter. Any drift would break the
// byte-identical SQL guarantee between the two paths.
func TestBuildRoundValueExprMatchesApply(t *testing.T) {
	for _, tc := range roundCases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, nativeAnalysis := buildLowerInputs(t, tc.query)
			fragment, err := native.BuildFragment(root, nativeAnalysis)
			if err != nil {
				t.Fatalf("BuildFragment: %v", err)
			}
			if fragment.ValueTransform == nil {
				t.Fatalf("fragment has no ValueTransform spec")
			}
			fragmentExpr := fragment.ValueTransform.ValueExpr
			// Recover toNearest from the logical RoundPlan: default 1.0 when Decimals nil.
			round, ok := root.(*logicalpkg.RoundPlan)
			if !ok {
				t.Fatalf("expected *RoundPlan, got %T", root)
			}
			toNearest := 1.0
			if round.Decimals != nil {
				toNearest = *round.Decimals
			}
			directExpr, ok := buildRoundValueExpr(toNearest)
			if !ok {
				t.Fatalf("buildRoundValueExpr returned ok=false for toNearest=%v", toNearest)
			}
			if fragmentExpr != directExpr {
				t.Errorf("value expressions differ\nFragment: %s\nDirect:   %s", fragmentExpr, directExpr)
			}
		})
	}
}
