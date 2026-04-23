package logical

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
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

func TestAnalyzeExpressionSupportsTier1AdditionalAggregations(t *testing.T) {
	queries := []string{
		"stddev(up)",
		"stdvar(up)",
		"quantile(0.9, up)",
		"group(up)",
	}
	for _, query := range queries {
		expr, err := ParseExpression(query)
		if err != nil {
			t.Fatalf("ParseExpression(%q): %v", query, err)
		}
		result := AnalyzeExpression(expr)
		if !result.Supported {
			t.Fatalf("expected supported aggregation for %q, got %#v", query, result)
		}
	}
}

func TestAnalyzeExpressionRejectsDynamicQuantileAggregationParameter(t *testing.T) {
	expr, err := ParseExpression("quantile(1 / 2, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported quantile aggregation parameter expression, got %#v", result)
	}
	if !strings.Contains(result.Reason, "literal scalar parameter") {
		t.Fatalf("expected literal scalar parameter reason, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsTier1AdditionalRangeFunctions(t *testing.T) {
	queries := []string{
		"stddev_over_time(up[5m])",
		"stdvar_over_time(up[5m])",
		"present_over_time(up[5m])",
		"mad_over_time(up[5m])",
		"resets(up[5m])",
		"predict_linear(up[5m], 60)",
	}
	for _, query := range queries {
		expr, err := ParseExpression(query)
		if err != nil {
			t.Fatalf("ParseExpression(%q): %v", query, err)
		}
		result := AnalyzeExpression(expr)
		if !result.Supported {
			t.Fatalf("expected supported range function for %q, got %#v", query, result)
		}
	}
}

func TestAnalyzeExpressionRejectsDynamicPredictLinearDuration(t *testing.T) {
	expr, err := ParseExpression("predict_linear(up[5m], 1 + 2)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported dynamic predict_linear duration, got %#v", result)
	}
	if !strings.Contains(result.Reason, "literal scalar duration") {
		t.Fatalf("expected literal scalar duration reason, got %#v", result)
	}
}

func TestParseExpressionSupportsHoltWintersAlias(t *testing.T) {
	expr, err := ParseExpression("holt_winters(up[5m], 0.5, 0.3)")
	if err != nil {
		t.Fatalf("expected alias parse to succeed, got error: %v", err)
	}
	call, ok := expr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", expr)
	}
	if call.Func.Name != "double_exponential_smoothing" {
		t.Fatalf("expected alias rewrite to double_exponential_smoothing, got %q", call.Func.Name)
	}
}

func TestAnalyzeExpressionSupportsDoubleExponentialSmoothing(t *testing.T) {
	expr, err := ParseExpression("double_exponential_smoothing(up[5m], 0.5, 0.3)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported smoothing expression, got %#v", result)
	}
}

func TestAnalyzeExpressionRejectsOutOfRangeSmoothingFactor(t *testing.T) {
	expr, err := ParseExpression("double_exponential_smoothing(up[5m], 1, 0.3)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported smoothing factor, got %#v", result)
	}
	if !strings.Contains(result.Reason, "smoothing factor must be between 0 and 1 exclusive") {
		t.Fatalf("expected smoothing factor bounds reason, got %#v", result)
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

func TestAnalyzeExpressionSupportsLimitKAggregator(t *testing.T) {
	expr, err := ParseExpression("limitk(2, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported limitk aggregation, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsLimitRatioAggregator(t *testing.T) {
	expr, err := ParseExpression("limit_ratio(0.5, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported limit_ratio aggregation, got %#v", result)
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
		"histogram_stddev(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))",
		"histogram_stdvar(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))",
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

func TestAnalyzeExpressionSupportsHistogramQuantiles(t *testing.T) {
	expr, err := ParseExpression("histogram_quantiles(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])), \"quantile\", 0.5, scalar(sum(up)))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported histogram_quantiles expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for histogram_quantiles, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsVectorFunction(t *testing.T) {
	expr, err := ParseExpression("vector(0)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported vector() expression, got %#v", result)
	}
	if result.Difficulty != DifficultyMedium {
		t.Fatalf("expected medium difficulty for vector(), got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsRoundFunction(t *testing.T) {
	expr, err := ParseExpression("round(up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported round() expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for round(), got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsTier1Transforms(t *testing.T) {
	queries := []string{
		"abs(up)",
		"clamp(up, 1, 2)",
		"sin(up)",
		"timestamp(up)",
		"day_of_week(up)",
		"minute()",
		"time()",
		"pi()",
		"scalar(up)",
		"info(up)",
		"info(up, {k8s_cluster_name=\"prod\"})",
	}
	for _, query := range queries {
		expr, err := ParseExpression(query)
		if err != nil {
			t.Fatalf("ParseExpression(%q): %v", query, err)
		}
		result := AnalyzeExpression(expr)
		if !result.Supported {
			t.Fatalf("expected supported transform for %q, got %#v", query, result)
		}
	}
}

func TestAnalyzeExpressionSupportsScalarClampBounds(t *testing.T) {
	expr, err := ParseExpression("clamp(up, scalar(sum(up)), time())")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported clamp bounds, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsSortFunctions(t *testing.T) {
	queries := []string{
		"sort(up)",
		"sort_desc(up)",
		"sort_by_label(up, \"job\")",
		"sort_by_label_desc(up, \"job\", \"instance\")",
	}
	for _, query := range queries {
		expr, err := ParseExpression(query)
		if err != nil {
			t.Fatalf("ParseExpression(%q): %v", query, err)
		}
		result := AnalyzeExpression(expr)
		if !result.Supported {
			t.Fatalf("expected supported sort query for %q, got %#v", query, result)
		}
	}
}

func TestAnalyzeExpressionSupportsNestedAggregationExpression(t *testing.T) {
	expr, err := ParseExpression("count(count by (job) (up))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported nested aggregation, got %#v", result)
	}
	if result.Difficulty != DifficultyMedium {
		t.Fatalf("expected medium difficulty for nested aggregation, got %s", result.Difficulty)
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

func TestAnalyzeExpressionSupportsHistogramFraction(t *testing.T) {
	expr, err := ParseExpression("histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported histogram_fraction expression, got %#v", result)
	}
	if result.Difficulty != DifficultyHard {
		t.Fatalf("expected hard difficulty for histogram_fraction, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionRejectsNonLiteralHistogramFractionBounds(t *testing.T) {
	expr, err := ParseExpression("histogram_fraction(time(), 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported histogram_fraction bound expression, got %#v", result)
	}
	if !strings.Contains(result.Reason, "literal scalar lower bound") {
		t.Fatalf("expected literal lower bound reason, got %#v", result)
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

func TestAnalyzeExpressionSupportsRateFamilyWithSubquery(t *testing.T) {
	for _, fn := range []string{"increase", "delta", "idelta", "changes", "deriv", "resets", "rate", "irate"} {
		exprText := fmt.Sprintf("%s(sum(up)[5m:])", fn)
		expr, err := ParseExpression(exprText)
		if err != nil {
			t.Fatal(err)
		}
		result := AnalyzeExpression(expr)
		if !result.Supported {
			t.Fatalf("expected supported %s(aggregation-subquery) expression, got %#v", fn, result)
		}
		if result.Difficulty != DifficultyHard {
			t.Fatalf("expected hard difficulty for %s subquery support, got %s", fn, result.Difficulty)
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
