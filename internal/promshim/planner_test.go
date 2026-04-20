package promshim

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildPlanCreatesDelegatedLeafPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected delegated plan, got error: %v", err)
	}
	if _, ok := plan.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegatedExprPlan, got %T", plan)
	}
}

func TestBuildPlanCreatesSumAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected sum aggregation plan, got error: %v", err)
	}

	agg, ok := plan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", plan)
	}
	if agg.Op != parser.SUM {
		t.Fatalf("expected sum op, got %v", agg.Op)
	}
	if !sameStrings(agg.Grouping, []string{"job"}) {
		t.Fatalf("unexpected grouping: %#v", agg.Grouping)
	}
	if agg.Without {
		t.Fatal("did not expect without=true")
	}
	if _, ok := agg.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated child plan, got %T", agg.Child)
	}
}

func TestBuildPlanCreatesAvgAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("avg(up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected avg aggregation plan, got error: %v", err)
	}

	agg, ok := plan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", plan)
	}
	if agg.Op != parser.AVG {
		t.Fatalf("expected avg op, got %v", agg.Op)
	}
}

func TestDecideNativeAggregationPushdownAllowsDelegatedLeaf(t *testing.T) {
	logical, err := buildLogicalPlan(mustParseExpr(t, "sum by (job) (up)"))
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := logical.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", logical)
	}

	decision := decideNativeAggregationPushdown(agg, planContext{PreferNativeAggregationPushdown: true})
	if !decision.Eligible {
		t.Fatalf("expected pushdown eligibility, got %#v", decision)
	}
	if decision.Source.PromQLLeaf.String() != "up" {
		t.Fatalf("expected delegated leaf source, got %#v", decision.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationPlanForRangeDelegatedLeaf(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	if _, ok := plan.(*nativeAggregationPlan); !ok {
		t.Fatalf("expected nativeAggregationPlan, got %T", plan)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationPlanForUnaryTransformedLeaf(t *testing.T) {
	expr, err := plan.ParseExpression("avg by (job) (-up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	native, ok := plan.(*nativeAggregationPlan)
	if !ok {
		t.Fatalf("expected nativeAggregationPlan, got %T", plan)
	}
	if !strings.Contains(native.Source.ValueExpr, "-") {
		t.Fatalf("expected unary value transform in native source, got %#v", native.Source)
	}
	if !strings.Contains(native.Source.TagsExpr, "__name__") {
		t.Fatalf("expected metric-name dropping tags transform, got %#v", native.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationPlanForVectorScalarLeaf(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up * 100)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	native, ok := plan.(*nativeAggregationPlan)
	if !ok {
		t.Fatalf("expected nativeAggregationPlan, got %T", plan)
	}
	if !strings.Contains(native.Source.ValueExpr, "100") || !strings.Contains(native.Source.ValueExpr, "*") {
		t.Fatalf("expected vector-scalar arithmetic in native source, got %#v", native.Source)
	}
	if !strings.Contains(native.Source.TagsExpr, "__name__") {
		t.Fatalf("expected metric-name dropping tags transform, got %#v", native.Source)
	}
}

func TestDecideNativeAggregationPushdownRejectsNonPushdownSafeChild(t *testing.T) {
	logical, err := buildLogicalPlan(mustParseExpr(t, `sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`))
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := logical.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", logical)
	}

	decision := decideNativeAggregationPushdown(agg, planContext{PreferNativeAggregationPushdown: true})
	if decision.Eligible {
		t.Fatalf("expected non-eligible pushdown, got %#v", decision)
	}
	if !strings.Contains(decision.Reason, "pushdown-safe") {
		t.Fatalf("expected explicit fallback reason, got %#v", decision)
	}
}

func TestDecideNativeAggregationPushdownRejectsWhenDisabled(t *testing.T) {
	logical, err := buildLogicalPlan(mustParseExpr(t, "sum by (job) (up)"))
	if err != nil {
		t.Fatal(err)
	}
	agg := logical.(*logicalAggregationPlan)
	decision := decideNativeAggregationPushdown(agg, planContext{PreferNativeAggregationPushdown: false})
	if decision.Eligible {
		t.Fatalf("expected disabled pushdown to be rejected, got %#v", decision)
	}
	if !strings.Contains(decision.Reason, "disabled") {
		t.Fatalf("expected disabled reason, got %#v", decision)
	}
}

func TestBuildPlanWithContextFallsBackToLocalAggregationForNonLeafChild(t *testing.T) {
	expr, err := plan.ParseExpression(`sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected local aggregation fallback, got error: %v", err)
	}
	if _, ok := plan.(*localAggregationPlan); !ok {
		t.Fatalf("expected localAggregationPlan fallback, got %T", plan)
	}
}

func TestExplainPlanDescribesNativeAggregationStrategy(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := explainPlan(plan)
	if explain.Strategy != "native_sql" {
		t.Fatalf("expected native_sql strategy, got %#v", explain)
	}
	if explain.Kind != "aggregation" {
		t.Fatalf("expected aggregation kind, got %#v", explain)
	}
	if explain.Estimate == nil || explain.Estimate.PointsPerSeries != 11 {
		t.Fatalf("expected range estimate with 11 points per series, got %#v", explain.Estimate)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "delegated_promql" {
		t.Fatalf("expected delegated leaf child explain, got %#v", explain.Children)
	}
}

func TestExplainPlanDescribesNativeTransformedAggregationStrategy(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up * 100)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := explainPlan(plan)
	if explain.Strategy != "native_sql" {
		t.Fatalf("expected native_sql strategy, got %#v", explain)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "native_sql_expression" {
		t.Fatalf("expected native transformed child explain, got %#v", explain.Children)
	}
	if len(explain.Children[0].Children) != 1 || explain.Children[0].Children[0].Strategy != "delegated_promql" {
		t.Fatalf("expected delegated leaf under native transform explain, got %#v", explain.Children)
	}
}

func TestExplainPlanDescribesLocalAggregationFallbackReasonWhenPushdownDisabled(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeInstant,
		EvaluationTime:                  time.Unix(300, 0).UTC(),
		PreferNativeAggregationPushdown: false,
	})
	if err != nil {
		t.Fatalf("expected local aggregation plan, got error: %v", err)
	}
	explain := explainPlan(plan)
	if explain.Strategy != "local" {
		t.Fatalf("expected local strategy, got %#v", explain)
	}
	if !strings.Contains(explain.Reason, "disabled") {
		t.Fatalf("expected disabled fallback reason, got %#v", explain)
	}
}

func TestExplainPlanDescribesLocalAggregationFallbackReasonWhenChildIsNotPushdownSafe(t *testing.T) {
	expr, err := plan.ParseExpression(`sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected local aggregation plan, got error: %v", err)
	}
	explain := explainPlan(plan)
	if explain.Strategy != "local" {
		t.Fatalf("expected local strategy, got %#v", explain)
	}
	if !strings.Contains(explain.Reason, "pushdown-safe") {
		t.Fatalf("expected pushdown-safe fallback reason, got %#v", explain)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "local" {
		t.Fatalf("expected local child explain for label_join fallback, got %#v", explain.Children)
	}
}

func TestBuildPlanCreatesScalarBinaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("1 + 2")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected scalar binary plan, got error: %v", err)
	}
	if _, ok := plan.(*localBinaryPlan); !ok {
		t.Fatalf("expected localBinaryPlan, got %T", plan)
	}
}

func TestBuildPlanCreatesUnaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("-up")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected unary plan, got error: %v", err)
	}
	if _, ok := plan.(*localUnaryPlan); !ok {
		t.Fatalf("expected localUnaryPlan, got %T", plan)
	}
}

func TestBuildPlanCreatesLabelReplacePlan(t *testing.T) {
	expr, err := plan.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected label_replace plan, got error: %v", err)
	}
	if _, ok := plan.(*localLabelReplacePlan); !ok {
		t.Fatalf("expected localLabelReplacePlan, got %T", plan)
	}
}

func TestBuildPlanCreatesLabelJoinPlan(t *testing.T) {
	expr, err := plan.ParseExpression(`label_join(up, "joined", "/", "job", "namespace")`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected label_join plan, got error: %v", err)
	}
	if _, ok := plan.(*localLabelJoinPlan); !ok {
		t.Fatalf("expected localLabelJoinPlan, got %T", plan)
	}
}

func TestBuildPlanRejectsInvalidLabelReplaceRegex(t *testing.T) {
	expr, err := plan.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "[")`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPlan(expr)
	if err == nil {
		t.Fatal("expected bad data build error")
	}
	if internalErrorKindOf(err) != internalErrorKindBadData {
		t.Fatalf("expected bad_data error kind, got %v (%v)", internalErrorKindOf(err), err)
	}
	if !strings.Contains(err.Error(), "invalid regular expression in label_replace") {
		t.Fatalf("expected regex context in error, got %q", err.Error())
	}
}

func TestBuildPlanRejectsInvalidLabelJoinDestination(t *testing.T) {
	expr, err := plan.ParseExpression(`label_join(up, "", "/", "job")`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPlan(expr)
	if err == nil {
		t.Fatal("expected bad data build error")
	}
	if internalErrorKindOf(err) != internalErrorKindBadData {
		t.Fatalf("expected bad_data error kind, got %v (%v)", internalErrorKindOf(err), err)
	}
	if !strings.Contains(err.Error(), "invalid destination label name in label_join") {
		t.Fatalf("expected destination label context in error, got %q", err.Error())
	}
}

func TestBuildPlanRejectsUnsupportedAggregation(t *testing.T) {
	expr, err := plan.ParseExpression("topk(3, up)")
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPlan(expr)
	if err == nil {
		t.Fatal("expected unsupported plan build error")
	}
	var buildErr *planBuildError
	if !errors.As(err, &buildErr) {
		t.Fatalf("expected planBuildError, got %T (%v)", err, err)
	}
	if buildErr.Support.Supported {
		t.Fatalf("expected unsupported support result, got %#v", buildErr.Support)
	}
	if buildErr.Support.Difficulty != plan.DifficultyMedium {
		t.Fatalf("expected medium difficulty, got %s", buildErr.Support.Difficulty)
	}
	if buildErr.Expr == nil || buildErr.Expr.String() != "topk(3, up)" {
		t.Fatalf("expected planner error to keep expression context, got %#v", buildErr)
	}
	if buildErr.Stage == "" {
		t.Fatalf("expected planner error to keep stage context, got %#v", buildErr)
	}
	if err.Error() == "" {
		t.Fatal("expected planner error message")
	}
	if !strings.Contains(err.Error(), "aggregate planning") || !strings.Contains(err.Error(), "topk(3, up)") {
		t.Fatalf("expected planner error to include stage and expression context, got %q", err.Error())
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
