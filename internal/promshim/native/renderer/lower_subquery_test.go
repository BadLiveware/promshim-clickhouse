package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
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

func TestSubqueryEnvelopeDefaultsMissingStepToOuterRangeStep(t *testing.T) {
	subquery := logicalSubqueryForTest(t, `sum(up)[5m:]`)
	params := testRenderParamsRange()
	params.StepMS = 30_000

	startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(subquery, params)
	if err != nil {
		t.Fatalf("subqueryRenderEnvelopeLogical: %v", err)
	}
	if stepMS != params.StepMS {
		t.Fatalf("expected subquery step to default to outer step %d, got %d", params.StepMS, stepMS)
	}
	// The outer end (1_700_000_300_000) is 20s past a 30s step multiple;
	// the envelope end is clamped to the grid.
	if wantEnd := int64(1_700_000_280_000); endMS != wantEnd {
		t.Fatalf("expected end %d, got %d", wantEnd, endMS)
	}
	wantStart := alignSubqueryStepStart(params.StartMS-subquery.Range.Milliseconds(), params.StepMS)
	if startMS != wantStart {
		t.Fatalf("expected start %d, got %d", wantStart, startMS)
	}
}

func TestSubqueryEnvelopeResolvesRangeAnchors(t *testing.T) {
	params := testRenderParamsRange()
	// The outer bounds (1_700_000_000_000 / 1_700_000_300_000) are both
	// 20s past a 1m step multiple; the anchored envelope ends are clamped
	// to the last absolute step multiple at or before the anchor.
	for _, tc := range []struct {
		name    string
		query   string
		wantEnd int64
	}{
		{name: "start", query: `(up * 100)[2m:1m] @ start()`, wantEnd: 1_699_999_980_000},
		{name: "end", query: `(up * 100)[2m:1m] @ end()`, wantEnd: 1_700_000_280_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subquery := logicalSubqueryForTest(t, tc.query)
			startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(subquery, params)
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

// chRangePoints mirrors ClickHouse's range(start, stop, step): the
// arithmetic progression start, start+step, ... strictly below stop.
// Used to assert the exact evaluation-point set the rendered grid
// expressions emit, instead of only the envelope bounds.
func chRangePoints(startMS, stopMS, stepMS int64) []int64 {
	points := []int64{}
	for ts := startMS; ts < stopMS; ts += stepMS {
		points = append(points, ts)
	}
	return points
}

// TestSubqueryGridEmitsOnlyAbsoluteStepMultiples pins the exact point set
// the two grid SQL shapes emit for subquery envelopes whose raw end
// (t - offset) is NOT an absolute multiple of the subquery step.
// Prometheus's subquery grid contains only absolute multiples of step:
// the last point is floor((t-offset)/step)*step, never the unaligned
// t-offset itself and never a multiple beyond it (promql/engine.go,
// SubqueryExpr case: aligned start, floor-semantics end). Expected point
// sets below were verified against the real engine via promqltest
// (prometheus v0.311.2).
//
// The leaf-selector grid renders range(start, end + step, step)
// (emit.GridEvalTSParams); with an unaligned end that stop bound
// overshoots by one full step, emitting an extra evaluation point past
// t-offset — the defect this test locks out. The literal grid form
// (emit.GridEvalTSLiteral) uses stop = end + 1 and is exercised for the
// same envelopes.
func TestSubqueryGridEmitsOnlyAbsoluteStepMultiples(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		params     RenderParams
		wantStart  int64
		wantEnd    int64
		wantPoints []int64
	}{
		// count_over_time(up[15m:5m]) probe shape: instant t with
		// t % 300s == 60s. Prometheus: 3 points, promqltest-verified.
		{
			name:       "15m_5m_unaligned_instant",
			query:      `up[15m:5m]`,
			params:     RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 3_660_000},
			wantStart:  3_000_000,
			wantEnd:    3_600_000,
			wantPoints: []int64{3_000_000, 3_300_000, 3_600_000},
		},
		// Offset shifts the window end off-grid too: end = t - 90s.
		{
			name:       "15m_5m_offset_90s_unaligned_instant",
			query:      `up[15m:5m] offset 90s`,
			params:     RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 3_660_000},
			wantStart:  2_700_000,
			wantEnd:    3_300_000,
			wantPoints: []int64{2_700_000, 3_000_000, 3_300_000},
		},
		// @ modifier with an unaligned anchor (1810s, step 5m).
		{
			name:       "15m_5m_at_1810_instant",
			query:      `up[15m:5m] @ 1810`,
			params:     RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 3_600_000},
			wantStart:  1_200_000,
			wantEnd:    1_800_000,
			wantPoints: []int64{1_200_000, 1_500_000, 1_800_000},
		},
		// Step larger than window at unaligned t: exactly one aligned point.
		{
			name:       "2m_5m_step_gt_window_unaligned_instant",
			query:      `up[2m:5m]`,
			params:     RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 3_660_000},
			wantStart:  3_600_000,
			wantEnd:    3_600_000,
			wantPoints: []int64{3_600_000},
		},
		// Step larger than window, no aligned point inside: empty grid.
		{
			name:       "1m_5m_empty_window_unaligned_instant",
			query:      `up[1m:5m]`,
			params:     RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 3_450_000},
			wantStart:  3_600_000,
			wantEnd:    3_300_000,
			wantPoints: []int64{},
		},
		// Range mode with an @-anchored, unaligned inner end. The inner
		// grid is fixed; an overshoot point here is NOT masked by the
		// outer per-step window filter once eval_ts passes it.
		{
			name:       "15m_5m_at_1810_range",
			query:      `up[15m:5m] @ 1810`,
			params:     RenderParams{Mode: native.RenderModeRange, StartMS: 3_600_000, EndMS: 3_720_000, StepMS: 60_000},
			wantStart:  1_200_000,
			wantEnd:    1_800_000,
			wantPoints: []int64{1_200_000, 1_500_000, 1_800_000},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subquery := logicalSubqueryForTest(t, tc.query)
			startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(subquery, tc.params)
			if err != nil {
				t.Fatalf("subqueryRenderEnvelopeLogical: %v", err)
			}
			if startMS != tc.wantStart {
				t.Fatalf("expected envelope start %d, got %d", tc.wantStart, startMS)
			}
			if endMS != tc.wantEnd {
				t.Fatalf("expected envelope end %d (last absolute step multiple), got %d", tc.wantEnd, endMS)
			}
			// Leaf-selector grid shape: range(start, end + step, step).
			if got := chRangePoints(startMS, endMS+stepMS, stepMS); !slices.Equal(got, tc.wantPoints) {
				t.Fatalf("params grid range(start, end+step, step) emits %v, want %v", got, tc.wantPoints)
			}
			// Literal grid shape: range(start, end + 1, step).
			if got := chRangePoints(startMS, endMS+1, stepMS); !slices.Equal(got, tc.wantPoints) {
				t.Fatalf("literal grid range(start, end+1, step) emits %v, want %v", got, tc.wantPoints)
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

// TestAlignSubqueryStepStart pins the Prometheus 3.x subquery grid start:
// the first absolute multiple of step STRICTLY after the window start
// (left-open interval (t-range, t], promql/engine.go SubqueryExpr case).
func TestAlignSubqueryStepStart(t *testing.T) {
	for _, tc := range []struct {
		name          string
		windowStartMS int64
		stepMS        int64
		want          int64
	}{
		{name: "aligned_boundary_excluded", windowStartMS: 2_700_000, stepMS: 60_000, want: 2_760_000},
		{name: "unaligned_rounds_up", windowStartMS: 2_701_000, stepMS: 60_000, want: 2_760_000},
		{name: "just_below_multiple", windowStartMS: 2_759_999, stepMS: 60_000, want: 2_760_000},
		{name: "zero_boundary_excluded", windowStartMS: 0, stepMS: 60_000, want: 60_000},
		{name: "negative_unaligned", windowStartMS: -90_000, stepMS: 60_000, want: -60_000},
		{name: "negative_aligned_boundary_excluded", windowStartMS: -120_000, stepMS: 60_000, want: -60_000},
		{name: "non_positive_step_passthrough", windowStartMS: 123, stepMS: 0, want: 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := alignSubqueryStepStart(tc.windowStartMS, tc.stepMS); got != tc.want {
				t.Fatalf("alignSubqueryStepStart(%d, %d) = %d, want %d", tc.windowStartMS, tc.stepMS, got, tc.want)
			}
		})
	}
}

// TestSubqueryEnvelopeLeftOpenWindow pins the instant-mode subquery
// envelope against Prometheus semantics with absolute expected values
// (not recomputed through the same alignment helper). The evaluation
// time 3_600_000ms is aligned to every step used below, so the window
// start boundary sits exactly on a grid point and must be excluded.
func TestSubqueryEnvelopeLeftOpenWindow(t *testing.T) {
	const alignedEvalMS = 3_600_000 // 1h, multiple of 1m and 5m
	for _, tc := range []struct {
		name       string
		query      string
		evalTimeMS int64
		wantStart  int64
		wantEnd    int64
		wantPoints int64
	}{
		// Reproducer shapes from issue #33: counts must match Prometheus.
		{name: "15m_1m_aligned", query: `up[15m:1m]`, evalTimeMS: alignedEvalMS, wantStart: 2_760_000, wantEnd: alignedEvalMS, wantPoints: 15},
		{name: "15m_5m_aligned", query: `up[15m:5m]`, evalTimeMS: alignedEvalMS, wantStart: 3_000_000, wantEnd: alignedEvalMS, wantPoints: 3},
		{name: "1h_5m_aligned", query: `up[1h:5m]`, evalTimeMS: alignedEvalMS, wantStart: 300_000, wantEnd: alignedEvalMS, wantPoints: 12},
		// Window not a multiple of step: boundary is off-grid, ceil applies.
		{name: "16m_5m_aligned", query: `up[16m:5m]`, evalTimeMS: alignedEvalMS, wantStart: 2_700_000, wantEnd: alignedEvalMS, wantPoints: 4},
		// Evaluation time not step-aligned: the envelope end is clamped
		// to the last absolute step multiple <= t (the previous
		// expectation pinned the raw unaligned t as the envelope end,
		// which made the rendered range(start, end+step, step) grid emit
		// an extra point past t — issue #33 defect 2).
		{name: "15m_5m_unaligned_eval", query: `up[15m:5m]`, evalTimeMS: alignedEvalMS + 10_000, wantStart: 3_000_000, wantEnd: alignedEvalMS, wantPoints: 3},
		// Step larger than window: only the aligned end point survives.
		{name: "1m_5m_step_gt_window", query: `up[1m:5m]`, evalTimeMS: alignedEvalMS, wantStart: 3_600_000, wantEnd: alignedEvalMS, wantPoints: 1},
		// Step larger than window and no aligned point inside: empty grid
		// (end is clamped down to the last step multiple, below start).
		{name: "1m_5m_empty_window", query: `up[1m:5m]`, evalTimeMS: alignedEvalMS - 150_000, wantStart: 3_600_000, wantEnd: 3_300_000, wantPoints: 0},
		// Offset shifts the window before alignment.
		{name: "15m_1m_offset_5m", query: `up[15m:1m] offset 5m`, evalTimeMS: alignedEvalMS, wantStart: 2_460_000, wantEnd: 3_300_000, wantPoints: 15},
		// @ modifier anchors the window at the fixed timestamp.
		{name: "15m_1m_at_1800", query: `up[15m:1m] @ 1800`, evalTimeMS: alignedEvalMS, wantStart: 960_000, wantEnd: 1_800_000, wantPoints: 15},
		{name: "15m_1m_at_1800_offset_5m", query: `up[15m:1m] @ 1800 offset 5m`, evalTimeMS: alignedEvalMS, wantStart: 660_000, wantEnd: 1_500_000, wantPoints: 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subquery := logicalSubqueryForTest(t, tc.query)
			params := RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: tc.evalTimeMS}
			startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(subquery, params)
			if err != nil {
				t.Fatalf("subqueryRenderEnvelopeLogical: %v", err)
			}
			if startMS != tc.wantStart {
				t.Fatalf("expected start %d, got %d", tc.wantStart, startMS)
			}
			if endMS != tc.wantEnd {
				t.Fatalf("expected end %d, got %d", tc.wantEnd, endMS)
			}
			gotPoints := int64(0)
			if endMS >= startMS {
				gotPoints = (endMS-startMS)/stepMS + 1
			}
			if gotPoints != tc.wantPoints {
				t.Fatalf("expected %d grid points, got %d (start=%d end=%d step=%d)", tc.wantPoints, gotPoints, startMS, endMS, stepMS)
			}
		})
	}
}

// TestSubqueryEnvelopeRangeModeExcludesBoundary pins the range-mode
// envelope: the grid must start strictly after outer.start - range.
func TestSubqueryEnvelopeRangeModeExcludesBoundary(t *testing.T) {
	subquery := logicalSubqueryForTest(t, `up[15m:1m]`)
	params := RenderParams{Mode: native.RenderModeRange, StartMS: 3_600_000, EndMS: 3_900_000, StepMS: 60_000}
	startMS, endMS, _, err := subqueryRenderEnvelopeLogical(subquery, params)
	if err != nil {
		t.Fatalf("subqueryRenderEnvelopeLogical: %v", err)
	}
	// outer.start - range = 2_700_000 is minute-aligned: excluded.
	if startMS != 2_760_000 {
		t.Fatalf("expected envelope start 2760000, got %d", startMS)
	}
	if endMS != params.EndMS {
		t.Fatalf("expected envelope end %d, got %d", params.EndMS, endMS)
	}
}
