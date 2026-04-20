package promshim

import "testing"

func TestAnalyzeExpressionSupportsSumAggregation(t *testing.T) {
	expr, err := ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported aggregation, got %#v", result)
	}
	if result.Difficulty != DifficultyMedium {
		t.Fatalf("expected medium difficulty, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionRejectsUnsupportedAggregator(t *testing.T) {
	expr, err := ParseExpression("avg(up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported aggregation, got %#v", result)
	}
	if result.Reason == "" {
		t.Fatal("expected unsupported reason")
	}
}

func TestAggregationMetricWithoutDropsMetricName(t *testing.T) {
	metric := map[string]string{
		"__name__": "up",
		"job":      "clickhouse",
		"instance": "127.0.0.1:9363",
		"pod":      "clickhouse-0",
		"service":  "clickhouse",
	}
	result := aggregationMetric(metric, []string{"instance", "pod"}, true)
	if _, ok := result["__name__"]; ok {
		t.Fatal("expected __name__ to be removed")
	}
	if _, ok := result["instance"]; ok {
		t.Fatal("expected instance to be removed")
	}
	if _, ok := result["pod"]; ok {
		t.Fatal("expected pod to be removed")
	}
	if result["job"] != "clickhouse" || result["service"] != "clickhouse" {
		t.Fatalf("unexpected remaining labels: %#v", result)
	}
}
