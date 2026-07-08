package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

// subqueryCases covers subquery inputs that all lower natively: a bare leaf
// selector subquery, a subquery with label matchers, and a subquery over a
// scalar-transformed child.
var subqueryCases = []struct {
	name  string
	query string
}{
	{name: "bare", query: `up[5m:1m]`},
	{name: "labelled", query: `http_requests_total{job="api"}[5m:1m]`},
	{name: "scalar_mul", query: `(up * 2)[10m:30s]`},
	{name: "no_step", query: `up[5m:]`},
}

// TestLowerSubqueryGolden locks in the exact SQL for a subset of
// subqueryCases. Run with -update to regenerate. We lock the bare and
// labelled cases in both render modes.
func TestLowerSubqueryGolden(t *testing.T) {
	goldenCases := subqueryCases[:2] // bare + labelled
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
				goldenPath := filepath.Join("testdata", "subquery_"+tc.name+"_"+mode.name+".sql")
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

// TestLowerSubqueryNoStepGolden locks the rendered SQL for a no-step
// subquery. The range-mode params use a 300s outer step so the golden's
// 60s inner grid proves the step comes from the default evaluation
// interval, not the outer query step (issue #35). Run with -update to
// regenerate.
func TestLowerSubqueryNoStepGolden(t *testing.T) {
	rangeParams := testRenderParamsRange()
	rangeParams.StepMS = 300_000
	for _, mode := range []struct {
		name   string
		params RenderParams
	}{
		{name: "instant", params: testRenderParamsInstant()},
		{name: "range", params: rangeParams},
	} {
		t.Run(mode.name, func(t *testing.T) {
			root, analysis, nativeAnalysis := buildLowerInputs(t, `up[5m:]`)
			rq, err := Lower(LoweringCtx{
				Config:         testRenderConfig(),
				Analysis:       analysis,
				NativeAnalysis: nativeAnalysis,
				Params:         mode.params,
			}, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			// The SQL binds the grid step as a query parameter; the golden
			// text alone cannot lock the value, so assert the bound step is
			// the 1m default evaluation interval (not the 300s outer step).
			if got := rq.QueryParams["param_step_ms"]; got != "60000" {
				t.Fatalf("expected inner grid step param 60000 (default evaluation interval), got %q", got)
			}
			goldenPath := filepath.Join("testdata", "subquery_no_step_"+mode.name+".sql")
			if *updateLowerGolden {
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

// TestLowerSubqueryNilErrors exercises the defensive nil guard in
// lowerSubquery. A nil node must return a non-sentinel error (callers
// should not silently fall back to Fragment for a malformed plan tree).
func TestLowerSubqueryNilErrors(t *testing.T) {
	_, err := lowerSubquery(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil SubqueryPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestSubqueryEnvelopeNoStepUsesDefaultEvaluationInterval locks the
// Prometheus rule for no-step subqueries: the missing step is filled with
// the server-side default evaluation interval, never the outer query step
// (promql/engine.go: SubqueryExpr.Step == 0 -> noStepSubqueryIntervalFn).
func TestSubqueryEnvelopeNoStepUsesDefaultEvaluationInterval(t *testing.T) {
	for _, tc := range []struct {
		name          string
		query         string
		mode          string
		outerStepMS   int64
		defaultStepMS int64
		wantStepMS    int64
	}{
		{name: "range_fallback_1m_not_outer_step", query: `sum(up)[5m:]`, mode: "range", outerStepMS: 300_000, defaultStepMS: 0, wantStepMS: 60_000},
		{name: "range_configured_default", query: `sum(up)[5m:]`, mode: "range", outerStepMS: 300_000, defaultStepMS: 30_000, wantStepMS: 30_000},
		{name: "instant_fallback_1m", query: `sum(up)[5m:]`, mode: "instant", defaultStepMS: 0, wantStepMS: 60_000},
		{name: "instant_configured_default", query: `sum(up)[5m:]`, mode: "instant", defaultStepMS: 30_000, wantStepMS: 30_000},
		{name: "explicit_step_never_overridden", query: `sum(up)[5m:15s]`, mode: "range", outerStepMS: 300_000, defaultStepMS: 30_000, wantStepMS: 15_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subquery := logicalSubqueryForTest(t, tc.query)
			params := testRenderParamsInstant()
			if tc.mode == "range" {
				params = testRenderParamsRange()
				params.StepMS = tc.outerStepMS
			}

			startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(subquery, params, tc.defaultStepMS)
			if err != nil {
				t.Fatalf("subqueryRenderEnvelopeLogical: %v", err)
			}
			if stepMS != tc.wantStepMS {
				t.Fatalf("expected subquery step %d, got %d", tc.wantStepMS, stepMS)
			}
			wantEnd := params.EvaluationTimeMS
			windowStart := wantEnd - subquery.Range.Milliseconds()
			if tc.mode == "range" {
				wantEnd = params.EndMS
				windowStart = params.StartMS - subquery.Range.Milliseconds()
			}
			if endMS != wantEnd {
				t.Fatalf("expected end %d, got %d", wantEnd, endMS)
			}
			wantStart := alignSubqueryStepStart(windowStart, stepMS)
			if startMS != wantStart {
				t.Fatalf("expected start %d, got %d", wantStart, startMS)
			}
		})
	}
}

func TestSubqueryEnvelopeResolvesRangeAnchors(t *testing.T) {
	params := testRenderParamsRange()
	for _, tc := range []struct {
		name    string
		query   string
		wantEnd int64
	}{
		{name: "start", query: `(up * 100)[2m:1m] @ start()`, wantEnd: params.StartMS},
		{name: "end", query: `(up * 100)[2m:1m] @ end()`, wantEnd: params.EndMS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subquery := logicalSubqueryForTest(t, tc.query)
			startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(subquery, params, 0)
			if err != nil {
				t.Fatalf("subqueryRenderEnvelopeLogical: %v", err)
			}
			if stepMS != subquery.Step.Milliseconds() {
				t.Fatalf("expected explicit subquery step %d, got %d", subquery.Step.Milliseconds(), stepMS)
			}
			if endMS != tc.wantEnd {
				t.Fatalf("expected anchored end %d, got %d", tc.wantEnd, endMS)
			}
			wantStart := alignSubqueryStepStart(tc.wantEnd-subquery.Range.Milliseconds(), stepMS)
			if startMS != wantStart {
				t.Fatalf("expected anchored start %d, got %d", wantStart, startMS)
			}
		})
	}
}

func logicalSubqueryForTest(t *testing.T, query string) *logicalpkg.SubqueryPlan {
	t.Helper()
	root, _, _ := buildLowerInputs(t, query)
	subquery, ok := root.(*logicalpkg.SubqueryPlan)
	if !ok {
		t.Fatalf("expected logical subquery root, got %T", root)
	}
	return subquery
}
