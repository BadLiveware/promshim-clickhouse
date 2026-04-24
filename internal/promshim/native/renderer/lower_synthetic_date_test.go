package renderer

import (
	"os"
	"path/filepath"
	"testing"

	"ch-observability/internal/promshim/native"
)

// TestLowerSyntheticDateGolden locks in the exact SQL for all 8 date
// functions × 2 render modes. Run with -update to regenerate golden files.
func TestLowerSyntheticDateGolden(t *testing.T) {
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
				goldenPath := filepath.Join("testdata", "lower_synthetic_date", fn+"_"+mode.name+".sql")
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

// TestLowerSyntheticDateDirectHelper tests renderSyntheticDateFragment
// directly — including error paths for unsupported func names and bad mode.
func TestLowerSyntheticDateDirectHelper(t *testing.T) {
	t.Run("unsupported_func", func(t *testing.T) {
		_, err := renderSyntheticDateFragment("abs", testRenderParamsInstant())
		if err == nil {
			t.Fatal("expected error for unsupported func name")
		}
	})
	t.Run("range_zero_step", func(t *testing.T) {
		badParams := RenderParams{Mode: native.RenderModeRange, StartMS: 1_700_000_000_000, EndMS: 1_700_000_300_000, StepMS: 0}
		_, err := renderSyntheticDateFragment("minute", badParams)
		if err == nil {
			t.Fatal("expected error for zero step in range mode")
		}
	})
}
