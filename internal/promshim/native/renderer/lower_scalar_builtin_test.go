package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// scalarBuiltinFuncCases covers the two zero-arg synthetic scalar
// builtins that flow through logical.ScalarBuiltinPlan: time() and
// pi(). The synthetic date functions (minute/hour/...) flow through
// logical.PointwiseFunctionPlan and live in lower_synthetic_test.go.
var scalarBuiltinFuncCases = []string{
	"time",
	"pi",
}

// TestLowerScalarBuiltinMatchesFragment is the byte-identical
// differential guard: for every supported builtin in every render
// mode, lower the plan twice — once through renderer.Lower, once
// through native.BuildFragment + RenderFragment — and fail on any
// diff. This is what locks byte-identity to the shared
// syntheticSeriesValueSQL helper.
func TestLowerScalarBuiltinMatchesFragment(t *testing.T) {
	for _, fn := range scalarBuiltinFuncCases {
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
				if _, ok := root.(*logicalpkg.ScalarBuiltinPlan); !ok {
					t.Fatalf("expected ScalarBuiltinPlan root for %q, got %T", query, root)
				}
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

// TestLowerScalarBuiltinGolden locks in the exact SQL per func × mode
// to catch incidental drift. Run with -update to regenerate goldens.
func TestLowerScalarBuiltinGolden(t *testing.T) {
	for _, fn := range scalarBuiltinFuncCases {
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
				goldenPath := filepath.Join("testdata", "lower_scalar_builtin", fn+"_"+mode.name+".sql")
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

// TestLowerScalarBuiltinNilErrors exercises the defensive nil guard
// in lowerScalarBuiltin. A nil node must return a non-sentinel error.
func TestLowerScalarBuiltinNilErrors(t *testing.T) {
	_, err := lowerScalarBuiltin(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil ScalarBuiltinPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestLowerScalarBuiltinUnsupportedFunc verifies that an unknown Func
// surfaces through renderScalarBuiltinLogical with a non-nil error
// (rather than silently producing empty SQL). This guards the
// isSupportedNativeScalarBuiltinFuncName switch against accidental
// drift from analysis_support.go.
func TestLowerScalarBuiltinUnsupportedFunc(t *testing.T) {
	node := &logicalpkg.ScalarBuiltinPlan{Func: "bogus"}
	_, err := lowerScalarBuiltin(LoweringCtx{Params: testRenderParamsInstant()}, node)
	if err == nil {
		t.Fatalf("expected error for unsupported ScalarBuiltinPlan Func")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for unsupported func, got sentinel")
	}
}
