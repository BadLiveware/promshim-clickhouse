package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// histogramFunctionCases covers all three histogram-function plan kinds
// (HistogramQuantilePlan, HistogramFractionPlan, HistogramQuantilesPlan)
// across the canonical queries listed in the Surface 10 spec.
var histogramFunctionCases = []struct {
	name  string
	query string
}{
	// HistogramQuantilePlan — simple instant form
	{
		name:  "quantile_simple",
		query: `histogram_quantile(0.95, http_request_duration_seconds_bucket)`,
	},
	// HistogramQuantilePlan — rate-wrapped range form
	{
		name:  "quantile_rate",
		query: `histogram_quantile(0.5, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`,
	},
	// HistogramFractionPlan — simple instant form
	{
		name:  "fraction_simple",
		query: `histogram_fraction(0, 0.2, http_request_duration_seconds_bucket)`,
	},
	// HistogramQuantilesPlan — multi-quantile instant form
	// Prometheus parser signature: histogram_quantiles(bucket_expr, label, q1, q2, ...)
	{
		name:  "quantiles_multi",
		query: `histogram_quantiles(http_request_duration_seconds_bucket, "quantile", 0.5, 0.95)`,
	},
}

// TestLowerHistogramFunctionGolden locks in the exact SQL for all cases
// in both instant and range modes. Run with -update to regenerate golden files.
func TestLowerHistogramFunctionGolden(t *testing.T) {
	for _, tc := range histogramFunctionCases {
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
				goldenPath := filepath.Join("testdata", "lower_histogram_function", tc.name+"_"+mode.name+".sql")
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

func TestLowerHistogramQuantileKeepsNonBucketGroupingLabels(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.95, sum by (job, le) (rate(http_request_duration_seconds_bucket[5m])))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{"has(['job', 'le'], tag.1)", "arrayFilter(tag -> tag.1 != 'le' AND tag.1 != '__name__'", "GROUP BY histogram_tags"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected %q in grouped histogram quantile SQL, got:\n%s", expected, rq.SQL)
		}
	}
}

func TestLowerHistogramFunctionEmitsPreparationShapeDecision(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsRange(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_preparation_shape")
	if !ok {
		t.Fatalf("expected histogram preparation shape decision, got %#v", rq.PhysicalDecisions)
	}
	if decision.Strategy != "classic_histogram_preparation_le_only" {
		t.Fatalf("unexpected histogram preparation strategy: %#v", decision)
	}
}

func TestLowerHistogramFunctionCanUseLateTagNativeGridRows(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`)
	cfg := testRenderConfig()
	cfg.EnableNativeGridFunctions = true
	rq, err := Lower(LoweringCtx{
		Config:         cfg,
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsRange(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_native_grid_rows_shape")
	if !ok {
		t.Fatalf("expected histogram native-grid rows shape decision, got %#v", rq.PhysicalDecisions)
	}
	if decision.Strategy != "late_series_join" {
		t.Fatalf("unexpected histogram native-grid rows strategy: %#v", decision)
	}
	for _, expected := range []string{"d.id IN (SELECT id FROM", "INNER JOIN", "series"} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected late-tag native-grid SQL to contain %q, got:\n%s", expected, rq.SQL)
		}
	}
}

func TestLowerNonHistogramDoesNotEmitPreparationShapeDecision(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `avg_over_time(memory_usage_bytes[1h])`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsRange(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_preparation_shape"); ok {
		t.Fatalf("non-histogram query unexpectedly emitted histogram preparation decision: %#v", decision)
	}
}

func TestLowerHistogramQuantileCoalescesGroupedRateDirectly(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.95, sum by (job, le) (rate(http_request_duration_seconds_bucket[5m])))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{
		"histogram_function_child_direct_child_rows",
		"SELECT tags AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, value AS value",
		"GROUP BY histogram_tags, timestamp, upper_bound",
		"GROUP BY histogram_tags, timestamp",
	} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected direct grouped histogram coalescing SQL to contain %q, got:\n%s", expected, rq.SQL)
		}
	}
	for _, unwanted := range []string{
		"histogram_child_rows",
		"GROUP BY tags) AS histogram_child_rows",
	} {
		if strings.Contains(rq.SQL, unwanted) {
			t.Fatalf("expected grouped histogram quantile to avoid intermediate child aggregation %q, got:\n%s", unwanted, rq.SQL)
		}
	}
}

// TestLowerHistogramQuantileDirectPathCoalescesAtEvaluationTime locks in the
// issue #38 fix: the fused direct histogram path must stamp every child row
// with the constant evaluation instant before bucket coalescing. An instant
// selector child emits each series' own last-sample time, so grouping by the
// raw row timestamp fragments buckets from series with staggered scrape times
// into separate partial histograms. This bites hardest on groups formed from
// series MISSING one of the by-labels (the `{}` group), which aggregate
// heterogeneous sources with unaligned scrape offsets. With the timestamp
// pinned to fromUnixTimestamp64Milli(<eval>), the coalesce GROUP BY has
// exactly one timestamp partition per group, so all buckets merge into one
// histogram per group regardless of per-series timestamps — matching
// Prometheus, which evaluates all buckets at one instant and groups by
// signature-without-le. The emitted result timestamp stays the evaluation
// time.
func TestLowerHistogramQuantileDirectPathCoalescesAtEvaluationTime(t *testing.T) {
	// `node` is absent on some series in the reported scenario; those series
	// collapse into the `{}` group, whose buckets must still coalesce.
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.99, sum by (le, node) (http_request_duration_seconds_bucket))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{
		// Direct fused path is in play.
		"histogram_function_child_direct_child_rows",
		// Child rows are normalized to the constant evaluation instant, so
		// the timestamp grouping key below cannot fragment by per-series
		// last-sample times.
		"SELECT tags AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, value AS value",
		// Labeled groups still coalesce per histogram identity.
		"GROUP BY histogram_tags, timestamp, upper_bound",
		"GROUP BY histogram_tags, timestamp",
	} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected direct histogram coalescing SQL to contain %q, got:\n%s", expected, rq.SQL)
		}
	}
	// The raw per-series row timestamp must not feed the coalesce grouping.
	if strings.Contains(rq.SQL, "SELECT tags AS tags, timestamp AS timestamp, value AS value, tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2) AS le_raw FROM (") {
		t.Fatalf("direct histogram path still projects the raw per-series timestamp into the coalesce grouping:\n%s", rq.SQL)
	}
}

// TestLowerHistogramQuantileDirectPathLEOnlyCoalescesAtEvaluationTime covers
// the `sum by (le)` shape: every series lands in the single empty-tags group,
// so the coalesce grouping degenerates to (timestamp, upper_bound). The
// timestamp key must be the constant evaluation instant or series with
// staggered last-sample times split the lone histogram apart (issue #38).
func TestLowerHistogramQuantileDirectPathLEOnlyCoalescesAtEvaluationTime(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.5, sum by (le) (http_request_duration_seconds_bucket))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	for _, expected := range []string{
		"histogram_function_child_direct_child_rows",
		"SELECT tags AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, value AS value",
		"GROUP BY timestamp, upper_bound",
		") AS coalesced_histogram_rows GROUP BY timestamp)",
	} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected le-only direct histogram coalescing SQL to contain %q, got:\n%s", expected, rq.SQL)
		}
	}
}

// TestLowerHistogramFunctionNilErrors exercises the defensive nil guard in
// lowerHistogramFunction. A nil node must return a non-sentinel error.
func TestLowerHistogramFunctionNilErrors(t *testing.T) {
	_, err := lowerHistogramFunction(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
