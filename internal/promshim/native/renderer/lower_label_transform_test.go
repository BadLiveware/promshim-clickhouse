package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ch-observability/internal/promshim/native"
)

// labelTransformCases covers label_replace and label_join variants that lower
// natively: label_replace on a direct selector, label_replace on a range
// function result, label_join with two source labels, and label_join with
// three source labels over a rate.
var labelTransformCases = []struct {
	name  string
	query string
}{
	{name: "label_replace_simple", query: `label_replace(up, "new_label", "$1", "job", "(.*)")`},
	{name: "label_replace_rate", query: `label_replace(rate(http_requests_total[5m]), "method_lower", "$1", "method", "(.+)")`},
	{name: "label_join_two", query: `label_join(up, "combined", "-", "job", "instance")`},
	{name: "label_join_three", query: `label_join(rate(http_requests_total[5m]), "src", "/", "job", "instance", "method")`},
}

// TestLowerLabelTransformMatchesFragment is the byte-identical differential
// guard for the label_transform surface: for every case in every render mode,
// lower the plan twice — once through renderer.Lower, once through
// native.BuildFragment + RenderFragment — and fail on any diff. The goldens
// only lock the shape down; this test is what makes them meaningful.
func TestLowerLabelTransformMatchesFragment(t *testing.T) {
	for _, tc := range labelTransformCases {
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

// TestLowerLabelTransformGolden locks in the exact SQL for a representative
// subset of the differential cases. Run with -update to regenerate. We lock
// label_replace_simple and label_join_two in both render modes; the other
// variants are covered by the differential guard alone.
func TestLowerLabelTransformGolden(t *testing.T) {
	goldenCases := []struct {
		name  string
		query string
	}{
		labelTransformCases[0], // label_replace_simple
		labelTransformCases[2], // label_join_two
	}
	for _, tc := range goldenCases {
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
				goldenPath := filepath.Join("testdata", "label_transform_"+tc.name+"_"+mode.name+".sql")
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

// TestLowerLabelTransformNilErrors exercises the defensive nil guard in
// lowerLabelTransform. A nil node must return a non-sentinel error
// (callers should not silently fall back to Fragment for a malformed
// plan tree).
func TestLowerLabelTransformNilErrors(t *testing.T) {
	_, err := lowerLabelTransform(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestBuildLabelTransformTagsExprLogicalMatchesFragment locks byte-identity
// between buildLabelTransformTagsExpr (reads *LabelTransformFragment) and
// buildLabelTransformTagsExprFromLogical (reads the logical node directly).
// The direct-render path uses the latter; the Fragment path uses the former.
// Both must produce the same mutated-tags SQL expression for equivalent
// inputs, otherwise the outer SELECT wrapping renderLabelTransformFromSource
// would emit drifting SQL.
func TestBuildLabelTransformTagsExprLogicalMatchesFragment(t *testing.T) {
	for _, tc := range labelTransformCases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, nativeAnalysis := buildLowerInputs(t, tc.query)
			fragment, err := native.BuildFragment(root, nativeAnalysis)
			if err != nil {
				t.Fatalf("BuildFragment: %v", err)
			}
			if fragment.LabelTransform == nil {
				t.Fatalf("fragment has no LabelTransform spec")
			}
			const tagsExpr = "label_child.tags"
			fragmentExpr, err := buildLabelTransformTagsExpr(fragment.LabelTransform, tagsExpr)
			if err != nil {
				t.Fatalf("buildLabelTransformTagsExpr: %v", err)
			}
			logicalExpr, err := buildLabelTransformTagsExprFromLogical(root, tagsExpr)
			if err != nil {
				t.Fatalf("buildLabelTransformTagsExprFromLogical: %v", err)
			}
			if fragmentExpr != logicalExpr {
				t.Errorf("tags expressions differ\nFragment: %s\nLogical:  %s", fragmentExpr, logicalExpr)
			}
		})
	}
}
