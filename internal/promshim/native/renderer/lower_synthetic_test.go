package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// syntheticFuncCases covers the eight zero-arg synthetic date
// functions that flow through logical.PointwiseFunctionPlan. "time"
// and "pi" are logical.ScalarBuiltinPlan and retire on a later surface
// commit; "literal" is logical.ScalarLiteralPlan and retires with the
// scalar-literal surface. Neither belongs in this surface's tests.
var syntheticFuncCases = []string{
	"minute",
	"hour",
	"day_of_week",
	"day_of_month",
	"day_of_year",
	"days_in_month",
	"month",
	"year",
}

func testRenderParamsInstant() RenderParams {
	return RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 1_700_000_000_000}
}

func testRenderParamsRange() RenderParams {
	return RenderParams{Mode: native.RenderModeRange, StartMS: 1_700_000_000_000, EndMS: 1_700_000_300_000, StepMS: 60_000}
}

// TestLowerPointwiseFunctionMatchesFragment is the byte-identical
// differential guard: for every supported synthetic Func in every
// render mode, lower the plan twice — once through renderer.Lower,
// once through native.BuildFragment + RenderFragment — and fail on any
// diff. The goldens only lock the shape down; this test is what makes
// them meaningful.
func TestLowerPointwiseFunctionMatchesFragment(t *testing.T) {
	for _, fn := range syntheticFuncCases {
		query := fn + "()"
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(fn+"_"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)
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

// TestLowerPointwiseFunctionGolden locks in the exact SQL per func ×
// mode to catch incidental drift. Run with -update to regenerate.
func TestLowerPointwiseFunctionGolden(t *testing.T) {
	for _, fn := range syntheticFuncCases {
		query := fn + "()"
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(fn+"_"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)
				rq, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				goldenPath := filepath.Join("testdata", "synthetic_"+fn+"_"+mode.name+".sql")
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

// TestLowerPointwiseFunctionUnarySourceExprMatchesFragment verifies that
// a PointwiseFunctionPlan with a vector child (e.g. abs(up)) lowers
// through Lower and produces byte-identical SQL to the Fragment path.
// This replaces the earlier "falls back" guard: Lower now handles the
// unary source-expression shape directly via the cached
// FragmentKindUnarySourceExpr.
func TestLowerPointwiseFunctionUnarySourceExprMatchesFragment(t *testing.T) {
	for _, query := range []string{`abs(up)`, `sqrt(up)`, `ceil(up)`} {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(query+"_"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)
				lowerRQ, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
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
			})
		}
	}
}

// TestLowerPointwiseFunctionNilErrors exercises the defensive nil
// guard in lowerPointwiseFunction.
func TestLowerPointwiseFunctionNilErrors(t *testing.T) {
	_, err := lowerPointwiseFunction(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil PointwiseFunctionPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
