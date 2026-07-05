package renderer

// Regression tests for issue #36: rate/increase/delta with offset (or @)
// fetched the correct sample window but anchored the Prometheus
// extrapolation factor at the raw evaluation time instead of
// eval - offset. Prometheus's extrapolatedRate (promql/functions.go)
// uses rangeStart = t-(offset+range) and rangeEnd = t-offset; every
// native-SQL emission site must anchor the factor the same way.
//
// Each test pins the anchor literal at one emission site:
//   - instant rows fast path            (buildInstantRateOverRowsSQL)
//   - instant fallback                  (buildInstantRangeFunctionSQL)
//   - instant fallback, subquery child  (buildInstantRangeFunctionSQL)
//   - range windowed arrays             (buildRangeFunctionOverWindowedSourceSQL)
//   - range window join                 (renderRangeFunctionLogicalBody)
//   - range subquery child              (buildRangeFunctionOverWindowedSourceSQL)
//   - fused aggregation windowed rows   (buildRangeFunctionOverWindowedArraysRowsSQL)
//   - fused aggregation window join     (renderRangeFunctionRowsLogicalSQL)
// plus @-modifier variants of the instant paths.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
)

const (
	offsetTestEvalMS   = int64(1_700_000_000_000)
	offsetTestRangeMS  = int64(300_000)   // 5m
	offsetTestOffsetMS = int64(1_800_000) // 30m
)

// Instant-mode anchor literals: the factor's rangeStart is rendered as
// ((<anchor>) - <range>.0).
const (
	instantShiftedAnchor   = "((1699998200000) - 300000.0)" // eval - 30m
	instantUnshiftedAnchor = "((1700000000000) - 300000.0)" // raw eval (the bug)
)

// Range-mode anchor expression: the per-step grid eval_ts shifted by the
// selector/subquery offset.
const rangeShiftedAnchor = "(toFloat64(toUnixTimestamp64Milli(eval_ts)) - 1800000.0)"

func offsetInstantRenderParams() RenderParams {
	return RenderParams{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
		// Production (LogicalRequiredInputBounds) computes:
		// end = eval - offset, start = end - range.
		RequiredStartMS: offsetTestEvalMS - offsetTestOffsetMS - offsetTestRangeMS,
		RequiredEndMS:   offsetTestEvalMS - offsetTestOffsetMS,
	}
}

func lowerForTest(t *testing.T, query string, params RenderParams) RenderedQuery {
	t.Helper()
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         params,
	}, root)
	if err != nil {
		t.Fatalf("Lower(%s): %v", query, err)
	}
	return rq
}

func assertAnchor(t *testing.T, sql, wantAnchor, buggyAnchor string) {
	t.Helper()
	if !strings.Contains(sql, wantAnchor) {
		t.Errorf("rendered SQL is missing the offset-shifted extrapolation anchor %q\nSQL:\n%s", wantAnchor, sql)
	}
	if buggyAnchor != "" && strings.Contains(sql, buggyAnchor) {
		t.Errorf("rendered SQL still contains the unshifted extrapolation anchor %q", buggyAnchor)
	}
}

// --- Instant mode ---

func TestInstantRateOffsetAnchor_RowsFastPath(t *testing.T) {
	rq := lowerForTest(t, `rate(http_requests_total[5m] offset 30m)`, offsetInstantRenderParams())
	if !strings.Contains(rq.SQL, "deltaSumTimestamp(") {
		t.Fatalf("expected the instant rows fast path (deltaSumTimestamp), got:\n%s", rq.SQL)
	}
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
	// Output timestamp stays at the raw evaluation time.
	if !strings.Contains(rq.SQL, "fromUnixTimestamp64Milli(1700000000000)") {
		t.Errorf("expected output timestamp anchored at the evaluation time")
	}
}

func TestInstantIncreaseOffsetAnchor_Fallback(t *testing.T) {
	rq := lowerForTest(t, `increase(http_requests_total[5m] offset 30m)`, offsetInstantRenderParams())
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
}

func TestInstantDeltaOffsetAnchor_Fallback(t *testing.T) {
	rq := lowerForTest(t, `delta(http_requests_total[5m] offset 30m)`, offsetInstantRenderParams())
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
}

func TestInstantRateSubqueryOffsetAnchor_Fallback(t *testing.T) {
	// Subquery child: the extrapolation anchor uses the *subquery's*
	// offset (Prometheus's evalSubquery synthesizes a MatrixSelector
	// carrying the subquery's offset and timestamp). The subquery's
	// inner selector has no offset, so production required bounds stay
	// anchored at the evaluation time.
	query := `rate(http_requests_total[5m:15s] offset 30m)`
	root, _, _ := buildLowerInputs(t, query)
	startMS, endMS, ok := LogicalRequiredInputBounds(root, native.OptimizationContext{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
	})
	if !ok {
		t.Fatalf("LogicalRequiredInputBounds returned !ok")
	}
	params := RenderParams{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
		RequiredStartMS:  startMS,
		RequiredEndMS:    endMS,
	}
	rq := lowerForTest(t, query, params)
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
}

// subqueryInstantParams derives the production required bounds for a
// subquery query and returns instant RenderParams carrying them, mirroring
// TestInstantRateSubqueryOffsetAnchor_Fallback.
func subqueryInstantParams(t *testing.T, query string) RenderParams {
	t.Helper()
	root, _, _ := buildLowerInputs(t, query)
	startMS, endMS, ok := LogicalRequiredInputBounds(root, native.OptimizationContext{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
	})
	if !ok {
		t.Fatalf("LogicalRequiredInputBounds returned !ok for %q", query)
	}
	return RenderParams{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
		RequiredStartMS:  startMS,
		RequiredEndMS:    endMS,
	}
}

func TestInstantRateSubqueryAtModifierAnchor(t *testing.T) {
	// Subquery child with a literal @ (range_logical.go: sub.Timestamp != nil):
	// the extrapolation anchor is the subquery's @ time (1699998200000), not
	// the outer evaluation time. Matches instantShiftedAnchor (== @ time).
	query := `rate(http_requests_total[5m:15s] @ 1699998200)`
	rq := lowerForTest(t, query, subqueryInstantParams(t, query))
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
}

func TestInstantRateSubqueryAtModifierWithOffsetAnchor(t *testing.T) {
	// Subquery @ combined with the subquery's own offset: the effective
	// anchor is @ts - offset = 1699998200000 - 1800000 = 1699996400000
	// (Prometheus's evalSubquery carries both the subquery @ and offset).
	query := `rate(http_requests_total[5m:15s] @ 1699998200 offset 30m)`
	rq := lowerForTest(t, query, subqueryInstantParams(t, query))
	const wantAnchor = "((1699996400000) - 300000.0)" // @ts - 30m
	assertAnchor(t, rq.SQL, wantAnchor, instantUnshiftedAnchor)
}

func TestInstantRateSubqueryAtStartEndAnchor(t *testing.T) {
	// Subquery @ start()/@ end() (range_logical.go: resolveSubqueryStartEndMS):
	// in instant mode both resolve to the evaluation time, so the anchor is
	// the raw eval time (== instantUnshiftedAnchor here, which is correct for
	// this shape rather than the bug).
	for _, query := range []string{
		`rate(http_requests_total[5m:15s] @ start())`,
		`rate(http_requests_total[5m:15s] @ end())`,
	} {
		t.Run(query, func(t *testing.T) {
			rq := lowerForTest(t, query, subqueryInstantParams(t, query))
			assertAnchor(t, rq.SQL, instantUnshiftedAnchor, "")
		})
	}
}

func TestInstantRateAtModifierAnchor(t *testing.T) {
	// rate(x[5m] @ 1699998200) evaluated at 1700000000: the anchor is
	// the @ time, not the outer evaluation time.
	query := `rate(http_requests_total[5m] @ 1699998200)`
	root, _, _ := buildLowerInputs(t, query)
	startMS, endMS, ok := LogicalRequiredInputBounds(root, native.OptimizationContext{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
	})
	if !ok {
		t.Fatalf("LogicalRequiredInputBounds returned !ok")
	}
	if endMS != 1_699_998_200_000 || startMS != 1_699_997_900_000 {
		t.Fatalf("expected @-anchored bounds [1699997900000, 1699998200000], got [%d, %d]", startMS, endMS)
	}
	params := RenderParams{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
		RequiredStartMS:  startMS,
		RequiredEndMS:    endMS,
	}
	rq := lowerForTest(t, query, params)
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
}

func TestInstantIncreaseAtModifierAnchor_Fallback(t *testing.T) {
	query := `increase(http_requests_total[5m] @ 1699998200)`
	root, _, _ := buildLowerInputs(t, query)
	startMS, endMS, ok := LogicalRequiredInputBounds(root, native.OptimizationContext{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
	})
	if !ok {
		t.Fatalf("LogicalRequiredInputBounds returned !ok")
	}
	params := RenderParams{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
		RequiredStartMS:  startMS,
		RequiredEndMS:    endMS,
	}
	rq := lowerForTest(t, query, params)
	assertAnchor(t, rq.SQL, instantShiftedAnchor, instantUnshiftedAnchor)
}

// --- Range mode ---

func rangeParamsWithStep(stepMS int64) RenderParams {
	return RenderParams{
		Mode:    native.RenderModeRange,
		StartMS: offsetTestEvalMS,
		EndMS:   offsetTestEvalMS + 600_000,
		StepMS:  stepMS,
		// Production widens by offset+range.
		RequiredStartMS: offsetTestEvalMS - offsetTestOffsetMS - offsetTestRangeMS,
		RequiredEndMS:   offsetTestEvalMS + 600_000 - offsetTestOffsetMS,
	}
}

func TestRangeRateOffsetAnchor_WindowedArrays(t *testing.T) {
	// step 1m over a 5m lookback: high overlap, falls back to the
	// windowed-arrays catch-all.
	rq := lowerForTest(t, `rate(http_requests_total[5m] offset 30m)`, rangeParamsWithStep(60_000))
	if !strings.Contains(rq.SQL, "window_series") {
		t.Fatalf("expected windowed-arrays SQL, got:\n%s", rq.SQL)
	}
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
	// The window predicate stays offset-shifted (already correct pre-fix).
	if !strings.Contains(rq.SQL, "toIntervalMillisecond(1800000)") || !strings.Contains(rq.SQL, "toIntervalMillisecond(2100000)") {
		t.Errorf("expected offset-shifted window filter intervals 1800000/2100000")
	}
}

func TestRangeIncreaseOffsetAnchor_WindowedArrays(t *testing.T) {
	rq := lowerForTest(t, `increase(http_requests_total[5m] offset 30m)`, rangeParamsWithStep(60_000))
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
}

func TestRangeDeltaOffsetAnchor_WindowedArrays(t *testing.T) {
	rq := lowerForTest(t, `delta(http_requests_total[5m] offset 30m)`, rangeParamsWithStep(60_000))
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
}

func TestRangeRateOffsetAnchor_WindowJoin(t *testing.T) {
	// step == lookback: low overlap, eligible for the direct window join.
	rq := lowerForTest(t, `rate(http_requests_total[5m] offset 30m)`, rangeParamsWithStep(300_000))
	if !strings.Contains(rq.SQL, "timeSeriesData") || strings.Contains(rq.SQL, "arrayFilter(point ->") {
		t.Fatalf("expected direct window-join SQL, got:\n%s", rq.SQL)
	}
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
}

func TestRangeRateSubqueryOffsetAnchor(t *testing.T) {
	rq := lowerForTest(t, `rate(http_requests_total[5m:15s] offset 30m)`, rangeParamsWithStep(60_000))
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
}

func TestFusedSumRateOffsetAnchor_WindowedRows(t *testing.T) {
	rq := lowerForTest(t, `sum(rate(http_requests_total[5m] offset 30m))`, rangeParamsWithStep(60_000))
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
}

func TestFusedSumRateOffsetAnchor_WindowJoin(t *testing.T) {
	rq := lowerForTest(t, `sum(rate(http_requests_total[5m] offset 30m))`, rangeParamsWithStep(300_000))
	assertAnchor(t, rq.SQL, rangeShiftedAnchor, "")
}

// TestLowerRangeFunctionOffsetGolden locks the exact SQL for the
// offset-carrying rate shape in both render modes so future renderer work
// cannot silently drop the anchor shift. Run with -update to regenerate.
func TestLowerRangeFunctionOffsetGolden(t *testing.T) {
	for _, mode := range []struct {
		name   string
		params RenderParams
	}{
		{name: "instant", params: offsetInstantRenderParams()},
		{name: "range", params: rangeParamsWithStep(60_000)},
	} {
		t.Run(mode.name, func(t *testing.T) {
			rq := lowerForTest(t, `rate(http_requests_total[5m] offset 30m)`, mode.params)
			goldenPath := filepath.Join("testdata", "lower_range_function", "rate_5m_offset30m_"+mode.name+".sql")
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

// --- Zero-offset stability ---

// TestZeroOffsetRateSQLHasNoAnchorShift locks the conditional emission:
// zero-offset queries must keep the historical anchor expressions so the
// rendered SQL (and the goldens) stay byte-identical.
func TestZeroOffsetRateSQLHasNoAnchorShift(t *testing.T) {
	rq := lowerForTest(t, `rate(http_requests_total[5m])`, rangeParamsWithStep(60_000))
	if strings.Contains(rq.SQL, "toFloat64(toUnixTimestamp64Milli(eval_ts)) - ") {
		t.Errorf("zero-offset range SQL must not carry an anchor shift:\n%s", rq.SQL)
	}
	instant := lowerForTest(t, `rate(http_requests_total[5m])`, RenderParams{
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: offsetTestEvalMS,
		RequiredStartMS:  offsetTestEvalMS - offsetTestRangeMS,
		RequiredEndMS:    offsetTestEvalMS,
	})
	if !strings.Contains(instant.SQL, instantUnshiftedAnchor) {
		t.Errorf("zero-offset instant SQL must keep the evaluation-time anchor %q", instantUnshiftedAnchor)
	}
}
