package renderer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// TestLowerSyntheticDateMatchesFragment is the byte-identical differential
// guard for the direct renderSyntheticDateFragment path: for each of the 8
// supported date func names in both RenderModes, compare the output of
// lowerPointwiseFunction (direct path) byte-for-byte against the Fragment
// path (construct the same Fragment manually, call renderSyntheticFragment +
// finalizeRenderedFragment). A mismatch here means the two paths have drifted.
func TestLowerSyntheticDateMatchesFragment(t *testing.T) {
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
				directRQ, err := Lower(lowerCtx, root)
				if err != nil {
					t.Fatalf("Lower (direct): %v", err)
				}

				// Fragment path: construct the Fragment manually and call
				// renderSyntheticFragment + finalizeRenderedFragment so both
				// paths go through syntheticSeriesValueSQL independently.
				rf, err := renderSyntheticDateFragment(fn, mode.params)
				if err != nil {
					t.Fatalf("renderSyntheticDateFragment: %v", err)
				}
				fragmentRQ, err := finalizeRenderedFragment(rf)
				if err != nil {
					t.Fatalf("finalizeRenderedFragment: %v", err)
				}

				if directRQ.SQL != fragmentRQ.SQL {
					t.Errorf("SQL differs:\nDirect:   %s\nFragment: %s", directRQ.SQL, fragmentRQ.SQL)
				}
				if len(directRQ.QueryParams) != len(fragmentRQ.QueryParams) {
					t.Errorf("QueryParams len differs: Direct=%v Fragment=%v", directRQ.QueryParams, fragmentRQ.QueryParams)
				}
				for k, v := range fragmentRQ.QueryParams {
					if directRQ.QueryParams[k] != v {
						t.Errorf("QueryParams[%q] differs: Direct=%q Fragment=%q", k, directRQ.QueryParams[k], v)
					}
				}
			})
		}
	}
}

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
