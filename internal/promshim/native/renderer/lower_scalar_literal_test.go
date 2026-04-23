package renderer

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/prometheus/prometheus/promql/parser"
)

// scalarLiteralCases covers the value space mandated by the surface spec:
// zero, positive integer, negative fractional, Inf, and NaN. Each value
// is expressed as a PromQL number literal so buildLowerInputs can produce
// the canonical plan tree, except Inf and NaN which are constructed
// directly since PromQL has no NaN literal.
var scalarLiteralCases = []struct {
	name  string
	value float64
	// useQuery, if non-empty, is parsed through buildLowerInputs.
	// Otherwise the test constructs ScalarLiteralPlan directly.
	useQuery string
}{
	{name: "zero", value: 0, useQuery: "0"},
	{name: "positive_int", value: 42, useQuery: "42"},
	{name: "negative_fractional", value: -1.5, useQuery: "-1.5"},
	{name: "inf", value: math.Inf(1)},
	{name: "nan", value: math.NaN()},
}

// buildScalarLiteralInputs returns the plan node and analysis for a
// ScalarLiteralPlan with the given value. When useQuery is set the plan
// is built from parsed PromQL (so it exercises the full pipeline);
// otherwise it is constructed directly for values that have no PromQL
// literal (Inf, NaN).
func buildScalarLiteralInputs(t *testing.T, tc struct {
	name     string
	value    float64
	useQuery string
}) (*logicalpkg.ScalarLiteralPlan, *logicalpkg.Analysis, *native.Analysis) {
	t.Helper()
	if tc.useQuery != "" {
		root, analysis, nativeAnalysis := buildLowerInputs(t, tc.useQuery)
		node, ok := root.(*logicalpkg.ScalarLiteralPlan)
		if !ok {
			t.Fatalf("expected ScalarLiteralPlan root for query %q, got %T", tc.useQuery, root)
		}
		return node, analysis, nativeAnalysis
	}
	// Construct the plan directly for special float values.
	expr := &parser.NumberLiteral{Val: tc.value}
	node := &logicalpkg.ScalarLiteralPlan{Expr: expr, Value: tc.value}
	analysis := logicalpkg.Analyze(node)
	nativeAnalysis := native.Analyze(node)
	return node, analysis, nativeAnalysis
}

// TestLowerScalarLiteralMatchesFragment is the byte-identical differential
// guard: for every value × mode, lower the plan twice — once through
// renderer.Lower (direct path), once through native.BuildFragment +
// RenderFragment (Fragment path) — and fail on any diff. NaN values are
// compared as strings because math.NaN() != math.NaN().
func TestLowerScalarLiteralMatchesFragment(t *testing.T) {
	for _, tc := range scalarLiteralCases {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(tc.name+"_"+mode.name, func(t *testing.T) {
				node, analysis, nativeAnalysis := buildScalarLiteralInputs(t, tc)
				lowerCtx := LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}
				lowerRQ, err := Lower(lowerCtx, node)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				fragment, err := native.BuildFragment(node, nativeAnalysis)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				fragmentRQ, err := RenderFragment(testRenderConfig(), fragment, mode.params)
				if err != nil {
					t.Fatalf("RenderFragment: %v", err)
				}
				if lowerRQ.SQL != fragmentRQ.SQL {
					t.Errorf("SQL differs:\nDirect:   %s\nFragment: %s", lowerRQ.SQL, fragmentRQ.SQL)
				}
				if len(lowerRQ.QueryParams) != len(fragmentRQ.QueryParams) {
					t.Errorf("QueryParams len differs: Direct=%v Fragment=%v", lowerRQ.QueryParams, fragmentRQ.QueryParams)
				}
				for k, v := range fragmentRQ.QueryParams {
					if lowerRQ.QueryParams[k] != v {
						t.Errorf("QueryParams[%q] differs: Direct=%q Fragment=%q", k, lowerRQ.QueryParams[k], v)
					}
				}
			})
		}
	}
}

// TestLowerScalarLiteralGolden locks in the exact SQL for all values ×
// modes. Run with -update to regenerate golden files.
func TestLowerScalarLiteralGolden(t *testing.T) {
	for _, tc := range scalarLiteralCases {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(tc.name+"_"+mode.name, func(t *testing.T) {
				node, analysis, nativeAnalysis := buildScalarLiteralInputs(t, tc)
				rq, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, node)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				goldenPath := filepath.Join("testdata", "lower_scalar_literal", tc.name+"_"+mode.name+".sql")
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

// TestLowerScalarLiteralNilErrors exercises the defensive nil guard in
// lowerScalarLiteral. A nil node must return a non-sentinel error.
func TestLowerScalarLiteralNilErrors(t *testing.T) {
	_, err := lowerScalarLiteral(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil ScalarLiteralPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
