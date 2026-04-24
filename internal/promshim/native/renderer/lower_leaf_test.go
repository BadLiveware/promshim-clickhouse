package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/prometheus/prometheus/promql/parser"
)

// leafCases covers the selector-backed LeafExprPlan shapes that lower
// natively. These are the canonical inputs validated by the golden test.
//
//   - "up"                          — bare metric name (instant selector)
//   - `up{job="prometheus"}`        — metric name + label matcher
//   - `{__name__="foo"}`            — matcher only, no metric name shorthand
//   - `up offset 5m`               — instant selector with offset
//   - `up[5m]`                      — range (matrix) selector
var leafCases = []struct {
	name  string
	query string
}{
	{name: "bare_selector", query: `up`},
	{name: "selector_with_matcher", query: `up{job="prometheus"}`},
	{name: "matcher_only", query: `{__name__="foo"}`},
	{name: "selector_with_offset", query: `up offset 5m`},
	{name: "matrix_selector", query: `up[5m]`},
}

// TestLowerLeafGolden locks in the exact SQL for all selector-backed leaf
// cases × modes. Run with -update to regenerate golden files.
func TestLowerLeafGolden(t *testing.T) {
	for _, tc := range leafCases {
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
				goldenPath := filepath.Join("testdata", "lower_leaf", tc.name+"_"+mode.name+".sql")
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

// TestLowerLeafNilErrors exercises the defensive nil guard in lowerLeaf.
// A nil node must return a non-sentinel error.
func TestLowerLeafNilErrors(t *testing.T) {
	_, err := lowerLeaf(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil LeafExprPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestLowerLeafDelegatedMissingResolverErrors verifies that a delegated
// (non-selector) leaf with a nil ResolveSourcePromQL returns an error.
// We construct a LeafExprPlan whose Expr is a NumberLiteral — not a
// VectorSelector or MatrixSelector — so buildSelectorSource returns
// (nil, nil), triggering the delegated PromQL branch where a nil
// resolver must produce an error.
func TestLowerLeafDelegatedMissingResolverErrors(t *testing.T) {
	// A NumberLiteral is neither VectorSelector nor MatrixSelector, so
	// buildSelectorSource returns (nil, nil) — the delegated PromQL path.
	expr := &parser.NumberLiteral{Val: 1.0}
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}
	analysis := logicalpkg.Analyze(leaf)
	nativeAnalysis := native.Analyze(leaf)

	// Instant mode with nil resolver.
	_, err := lowerLeaf(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(), // ResolveSourcePromQL is nil
	}, leaf)
	if err == nil {
		t.Fatalf("expected error for delegated leaf with nil resolver")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error, got sentinel")
	}
}
