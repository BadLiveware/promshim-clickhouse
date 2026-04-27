package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// aggregationCases covers the full spread of aggregation shapes:
//   - simple ops without grouping (sum, count)
//   - ops with BY grouping (sum by, avg without)
//   - selection aggregations (topk, bottomk, quantile)
//   - count_values (synthesizes a new label dimension)
//   - aggregation-range-fused paths (direct reducers over rate/range functions)
//
// Golden files lock a representative subset of SQL outputs from Lower
// (first 5 canonical + fused direct-reducer cases).
var aggregationCases = []struct {
	name  string
	query string
}{
	// — simple ops, no grouping —
	{name: "sum_up_instant", query: `sum(up)`},
	{name: "count_up_instant", query: `count(up)`},
	// — BY grouping —
	{name: "sum_by_job_up", query: `sum by (job) (up)`},
	// — WITHOUT grouping —
	{name: "avg_without_instance_up", query: `avg without (instance) (up)`},
	// — selection aggregation —
	{name: "topk_3_up", query: `topk(3, up)`},
	// — quantile —
	{name: "quantile_095_rate", query: `quantile(0.95, rate(http_requests_total[5m]))`},
	// — count_values (synthesizes output label) —
	{name: "count_values_le_up", query: `count_values("le", up)`},
	// — aggregation-range-fused: sum by + rate —
	{name: "sum_by_job_rate_fused", query: `sum by (job) (rate(http_requests_total[5m]))`},
	// — aggregation-range-fused: avg without + rate —
	{name: "avg_without_instance_rate_fused", query: `avg without (instance) (rate(cpu[5m]))`},
	// — aggregation-range-fused: min/max/count + rate —
	{name: "min_by_job_rate_fused", query: `min by (job) (rate(http_requests_total[5m]))`},
	{name: "max_by_job_rate_fused", query: `max by (job) (rate(http_requests_total[5m]))`},
	{name: "count_by_job_rate_fused", query: `count by (job) (rate(http_requests_total[5m]))`},
}

// goldenAggregationCases selects the subset of aggregationCases that receive
// golden files: first 5 canonical shapes plus fused direct-reducer cases.
var goldenAggregationCases = []int{0, 1, 2, 3, 4, 7, 8, 9, 10, 11}

// TestLowerAggregationGolden locks in the exact SQL for the first five
// canonical shapes plus fused direct-reducer cases in both render modes.
// Run with -update to regenerate golden files.
func TestLowerAggregationGolden(t *testing.T) {
	for _, idx := range goldenAggregationCases {
		tc := aggregationCases[idx]
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
				goldenPath := filepath.Join("testdata", "lower_aggregation", tc.name+"_"+mode.name+".sql")
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

func TestAggregationByProjectsInstantChildLabels(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `sum by (job) (up)`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "src.tags['job']") || strings.Contains(rq.SQL, "mapKeys(src.tags)") {
		t.Fatalf("expected instant aggregation child selector to project job label only, got:\n%s", rq.SQL)
	}
}

func TestAggregationByProjectionRollbackGate(t *testing.T) {
	t.Setenv(DisableNativeAggregationLabelProjectionEnv, "true")
	root, analysis, nativeAnalysis := buildLowerInputs(t, `sum by (job) (up)`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if strings.Contains(rq.SQL, "src.tags['job']") || !strings.Contains(rq.SQL, "mapKeys(src.tags)") {
		t.Fatalf("expected rollback gate to preserve full selector labels, got:\n%s", rq.SQL)
	}
}

func TestAggregationDirectReducersByRateRangeUseFusedRows(t *testing.T) {
	cases := []struct {
		query         string
		expectedValue string
	}{
		{query: `avg by (job) (rate(http_requests_total[5m]))`, expectedValue: "avg(value)) AS value"},
		{query: `min by (job) (rate(http_requests_total[5m]))`, expectedValue: "minIf(value, NOT isNaN(value))) AS value"},
		{query: `max by (job) (rate(http_requests_total[5m]))`, expectedValue: "maxIf(value, NOT isNaN(value))) AS value"},
		{query: `count by (job) (rate(http_requests_total[5m]))`, expectedValue: "count(value)) AS value"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
			rq, err := Lower(LoweringCtx{
				Config:         testRenderConfig(),
				Analysis:       analysis,
				NativeAnalysis: nativeAnalysis,
				Params:         testRenderParamsRange(),
			}, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			for _, expected := range []string{tc.expectedValue, "counter_delta_sum", "GROUP BY tags, timestamp"} {
				if !strings.Contains(rq.SQL, expected) {
					t.Fatalf("expected fused rate aggregation SQL to contain %q, got:\n%s", expected, rq.SQL)
				}
			}
			if strings.Contains(rq.SQL, "arrayJoin(time_series) AS point") {
				t.Fatalf("expected fused rate aggregation to avoid exploding range-function matrix rows before aggregation, got:\n%s", rq.SQL)
			}
		})
	}
}

func TestAggregationByAvgOverTimeRangeUsesDirectAggregateRows(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `sum by (job, type) (avg_over_time(demo_memory_usage_bytes[1h]))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:    testRenderParamsRange().Mode,
			StartMS: 1_700_000_000_000,
			EndMS:   1_700_086_400_000,
			StepMS:  60_000,
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{"sum(if(NOT isNaN(ifNull(toFloat64(d.value), nan))", "AS finite_sum", "ASOF LEFT JOIN", "upper.finite_sum - lower.finite_sum AS finite_sum", "finite_sum / finite_count", "GROUP BY tags, timestamp"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected avg_over_time aggregation cumulative rows SQL to contain %q, got:\n%s", expected, rq.SQL)
		}
	}
	for _, unexpected := range []string{"arraySort(groupArray((d.timestamp, d.value))) AS window_series", "window_values", "CROSS JOIN", "avgIf("} {
		if strings.Contains(rq.SQL, unexpected) {
			t.Fatalf("expected avg_over_time aggregation cumulative rows SQL to avoid %q, got:\n%s", unexpected, rq.SQL)
		}
	}
}

func TestAggregationByRateRangeUsesNativeGridArrayAggregationWhenEnabled(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`)
	cfg := testRenderConfig()
	cfg.EnableNativeGridFunctions = true
	rq, err := Lower(LoweringCtx{
		Config:         cfg,
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:    testRenderParamsRange().Mode,
			StartMS: 1_700_000_000_000,
			EndMS:   1_700_086_400_000,
			StepMS:  300_000,
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{"timeSeriesRateToGrid(", "arrayReduce('sumForEach'", "group_values", "present_counts", "nan_counts"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected native-grid array aggregation SQL to contain %q, got:\n%s", expected, rq.SQL)
		}
	}
	for _, unexpected := range []string{"ARRAY JOIN", "groupArray((timestamp, value))", "deltaSumTimestamp("} {
		if strings.Contains(rq.SQL, unexpected) {
			t.Fatalf("expected native-grid array aggregation SQL to avoid %q, got:\n%s", unexpected, rq.SQL)
		}
	}
}

func TestAggregationByNativeGridRangeFunctionsWhenEnabled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		chFunction string
		arraySum   bool
	}{
		{name: "sum_irate", query: `sum by (job) (irate(demo_cpu_usage_seconds_total[5m]))`, chFunction: "timeSeriesInstantRateToGrid(", arraySum: true},
		{name: "sum_delta", query: `sum by (job) (delta(demo_cpu_usage_seconds_total[10m]))`, chFunction: "timeSeriesDeltaToGrid(", arraySum: true},
		{name: "sum_idelta", query: `sum by (job) (idelta(demo_cpu_usage_seconds_total[10m]))`, chFunction: "timeSeriesInstantDeltaToGrid(", arraySum: true},
		{name: "sum_last_over_time", query: `sum by (job) (last_over_time(demo_cpu_usage_seconds_total[5m]))`, chFunction: "timeSeriesLastToGrid(", arraySum: true},
		{name: "min_delta_rows", query: `min by (job) (delta(demo_cpu_usage_seconds_total[10m]))`, chFunction: "timeSeriesDeltaToGrid("},
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
					Mode:    testRenderParamsRange().Mode,
					StartMS: 1_700_000_000_000,
					EndMS:   1_700_003_600_000,
					StepMS:  30_000,
				},
			}, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			if !strings.Contains(rq.SQL, tc.chFunction) {
				t.Fatalf("expected native-grid aggregation SQL to contain %q, got:\n%s", tc.chFunction, rq.SQL)
			}
			if tc.arraySum {
				for _, expected := range []string{"arrayReduce('sumForEach'", "present_counts", "nan_counts"} {
					if !strings.Contains(rq.SQL, expected) {
						t.Fatalf("expected native-grid array aggregation SQL to contain %q, got:\n%s", expected, rq.SQL)
					}
				}
				if strings.Contains(rq.SQL, "ARRAY JOIN") {
					t.Fatalf("expected SUM native-grid aggregation to avoid ARRAY JOIN, got:\n%s", rq.SQL)
				}
			} else if !strings.Contains(rq.SQL, "ARRAY JOIN") {
				t.Fatalf("expected non-SUM native-grid aggregation to aggregate row source, got:\n%s", rq.SQL)
			}
		})
	}
}

func TestAggregationByKeepsFullLabelsForRangeFunctions(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `sum by (job) (rate(http_requests_total[5m]))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsRange(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if strings.Contains(rq.SQL, "src.tags['job']") || !strings.Contains(rq.SQL, "mapKeys(src.tags)") {
		t.Fatalf("expected range-function aggregation to preserve full per-series labels before rate, got:\n%s", rq.SQL)
	}
}

// TestLowerAggregationNilErrors exercises the defensive nil guard in
// lowerAggregation. A nil node must return a non-sentinel error (callers should
// not silently fall back to Fragment for a malformed plan tree).
func TestLowerAggregationNilErrors(t *testing.T) {
	_, err := lowerAggregation(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil AggregationPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
