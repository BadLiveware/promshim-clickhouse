package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
)

// rangeFunctionCases covers all seven logical range-function plan kinds.
// The first five canonical shapes are locked by golden files.
var rangeFunctionCases = []struct {
	name  string
	query string
}{
	// — RatePlan —
	{name: "rate_5m", query: `rate(http_requests_total[5m])`},
	// — IncreasePlan —
	{name: "increase_1h", query: `increase(http_requests_total[1h])`},
	// — DeltaPlan —
	{name: "delta_10m", query: `delta(cpu_usage[10m])`},
	// — DerivPlan —
	{name: "deriv_10m", query: `deriv(cpu_usage[10m])`},
	// — ChangesPlan —
	{name: "changes_1h", query: `changes(process_starts[1h])`},
	// — RangeFunctionPlan (avg_over_time) —
	{name: "avg_over_time_5m", query: `avg_over_time(up[5m])`},
	// — RangeFunctionPlan (sum_over_time) — instant mode is primary
	{name: "sum_over_time_5m", query: `sum_over_time(up[5m])`},
	// — RangeFunctionPlan (max_over_time) —
	{name: "max_over_time_5m", query: `max_over_time(up[5m])`},
	// — QuantileOverTimePlan —
	{name: "quantile_over_time_095_5m", query: `quantile_over_time(0.95, request_duration[5m])`},
}

// TestLowerRangeFunctionGolden locks in the exact SQL for the first five
// canonical shapes (rate, increase, delta, deriv, changes) in both render
// modes. Run with -update to regenerate golden files.
func TestLowerRangeFunctionGolden(t *testing.T) {
	goldenCases := rangeFunctionCases[:5] // rate, increase, delta, deriv, changes
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
				goldenPath := filepath.Join("testdata", "lower_range_function", tc.name+"_"+mode.name+".sql")
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

func TestLowerLongInstantRateUsesGuardedDirectAggregate(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[5m])`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "deltaSumTimestamp(") {
		t.Fatalf("expected guarded instant rate aggregate SQL to contain deltaSumTimestamp, got %s", rq.SQL)
	}
	if strings.Contains(rq.SQL, "lagInFrame(") {
		t.Fatalf("expected guarded instant rate aggregate to avoid lagInFrame, got %s", rq.SQL)
	}
}

func TestLowerLongStepRateRangeUsesExtrapolatedWindowJoin(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[5m])`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:    native.RenderModeRange,
			StartMS: 1_700_000_000_000,
			EndMS:   1_700_086_400_000,
			StepMS:  300_000,
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{"arraySort(groupArray((d.timestamp, d.value))) AS window_series", "arrayMap(point -> tupleElement(point, 1), window_series) AS window_timestamps", "counter_delta_sum) * (if(", ") / (300)"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected extrapolated rate window SQL to contain %q, got %s", expected, rq.SQL)
		}
	}
	if strings.Contains(rq.SQL, "deltaSumTimestamp(") {
		t.Fatalf("expected range rate to avoid non-extrapolated direct aggregate, got %s", rq.SQL)
	}
}

func TestLowerLongStepRateRangeUsesNativeGridWhenEnabled(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[5m])`)
	cfg := testRenderConfig()
	cfg.EnableNativeGridFunctions = true
	rq, err := Lower(LoweringCtx{
		Config:         cfg,
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:    native.RenderModeRange,
			StartMS: 1_700_000_000_000,
			EndMS:   1_700_086_400_000,
			StepMS:  300_000,
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{"timeSeriesRateToGrid(", "arrayZip(arrayMap", "arrayFilter(point -> isNotNull(point.2)", "arrayMap(point -> (point.1, toFloat64(assumeNotNull(point.2)))"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected native-grid rate SQL to contain %q, got %s", expected, rq.SQL)
		}
	}
	for _, unexpected := range []string{"deltaSumTimestamp(", "ARRAY JOIN", "groupArray((timestamp, value))"} {
		if strings.Contains(rq.SQL, unexpected) {
			t.Fatalf("expected native-grid direct matrix SQL to avoid %q, got %s", unexpected, rq.SQL)
		}
	}
}

func TestLowerRangeFunctionsUseNativeGridWhenEnabled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		chFunction string
	}{
		{name: "high_overlap_rate", query: `rate(demo_cpu_usage_seconds_total[5m])`, chFunction: "timeSeriesRateToGrid("},
		{name: "irate", query: `irate(demo_cpu_usage_seconds_total[5m])`, chFunction: "timeSeriesInstantRateToGrid("},
		{name: "delta", query: `delta(demo_cpu_usage_seconds_total[10m])`, chFunction: "timeSeriesDeltaToGrid("},
		{name: "idelta", query: `idelta(demo_cpu_usage_seconds_total[10m])`, chFunction: "timeSeriesInstantDeltaToGrid("},
		{name: "last_over_time", query: `last_over_time(demo_cpu_usage_seconds_total[5m])`, chFunction: "timeSeriesLastToGrid("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
			cfg := testRenderConfig()
			cfg.EnableNativeGridFunctions = true
			rq, err := Lower(LoweringCtx{
				Config:         cfg,
				Analysis:       analysis,
				NativeAnalysis: nativeAnalysis,
				Params: RenderParams{
					Mode:    native.RenderModeRange,
					StartMS: 1_700_000_000_000,
					EndMS:   1_700_003_600_000,
					StepMS:  30_000,
				},
			}, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			for _, expected := range []string{tc.chFunction, "arrayZip(arrayMap", "arrayFilter(point -> isNotNull(point.2)"} {
				if !strings.Contains(rq.SQL, expected) {
					t.Fatalf("expected native-grid SQL to contain %q, got %s", expected, rq.SQL)
				}
			}
			for _, unexpected := range []string{"deltaSumTimestamp(", "arraySort(groupArray((d.timestamp, d.value))) AS window_series"} {
				if strings.Contains(rq.SQL, unexpected) {
					t.Fatalf("expected native-grid SQL to avoid %q, got %s", unexpected, rq.SQL)
				}
			}
			if tc.name == "last_over_time" && strings.Contains(rq.SQL, "arrayFilter(tag -> tag.1 != '__name__'") {
				t.Fatalf("last_over_time must preserve metric name before downstream operators, got %s", rq.SQL)
			}
		})
	}
}

func TestLowerHighOverlapAvgOverTimeRangeUsesDirectAggregate(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `avg_over_time(demo_memory_usage_bytes[1h])`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:    native.RenderModeRange,
			StartMS: 1_700_000_000_000,
			EndMS:   1_700_086_400_000,
			StepMS:  60_000,
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "range_window_aggregate")
	if !ok || decision.Strategy != string(physical.RangeWindowAggregateStrategyCumulativeAvg) {
		t.Fatalf("expected cumulative_avg physical decision, got %#v", rq.PhysicalDecisions)
	}
	for _, expected := range []string{"sum(if(NOT isNaN(ifNull(toFloat64(d.value), nan))", "AS finite_sum", "ASOF LEFT JOIN", "ARRAY JOIN [(1, upper_bound), (0, lower_prev_bound)] AS boundary", "maxIf(finite_sum, boundary_kind = 1) - maxIf(finite_sum, boundary_kind = 0) AS finite_sum", "finite_sum / finite_count"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected high-overlap avg_over_time cumulative aggregate SQL to contain %q, got %s", expected, rq.SQL)
		}
	}
	for _, unexpected := range []string{"arraySort(groupArray((d.timestamp, d.value))) AS window_series", "window_values", "CROSS JOIN", "avgIf("} {
		if strings.Contains(rq.SQL, unexpected) {
			t.Fatalf("expected high-overlap avg_over_time cumulative aggregate to avoid %q, got %s", unexpected, rq.SQL)
		}
	}
}

func TestLowerShortRateRangeUsesSortedWindowSeries(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[15s])`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:    native.RenderModeRange,
			StartMS: 1_776_807_342_000,
			EndMS:   1_776_807_942_000,
			StepMS:  10_000,
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if strings.Contains(rq.SQL, "deltaSumTimestamp(") {
		t.Fatalf("short rate ranges must not use direct deltaSumTimestamp aggregate, got %s", rq.SQL)
	}
	for _, expected := range []string{"arraySort(groupArray((d.timestamp, d.value))) AS window_series", "arrayPopBack(window_values) AS window_values_prev", "arrayPopFront(window_values) AS window_values_cur"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected %q in SQL, got %s", expected, rq.SQL)
		}
	}
}

// TestLowerRangeFunctionNilErrors exercises the defensive nil guard in
// lowerRangeFunction. A nil node must return a non-sentinel error (callers
// should not silently fall back to Fragment for a malformed plan tree).
func TestLowerRangeFunctionNilErrors(t *testing.T) {
	_, err := lowerRangeFunction(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

// TestLowerRangeFunctionOverSubqueryUsesLeftOpenWindow verifies that
// range-mode windows over subquery-derived sources are left-open
// (t-range, t] like Prometheus 3.x, while raw-sample selector windows
// keep the legacy closed lower bound. Subquery grid points sit exactly
// on step-aligned timestamps, so a closed lower bound would double-count
// the boundary evaluation (issue #33: subquery off-by-one).
func TestLowerRangeFunctionOverSubqueryUsesLeftOpenWindow(t *testing.T) {
	params := RenderParams{Mode: native.RenderModeRange, StartMS: 3_600_000, EndMS: 3_900_000, StepMS: 60_000}

	t.Run("subquery_child_left_open", func(t *testing.T) {
		root, analysis, nativeAnalysis := buildLowerInputs(t, `count_over_time(up[15m:1m])`)
		rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: params}, root)
		if err != nil {
			t.Fatalf("Lower: %v", err)
		}
		if !strings.Contains(rq.SQL, "tupleElement(point, 1) > grid.eval_ts - toIntervalMillisecond(900000)") {
			t.Fatalf("expected left-open subquery window lower bound, got %s", rq.SQL)
		}
		if strings.Contains(rq.SQL, "tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(900000)") {
			t.Fatalf("subquery window lower bound must not be closed, got %s", rq.SQL)
		}
	})

	t.Run("raw_selector_child_stays_closed", func(t *testing.T) {
		root, analysis, nativeAnalysis := buildLowerInputs(t, `deriv(cpu_usage[10m])`)
		rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: params}, root)
		if err != nil {
			t.Fatalf("Lower: %v", err)
		}
		if !strings.Contains(rq.SQL, "tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(600000)") {
			t.Fatalf("expected closed raw-sample window lower bound, got %s", rq.SQL)
		}
	})
}

// TestLowerInstantRangeFunctionOverSubqueryEnvelopeExcludesBoundary
// verifies end to end that instant-mode lowering of a range function over
// a subquery carves the child grid strictly after t-range: at a
// minute-aligned evaluation time the [15m:1m] envelope must contain
// exactly 15 evaluation points, matching Prometheus.
func TestLowerInstantRangeFunctionOverSubqueryEnvelopeExcludesBoundary(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `count_over_time(up[15m:1m])`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 3_600_000},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got, want := rq.QueryParams["param_start_ms"], "2760000"; got != want {
		t.Fatalf("expected subquery grid start %q (strictly after t-range), got %q", want, got)
	}
	if got, want := rq.QueryParams["param_end_ms"], "3600000"; got != want {
		t.Fatalf("expected subquery grid end %q, got %q", want, got)
	}
	if got, want := rq.QueryParams["param_step_ms"], "60000"; got != want {
		t.Fatalf("expected subquery grid step %q, got %q", want, got)
	}
}
