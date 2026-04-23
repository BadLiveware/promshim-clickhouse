package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ch-observability/internal/promshim/native"
)

// unaryCases covers the UnaryPlan shapes that lower natively:
//
//   - "-up"                        — instant + range (UnarySourceExpr: negation folded into source)
//   - "-rate(http_requests_total[5m])"  — range (ValueTransform: negation over a range fn)
//   - "-(-up)"                     — instant (UnarySourceExpr: double-negation, NOT folded by constant_fold_unary
//     because neither child is a scalar literal; both negations cancel in the
//     source expr transform)
//   - "+up"                        — instant (UnarySourceExpr: identity unary, effectively a no-op source pass)
//
// The differential guard (TestLowerUnaryMatchesFragment) verifies that Lower
// produces byte-identical SQL to the Fragment path for every case × mode.
// TestLowerUnaryGolden locks a representative subset into .sql files.
var unaryCases = []struct {
	name  string
	query string
}{
	{name: "neg_up", query: `-up`},
	{name: "neg_rate", query: `-rate(http_requests_total[5m])`},
	{name: "neg_neg_up", query: `-(-up)`},
	{name: "pos_up", query: `+up`},
}

// goldenUnaryCases selects the subset that receive golden files.
var goldenUnaryCases = []int{0, 1, 2}

// TestLowerUnaryMatchesFragment is the byte-identical differential guard for
// Surface 14 (UnaryPlan): for every case in every render mode, lower the plan
// twice — once through renderer.Lower, once through native.BuildFragment +
// RenderFragment — and fail on any diff.
func TestLowerUnaryMatchesFragment(t *testing.T) {
	for _, tc := range unaryCases {
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

// TestLowerUnaryGolden locks in the exact SQL for the golden subset in both
// instant and range modes. Run with -update to regenerate.
func TestLowerUnaryGolden(t *testing.T) {
	for _, idx := range goldenUnaryCases {
		tc := unaryCases[idx]
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
				goldenPath := filepath.Join("testdata", "lower_unary", tc.name+"_"+mode.name+".sql")
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

// TestLowerUnaryNilErrors exercises the defensive nil guard in lowerUnary. A
// nil node must return a non-sentinel error.
func TestLowerUnaryNilErrors(t *testing.T) {
	_, err := lowerUnary(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil UnaryPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestLowerUnaryNilNativeAnalysisReturnsUnsupported verifies that a nil
// NativeAnalysis returns errUnsupportedLowerNode so the caller falls back to
// the Fragment path rather than panicking or returning a hard error.
func TestLowerUnaryNilNativeAnalysisReturnsUnsupported(t *testing.T) {
	root, analysis, _ := buildLowerInputs(t, `-up`)
	_, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nil,
		Params:         testRenderParamsInstant(),
	}, root)
	if !errors.Is(err, errUnsupportedLowerNode) {
		t.Errorf("expected errUnsupportedLowerNode when NativeAnalysis is nil, got %v", err)
	}
}
