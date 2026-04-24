package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// vectorCases covers the VectorPlan shapes that lower natively:
//
//   - "vector(1)"              — instant + range (SyntheticSeries scalar literal lifted to vector)
//   - "vector(time())"         — instant + range (SyntheticSeries time scalar lifted to vector)
//   - "vector(scalar(sum(up)))" — instant (ScalarConvert child: scalar() aggregation lifted to vector)
//
// TestLowerVectorGolden locks a representative subset into .sql files.
var vectorCases = []struct {
	name  string
	query string
}{
	{name: "vector_1", query: `vector(1)`},
	{name: "vector_time", query: `vector(time())`},
	{name: "vector_scalar_sum_up", query: `vector(scalar(sum(up)))`},
}

// goldenVectorCases selects the subset that receive golden files.
var goldenVectorCases = []int{0, 1, 2}

// TestLowerVectorGolden locks in the exact SQL for the golden subset in both
// instant and range modes. Run with -update to regenerate.
func TestLowerVectorGolden(t *testing.T) {
	for _, idx := range goldenVectorCases {
		tc := vectorCases[idx]
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
				goldenPath := filepath.Join("testdata", "lower_vector", tc.name+"_"+mode.name+".sql")
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

// TestLowerVectorNilErrors exercises the defensive nil guard in lowerVector. A
// nil node must return a non-sentinel error.
func TestLowerVectorNilErrors(t *testing.T) {
	_, err := lowerVector(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil VectorPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestLowerVectorNilChildErrors ensures a VectorPlan with a nil Child
// returns an explicit non-sentinel error rather than recursing into
// Lower with nil. The sentinel path is reserved for child kinds that
// aren't yet direct-lowered; a structurally invalid plan is a hard
// failure.
func TestLowerVectorNilChildErrors(t *testing.T) {
	_, err := lowerVector(LoweringCtx{}, &logicalpkg.VectorPlan{Child: nil})
	if err == nil {
		t.Fatalf("expected error for VectorPlan with nil Child")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil child, got sentinel")
	}
}
