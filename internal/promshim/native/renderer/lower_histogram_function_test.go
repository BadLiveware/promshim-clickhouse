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

// TestLowerHistogramQuantileCollatesBucketWindowsPerID pins the F19 fix: when a
// histogram_quantile is built on sum by (le)(rate(<bucket>[range])), the
// by (le) aggregation narrows the bucket selector's tag projection to `le`
// alone, so every bucket series that shares an `le` collapses to the same tags.
// The rate window over that selector must collate per series id, not per
// (narrowed) tags — otherwise all same-le bucket series interleave into one
// window and the counter-reset-sensitive rate counts spurious cross-series
// deltas, corrupting every per-le rate the quantile is built on (observed ~139x
// off in the lab). The child materialization must therefore group by id and
// carry tags through with any(tags), never group by the narrowed tags directly.
// This is the same per-id collation shared with the resets/increase entries; it
// is asserted here specifically for the histogram bucket-collapse shape so a
// future golden regeneration cannot silently revert it.
func TestLowerHistogramQuantileCollatesBucketWindowsPerID(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket[1h])))`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsRange(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	// Fixed shape: the child window materialization selects the series id,
	// carries the narrowed tags through with any(tags), and collates GROUP BY id.
	for _, expected := range []string{
		"SELECT any(tags) AS tags,",
		"SELECT d.id AS id,",
		") GROUP BY id",
	} {
		if !strings.Contains(rq.SQL, expected) {
			t.Fatalf("expected per-id collation fragment %q in bucket-collapse histogram SQL, got:\n%s", expected, rq.SQL)
		}
	}
	// Pre-F19-fix bug shape: the child window selected `series.tags AS tags`
	// (no id) and collated GROUP BY the narrowed tags. Assert neither survives.
	// (The outer histogram coalescing legitimately uses GROUP BY tags, timestamp;
	// this guards only the bare child-window grouping.)
	for _, forbidden := range []string{
		"SELECT series.tags AS tags, d.timestamp",
		") GROUP BY tags\n",
	} {
		if strings.Contains(rq.SQL, forbidden) {
			t.Fatalf("bucket-collapse histogram SQL must not collate the child window by narrowed tags (found %q):\n%s", forbidden, rq.SQL)
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
