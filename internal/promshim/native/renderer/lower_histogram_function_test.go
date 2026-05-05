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

func TestLowerHistogramFunctionRecognizesFallbackOrShape(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes[5m])) by (proto) or sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_fallback_or")
	if !ok || decision.Strategy != "recognized" {
		t.Fatalf("expected histogram_fallback_or=recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerHistogramFunctionRejectsFallbackOrWhenNoBucketSide(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes[5m])) by (proto) or sum(rate(other_metric[5m])) by (proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_fallback_or")
	if !ok || decision.Strategy != "not_recognized" {
		t.Fatalf("expected histogram_fallback_or=not_recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerHistogramFunctionRejectsFallbackOrWhenNotHistogramQuantile(t *testing.T) {
	query := `sum(rate(foo[5m])) or sum(rate(foo_bucket[5m])) by (le)`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if _, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_fallback_or"); ok {
		t.Fatalf("did not expect histogram_fallback_or decision for non-histogram_quantile expression")
	}
}

func TestLowerHistogramFunctionRejectsFallbackOrWhenBothSidesHaveLe(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto) or sum(rate(coredns_dns_response_size_bytes_bucket[5m])) by (le, proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_fallback_or")
	if !ok || decision.Strategy != "not_recognized" || len(decision.Rejected) == 0 {
		t.Fatalf("expected histogram_fallback_or=not_recognized with rejected alternatives, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerHistogramFunctionRejectsFallbackOrWhenNeitherSideHasLe(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(foo[5m])) by (proto) or sum(rate(bar[5m])) by (proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_fallback_or")
	if !ok || decision.Strategy != "not_recognized" || len(decision.Rejected) == 0 {
		t.Fatalf("expected histogram_fallback_or=not_recognized with rejected alternatives, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerHistogramFunctionRecognizesRepeatedQuantileInputs(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto)) + histogram_quantile(0.90, sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_repeated_quantile")
	if !ok || decision.Strategy != "recognized" {
		t.Fatalf("expected histogram_repeated_quantile=recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerHistogramFunctionRejectsRepeatedQuantileWhenNoRepetition(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto)) + avg_over_time(memory_usage_bytes[1h])`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if _, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_repeated_quantile"); ok {
		t.Fatalf("did not expect repeated quantile decision for single histogram_quantile expression")
	}
}

func TestLowerHistogramFunctionReportsSemanticsPreservation(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_semantics_preservation")
	if !ok || decision.Strategy != "preserved" {
		t.Fatalf("expected histogram_semantics_preservation=preserved decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerHistogramFunctionReportsSemanticsPreservationForFallbackOr(t *testing.T) {
	query := `histogram_quantile(0.99, sum(rate(coredns_dns_request_size_bytes[5m])) by (proto) or sum(rate(coredns_dns_request_size_bytes_bucket[5m])) by (le, proto))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParamsInstant(),
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "histogram_semantics_preservation")
	if !ok || decision.Strategy != "preserved" {
		t.Fatalf("expected histogram_semantics_preservation=preserved decision, got %#v", rq.PhysicalDecisions)
	}
}
