package plan

import (
	"fmt"
	"strings"
	"testing"
)

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

func TestAnalyzeExpressionSupportsAvgAggregation(t *testing.T) {
	expr, err := ParseExpression("avg(up)")
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

func TestAnalyzeExpressionSupportsScalarBinary(t *testing.T) {
	expr, err := ParseExpression("1 + 2")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported binary expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsVectorScalarBinary(t *testing.T) {
	expr, err := ParseExpression("up * 100")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported vector-scalar expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsUnaryVector(t *testing.T) {
	expr, err := ParseExpression("-up")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported unary expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsLabelReplace(t *testing.T) {
	expr, err := ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported label_replace, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsLabelJoin(t *testing.T) {
	expr, err := ParseExpression(`label_join(up, "joined", "/", "job", "namespace")`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported label_join, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsTopKAggregator(t *testing.T) {
	expr, err := ParseExpression("topk(3, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported topk aggregation, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsBottomKAggregator(t *testing.T) {
	expr, err := ParseExpression("bottomk(3, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported bottomk aggregation, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsCountValuesAggregator(t *testing.T) {
	expr, err := ParseExpression(`count_values("sample_value", up)`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported count_values aggregation, got %#v", result)
	}
}

func TestAnalyzeExpressionRejectsDynamicTopKParameter(t *testing.T) {
	expr, err := ParseExpression("topk(1 + 2, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported aggregation parameter expression, got %#v", result)
	}
	if result.Reason == "" {
		t.Fatal("expected unsupported reason")
	}
}

func TestAnalyzeExpressionSupportsHistogramQuantile(t *testing.T) {
	expr, err := ParseExpression("histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported histogram_quantile expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsHistogramProjectionFunctions(t *testing.T) {
	queries := []string{
		"histogram_count(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))",
		"histogram_sum(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))",
		"histogram_avg(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))",
	}
	for _, query := range queries {
		expr, err := ParseExpression(query)
		if err != nil {
			t.Fatalf("ParseExpression(%q): %v", query, err)
		}
		result := AnalyzeExpression(expr)
		if !result.Supported {
			t.Fatalf("expected supported histogram projection function %q, got %#v", query, result)
		}
	}
}

func TestAnalyzeExpressionRejectsNonLiteralHistogramQuantileParameter(t *testing.T) {
	expr, err := ParseExpression("histogram_quantile(1 / 2, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported histogram_quantile parameter expression, got %#v", result)
	}
	if result.Reason == "" {
		t.Fatal("expected unsupported reason")
	}
}

func TestAnalyzeExpressionRejectsHistogramFractionUnsupported(t *testing.T) {
	expr, err := ParseExpression("histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported histogram_fraction expression, got %#v", result)
	}
	if !strings.Contains(result.Reason, "histogram_fraction") {
		t.Fatalf("expected histogram_fraction in unsupported reason, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsVectorVectorBinary(t *testing.T) {
	expr, err := ParseExpression("up + up")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported vector-vector binary, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for vector-vector binary, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsVectorMatchingBinary(t *testing.T) {
	expr, err := ParseExpression("up * on(job) group_left sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported vector matching binary, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsVectorMatchingFillModifier(t *testing.T) {
	expr, err := ParseExpression("up + on(job,instance) fill(0) up")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported vector matching fill modifier query, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for fill modifier query, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsSetOperator(t *testing.T) {
	expr, err := ParseExpression("up and on(job) up")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported set operator, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for set operator, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsOffsetModifier(t *testing.T) {
	expr, err := ParseExpression("up offset 5m")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported offset selector, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsAtModifierLiteral(t *testing.T) {
	expr, err := ParseExpression("up @ 1710000000")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported @ selector, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsAtModifierStartEndInRangeExpression(t *testing.T) {
	expr, err := ParseExpression("up @ start() + up @ end()")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported start/end @ expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsLastOverTime(t *testing.T) {
	expr, err := ParseExpression("last_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported last_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for last_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsSumOverTime(t *testing.T) {
	expr, err := ParseExpression("sum_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported sum_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for sum_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsAvgOverTime(t *testing.T) {
	expr, err := ParseExpression("avg_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported avg_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for avg_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsMaxOverTime(t *testing.T) {
	expr, err := ParseExpression("max_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported max_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for max_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsMinOverTime(t *testing.T) {
	expr, err := ParseExpression("min_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported min_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for min_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsCountOverTime(t *testing.T) {
	expr, err := ParseExpression("count_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported count_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for count_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsQuantileOverTime(t *testing.T) {
	expr, err := ParseExpression("quantile_over_time(0.9, up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported quantile_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for quantile_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionRejectsNonLiteralQuantileOverTimeParameter(t *testing.T) {
	expr, err := ParseExpression("quantile_over_time(1/2, up[5m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported non-literal quantile_over_time parameter, got %#v", result)
	}
	if result.Reason == "" {
		t.Fatal("expected unsupported reason")
	}
}

func TestAnalyzeExpressionSupportsAbsent(t *testing.T) {
	expr, err := ParseExpression(`absent(nonexistent{job="api"})`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported absent expression, got %#v", result)
	}
	if result.Difficulty != DifficultyMedium {
		t.Fatalf("expected medium difficulty for absent, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsAbsentOverTime(t *testing.T) {
	expr, err := ParseExpression(`absent_over_time(nonexistent{job="api"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported absent_over_time expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for absent_over_time, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsNestedMatrixBinaryOverTimeFunctions(t *testing.T) {
	expr, err := ParseExpression("sum_over_time((up * 100)[5m:30s]) + count_over_time((up * 100)[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported nested matrix-function binary expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for nested matrix-function binary, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsSubquery(t *testing.T) {
	expr, err := ParseExpression("last_over_time(up[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported subquery expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for local subquery composition expression, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsSubqueryWithLocalChildExpression(t *testing.T) {
	expr, err := ParseExpression("(up * 100)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported subquery with local child expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for local-child subquery expression, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsSubqueryWithLocalAggregationChild(t *testing.T) {
	expr, err := ParseExpression("sum(up)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported subquery with local aggregation child, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for local-aggregation subquery expression, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsNestedSubqueryViaLastOverTime(t *testing.T) {
	expr, err := ParseExpression("last_over_time(last_over_time((up * 100)[5m:30s])[10m:1m])")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported nested subquery expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for nested subquery expression, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionRejectsDelegationSensitiveLeafFunctionsWithSubquery(t *testing.T) {
	for _, fn := range []string{"rate", "irate", "increase", "delta", "idelta", "deriv", "changes"} {
		exprText := fmt.Sprintf("%s(up[5m:30s])", fn)
		expr, err := ParseExpression(exprText)
		if err != nil {
			t.Fatal(err)
		}
		result := AnalyzeExpression(expr)
		if result.Supported {
			t.Fatalf("expected unsupported %s(subquery) expression, got %#v", fn, result)
		}
		if result.Difficulty != DifficultyHard {
			t.Fatalf("expected hard difficulty for %s(subquery) rejection, got %s", fn, result.Difficulty)
		}
		if result.Reason == "" {
			t.Fatal("expected unsupported reason")
		}
	}
}

func TestAnalyzeExpressionRejectsDelegationSensitiveLeafFunctionsNestedInAggregateWithSubquery(t *testing.T) {
	for _, fn := range []string{"rate", "irate", "increase", "delta", "idelta", "deriv", "changes"} {
		exprText := fmt.Sprintf("sum(%s(up[5m:30s]))", fn)
		expr, err := ParseExpression(exprText)
		if err != nil {
			t.Fatal(err)
		}
		result := AnalyzeExpression(expr)
		if result.Supported {
			t.Fatalf("expected unsupported %s wrapped in aggregate, got %#v", fn, result)
		}
		if result.Difficulty != DifficultyHard {
			t.Fatalf("expected hard difficulty for %s nested wrapper rejection, got %s", fn, result.Difficulty)
		}
		if result.Reason == "" {
			t.Fatal("expected unsupported reason")
		}
		if !strings.Contains(result.Reason, fn) {
			t.Fatalf("expected unsupported reason to mention %q, got %q", fn, result.Reason)
		}
	}
}

func TestAnalyzeDelegatableExpressionRejectsSubqueryWithNonDelegatableInnerExpression(t *testing.T) {
	expr, err := ParseExpression(`label_join(up, "joined", "/", "job", "instance")[5m:30s]`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeDelegatableExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported delegated subquery inner expression, got %#v", result)
	}
	if result.Reason == "" {
		t.Fatal("expected unsupported reason")
	}
}
