package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
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
