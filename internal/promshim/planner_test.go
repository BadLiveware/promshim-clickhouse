package promshim

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/plan"
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

func TestBuildPlanCreatesDelegatedTimeModifierLeafPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up @ 1710000000")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected delegated time-modifier plan, got error: %v", err)
	}
	leaf, ok := execPlan.(*delegatedExprPlan)
	if !ok {
		t.Fatalf("expected delegatedExprPlan, got %T", execPlan)
	}
	if !strings.Contains(leaf.Expr.String(), "up @") {
		t.Fatalf("expected @ expression to be preserved, got %q", leaf.Expr.String())
	}
}

func TestBuildPlanCreatesSubqueryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("(up * 100)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected subquery plan, got error: %v", err)
	}
	subquery, ok := execPlan.(*localSubqueryPlan)
	if !ok {
		t.Fatalf("expected localSubqueryPlan, got %T", execPlan)
	}
	if subquery.Expr.String() != "(up * 100)[5m:30s]" {
		t.Fatalf("expected subquery expression to be preserved, got %q", subquery.Expr.String())
	}
	if subquery.DelegatedLeafCompatible {
		t.Fatalf("expected non-delegated-compatible subquery for (up * 100)[5m:30s], got %#v", subquery)
	}
}

func TestBuildPlanCreatesSubqueryWithLocalAggregationChildPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum(up)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected subquery plan, got error: %v", err)
	}
	subquery, ok := execPlan.(*localSubqueryPlan)
	if !ok {
		t.Fatalf("expected localSubqueryPlan, got %T", execPlan)
	}
	if subquery.DelegatedLeafCompatible {
		t.Fatalf("expected non-delegated-compatible subquery for sum(up)[5m:30s], got %#v", subquery)
	}
	if _, ok := subquery.Child.(*localAggregationPlan); !ok {
		t.Fatalf("expected local aggregation child inside subquery, got %T", subquery.Child)
	}
}

func TestResolveDelegatedPromQLRewritesAtStartEndForRange(t *testing.T) {
	expr, err := plan.ParseExpression("up @ start() + up @ end()")
	if err != nil {
		t.Fatal(err)
	}

	promQL, err := resolveDelegatedPromQL(expr, evalParams{
		Mode:  evalModeRange,
		Start: time.Unix(100, 0).UTC(),
		End:   time.Unix(200, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("expected @ start()/end() rewrite, got error: %v", err)
	}
	if strings.Contains(promQL, "start()") || strings.Contains(promQL, "end()") {
		t.Fatalf("expected start()/end() to be rewritten to numeric timestamps, got %q", promQL)
	}
	if !strings.Contains(promQL, "100") || !strings.Contains(promQL, "200") {
		t.Fatalf("expected rewritten query to contain start/end unix seconds, got %q", promQL)
	}
}

func TestResolveDelegatedPromQLRewritesAtStartEndForInstantToEvaluationTime(t *testing.T) {
	expr, err := plan.ParseExpression("up @ start()")
	if err != nil {
		t.Fatal(err)
	}

	evalTime := time.Unix(321, 0).UTC()
	promQL, err := resolveDelegatedPromQL(expr, evalParams{Mode: evalModeInstant, EvaluationTime: evalTime})
	if err != nil {
		t.Fatalf("expected @ start() rewrite for instant mode, got error: %v", err)
	}
	if strings.Contains(promQL, "start()") {
		t.Fatalf("expected start() to be rewritten to numeric timestamp, got %q", promQL)
	}
	if !strings.Contains(promQL, "321") {
		t.Fatalf("expected rewritten query to contain evaluation unix seconds, got %q", promQL)
	}
}

func TestResolveDelegatedPromQLRewritesSubqueryAtStartForRange(t *testing.T) {
	expr, err := plan.ParseExpression("up[5m:1m] @ start()")
	if err != nil {
		t.Fatal(err)
	}

	promQL, err := resolveDelegatedPromQL(expr, evalParams{Mode: evalModeRange, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected subquery @ start() rewrite, got error: %v", err)
	}
	if strings.Contains(promQL, "start()") {
		t.Fatalf("expected subquery start() to be rewritten to numeric timestamp, got %q", promQL)
	}
	if !strings.Contains(promQL, "100") {
		t.Fatalf("expected rewritten subquery to contain start unix seconds, got %q", promQL)
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

func TestBuildPlanCreatesTopKAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("topk(3, up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected topk aggregation plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if agg.Op != parser.TOPK {
		t.Fatalf("expected topk op, got %v", agg.Op)
	}
	if agg.ParamNumber == nil || *agg.ParamNumber != 3 {
		t.Fatalf("expected topk parameter 3, got %#v", agg.ParamNumber)
	}
}

func TestBuildPlanCreatesHistogramQuantilePlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected histogram_quantile plan, got error: %v", err)
	}
	histogramPlan, ok := execPlan.(*localHistogramQuantilePlan)
	if !ok {
		t.Fatalf("expected localHistogramQuantilePlan, got %T", execPlan)
	}
	if histogramPlan.Quantile != 0.9 {
		t.Fatalf("expected quantile 0.9, got %#v", histogramPlan.Quantile)
	}
	if _, ok := histogramPlan.Child.(*localAggregationPlan); !ok {
		t.Fatalf("expected local aggregation child, got %T", histogramPlan.Child)
	}
}

func TestBuildPlanCreatesHistogramProjectionPlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_count(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected histogram projection plan, got error: %v", err)
	}
	histogramPlan, ok := execPlan.(*localHistogramProjectionPlan)
	if !ok {
		t.Fatalf("expected localHistogramProjectionPlan, got %T", execPlan)
	}
	if histogramPlan.Func != "histogram_count" {
		t.Fatalf("expected histogram_count function, got %#v", histogramPlan.Func)
	}
	if _, ok := histogramPlan.Child.(*localAggregationPlan); !ok {
		t.Fatalf("expected local aggregation child, got %T", histogramPlan.Child)
	}
}

func TestBuildPlanCreatesHistogramFractionPlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected histogram_fraction plan, got error: %v", err)
	}
	histogramPlan, ok := execPlan.(*localHistogramFractionPlan)
	if !ok {
		t.Fatalf("expected localHistogramFractionPlan, got %T", execPlan)
	}
	if histogramPlan.Lower != 0 || histogramPlan.Upper != 1 {
		t.Fatalf("expected bounds [0,1], got [%v,%v]", histogramPlan.Lower, histogramPlan.Upper)
	}
	if _, ok := histogramPlan.Child.(*localAggregationPlan); !ok {
		t.Fatalf("expected local aggregation child, got %T", histogramPlan.Child)
	}
}

func TestBuildPlanCreatesIncreasePlan(t *testing.T) {
	expr, err := plan.ParseExpression("increase(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected increase plan, got error: %v", err)
	}
	increasePlan, ok := execPlan.(*localIncreasePlan)
	if !ok {
		t.Fatalf("expected localIncreasePlan, got %T", execPlan)
	}
	if _, ok := increasePlan.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated child, got %T", increasePlan.Child)
	}
}

func TestBuildPlanCreatesVectorPlan(t *testing.T) {
	expr, err := plan.ParseExpression("vector(0)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected vector plan, got error: %v", err)
	}
	vectorPlan, ok := execPlan.(*localVectorPlan)
	if !ok {
		t.Fatalf("expected localVectorPlan, got %T", execPlan)
	}
	if _, ok := vectorPlan.Child.(*scalarLiteralPlan); !ok {
		t.Fatalf("expected scalar child for vector(), got %T", vectorPlan.Child)
	}
}

func TestBuildPlanCreatesRoundPlan(t *testing.T) {
	expr, err := plan.ParseExpression("round(up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected round plan, got error: %v", err)
	}
	roundPlan, ok := execPlan.(*localRoundPlan)
	if !ok {
		t.Fatalf("expected localRoundPlan, got %T", execPlan)
	}
	if roundPlan.Decimals != nil {
		t.Fatalf("expected nil decimals for round(up), got %#v", roundPlan.Decimals)
	}
	if _, ok := roundPlan.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated vector child for round(), got %T", roundPlan.Child)
	}
}

func TestBuildPlanCreatesNestedAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("count(count by (job) (up))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected nested aggregation plan, got error: %v", err)
	}
	outer, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if outer.Op != parser.COUNT {
		t.Fatalf("expected outer count op, got %v", outer.Op)
	}
	if _, ok := outer.Child.(*localAggregationPlan); !ok {
		t.Fatalf("expected local aggregation child, got %T", outer.Child)
	}
}

func TestBuildPlanCreatesLastOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("last_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected last_over_time plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", execPlan)
	}
	if rangeFn.Func != "last_over_time" {
		t.Fatalf("expected last_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildPlanCreatesSumOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected sum_over_time plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", execPlan)
	}
	if rangeFn.Func != "sum_over_time" {
		t.Fatalf("expected sum_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildPlanCreatesAvgOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("avg_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected avg_over_time plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", execPlan)
	}
	if rangeFn.Func != "avg_over_time" {
		t.Fatalf("expected avg_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildPlanCreatesMaxOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("max_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected max_over_time plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", execPlan)
	}
	if rangeFn.Func != "max_over_time" {
		t.Fatalf("expected max_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildPlanCreatesMinOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("min_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected min_over_time plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", execPlan)
	}
	if rangeFn.Func != "min_over_time" {
		t.Fatalf("expected min_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildPlanCreatesCountOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("count_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected count_over_time plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", execPlan)
	}
	if rangeFn.Func != "count_over_time" {
		t.Fatalf("expected count_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildPlanCreatesQuantileOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("quantile_over_time(0.95, (up * 100)[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected quantile_over_time plan, got error: %v", err)
	}
	quantilePlan, ok := execPlan.(*localQuantileOverTimePlan)
	if !ok {
		t.Fatalf("expected localQuantileOverTimePlan, got %T", execPlan)
	}
	if quantilePlan.Quantile != 0.95 {
		t.Fatalf("expected quantile 0.95, got %#v", quantilePlan.Quantile)
	}
	if _, ok := quantilePlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected localSubquery child, got %T", quantilePlan.Child)
	}
}

func TestBuildPlanCreatesAbsentPlan(t *testing.T) {
	expr, err := plan.ParseExpression(`absent(nonexistent{job="api",instance=~".*"})`)
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected absent plan, got error: %v", err)
	}
	absentPlan, ok := execPlan.(*localAbsentPlan)
	if !ok {
		t.Fatalf("expected localAbsentPlan, got %T", execPlan)
	}
	if len(absentPlan.OutputMetric) != 1 || absentPlan.OutputMetric["job"] != "api" {
		t.Fatalf("unexpected absent output metric: %#v", absentPlan.OutputMetric)
	}
}

func TestBuildPlanCreatesAbsentOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression(`absent_over_time(nonexistent{job="api"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected absent_over_time plan, got error: %v", err)
	}
	absentPlan, ok := execPlan.(*localAbsentOverTimePlan)
	if !ok {
		t.Fatalf("expected localAbsentOverTimePlan, got %T", execPlan)
	}
	if len(absentPlan.OutputMetric) != 1 || absentPlan.OutputMetric["job"] != "api" {
		t.Fatalf("unexpected absent_over_time output metric: %#v", absentPlan.OutputMetric)
	}
	if absentPlan.BoundaryProbeExpr == nil || absentPlan.BoundaryProbeRange != 5*time.Minute {
		t.Fatalf("expected absent_over_time boundary probe for matrix selector, got expr=%T range=%s", absentPlan.BoundaryProbeExpr, absentPlan.BoundaryProbeRange)
	}
}

func TestBuildPlanCreatesNestedMatrixFunctionBinaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum_over_time((up * 100)[5m:30s]) + count_over_time((up * 100)[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected nested matrix binary local plan, got error: %v", err)
	}
	if _, ok := execPlan.(*localBinaryPlan); !ok {
		t.Fatalf("expected localBinaryPlan, got %T", execPlan)
	}
}

func TestBuildPlanCreatesNestedSubqueryRangeFunctionPlan(t *testing.T) {
	expr, err := plan.ParseExpression("last_over_time(last_over_time((up * 100)[5m:30s])[10m:1m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected nested subquery/range-function plan, got error: %v", err)
	}
	outer, ok := execPlan.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected outer localRangeFunctionPlan, got %T", execPlan)
	}
	if _, ok := outer.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected outer child localSubqueryPlan, got %T", outer.Child)
	}
}

func TestBuildPlanRejectsNonLiteralHistogramQuantileParameter(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_quantile(1 / 2, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
	if !strings.Contains(buildErr.Support.Reason, "literal scalar quantile") {
		t.Fatalf("expected quantile parameter reason, got %#v", buildErr.Support)
	}
}

func TestBuildPlanRejectsNonLiteralHistogramFractionBound(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_fraction(time(), 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
	if !strings.Contains(buildErr.Support.Reason, "literal scalar lower bound") {
		t.Fatalf("unexpected reason for histogram_fraction bound rejection: %#v", buildErr.Support)
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

	plan, analysis, err := buildPlanWithContextAndAnalysis(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := explainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "native_sql" {
		t.Fatalf("expected native_sql strategy, got %#v", explain)
	}
	if explain.Kind != "aggregation" {
		t.Fatalf("expected aggregation kind, got %#v", explain)
	}
	if explain.Estimate == nil || explain.Estimate.PointsPerSeries != 11 {
		t.Fatalf("expected range estimate with 11 points per series, got %#v", explain.Estimate)
	}
	if explain.Lowering == nil || !explain.Lowering.NativeLowerable || !explain.Lowering.AggregationPushdownEligible {
		t.Fatalf("expected native lowering metadata on aggregation explain, got %#v", explain.Lowering)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "delegated_promql" {
		t.Fatalf("expected delegated leaf child explain, got %#v", explain.Children)
	}
	if explain.Children[0].Lowering == nil || explain.Children[0].Lowering.FragmentKind == "" {
		t.Fatalf("expected lowering metadata on delegated child explain, got %#v", explain.Children)
	}
}

func TestExplainPlanDescribesNativeTransformedAggregationStrategy(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up * 100)")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := buildPlanWithContextAndAnalysis(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := explainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "native_sql" {
		t.Fatalf("expected native_sql strategy, got %#v", explain)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "native_sql_expression" {
		t.Fatalf("expected native transformed child explain, got %#v", explain.Children)
	}
	if len(explain.Children[0].Children) == 0 || explain.Children[0].Children[0].Strategy != "delegated_promql" {
		t.Fatalf("expected delegated leaf under native transform explain, got %#v", explain.Children)
	}
}

func TestExplainPlanDescribesLocalAggregationFallbackReasonWhenPushdownDisabled(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := buildPlanWithContextAndAnalysis(expr, planContext{
		Mode:                            evalModeInstant,
		EvaluationTime:                  time.Unix(300, 0).UTC(),
		PreferNativeAggregationPushdown: false,
	})
	if err != nil {
		t.Fatalf("expected local aggregation plan, got error: %v", err)
	}
	explain := explainPlanWithLowering(plan, analysis.Root)
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

	plan, analysis, err := buildPlanWithContextAndAnalysis(expr, planContext{
		Mode:                            evalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected local aggregation plan, got error: %v", err)
	}
	explain := explainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "local" {
		t.Fatalf("expected local strategy, got %#v", explain)
	}
	if !strings.Contains(explain.Reason, "pushdown-safe") {
		t.Fatalf("expected pushdown-safe fallback reason, got %#v", explain)
	}
	if explain.Lowering == nil || explain.Lowering.AggregationPushdownEligible {
		t.Fatalf("expected non-eligible lowering metadata, got %#v", explain.Lowering)
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

func TestBuildPlanCreatesVectorMatchingBinaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up * on(job) group_left sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected vector matching binary plan, got error: %v", err)
	}
	binaryPlan, ok := execPlan.(*localBinaryPlan)
	if !ok {
		t.Fatalf("expected localBinaryPlan, got %T", execPlan)
	}
	if binaryPlan.VectorMatching == nil {
		t.Fatalf("expected vector matching metadata, got %#v", binaryPlan)
	}
	if binaryPlan.VectorMatching.Card != parser.CardManyToOne {
		t.Fatalf("expected many-to-one vector matching card, got %#v", binaryPlan.VectorMatching)
	}
	if !binaryPlan.VectorMatching.On || !sameStrings(binaryPlan.VectorMatching.MatchingLabels, []string{"job"}) {
		t.Fatalf("unexpected vector matching labels: %#v", binaryPlan.VectorMatching)
	}
}

func TestBuildPlanCreatesSetOperatorPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up and on(job) up")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected set-operator plan, got error: %v", err)
	}
	binaryPlan, ok := execPlan.(*localBinaryPlan)
	if !ok {
		t.Fatalf("expected localBinaryPlan, got %T", execPlan)
	}
	if binaryPlan.Op != parser.LAND {
		t.Fatalf("expected LAND operator, got %v", binaryPlan.Op)
	}
	if binaryPlan.VectorMatching == nil || binaryPlan.VectorMatching.Card != parser.CardManyToMany {
		t.Fatalf("expected many-to-many vector matching for set operator, got %#v", binaryPlan.VectorMatching)
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

func TestBuildPlanRejectsInvalidCountValuesLabel(t *testing.T) {
	expr, err := plan.ParseExpression(`count_values("", up)`)
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
	if !strings.Contains(err.Error(), "invalid destination label name in count_values") {
		t.Fatalf("expected count_values label validation context in error, got %q", err.Error())
	}
}

func TestBuildPlanRejectsUnsupportedAggregationParameterExpression(t *testing.T) {
	expr, err := plan.ParseExpression("topk(1 + 2, up)")
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
	if buildErr.Expr == nil || buildErr.Expr.String() != "topk(1 + 2, up)" {
		t.Fatalf("expected planner error to keep expression context, got %#v", buildErr)
	}
	if buildErr.Stage == "" {
		t.Fatalf("expected planner error to keep stage context, got %#v", buildErr)
	}
	if err.Error() == "" {
		t.Fatal("expected planner error message")
	}
	if !strings.Contains(err.Error(), "aggregate planning") || !strings.Contains(err.Error(), "topk(1 + 2, up)") {
		t.Fatalf("expected planner error to include stage and expression context, got %q", err.Error())
	}
}

func TestBuildPlanBuildsIncreasePlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("increase(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected local plan for increase subquery, got error: %v", err)
	}
	increasePlan, ok := execPlan.(*localIncreasePlan)
	if !ok {
		t.Fatalf("expected localIncreasePlan, got %T", execPlan)
	}
	if _, ok := increasePlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", increasePlan.Child)
	}
}

func TestBuildPlanBuildsDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("delta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected local plan for delta subquery, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*localDeltaPlan)
	if !ok {
		t.Fatalf("expected localDeltaPlan, got %T", execPlan)
	}
	if deltaPlan.Func != "delta" {
		t.Fatalf("expected delta function, got %q", deltaPlan.Func)
	}
	if _, ok := deltaPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", deltaPlan.Child)
	}
}

func TestBuildPlanBuildsIDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("idelta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected local plan for idelta subquery, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*localDeltaPlan)
	if !ok {
		t.Fatalf("expected localDeltaPlan, got %T", execPlan)
	}
	if deltaPlan.Func != "idelta" {
		t.Fatalf("expected idelta function, got %q", deltaPlan.Func)
	}
	if _, ok := deltaPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", deltaPlan.Child)
	}
}

func TestBuildPlanBuildsChangesPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("changes(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected local plan for changes subquery, got error: %v", err)
	}
	changesPlan, ok := execPlan.(*localChangesPlan)
	if !ok {
		t.Fatalf("expected localChangesPlan, got %T", execPlan)
	}
	if _, ok := changesPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", changesPlan.Child)
	}
}

func TestBuildPlanBuildsDerivPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("deriv(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected local plan for deriv subquery, got error: %v", err)
	}
	derivPlan, ok := execPlan.(*localDerivPlan)
	if !ok {
		t.Fatalf("expected localDerivPlan, got %T", execPlan)
	}
	if _, ok := derivPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", derivPlan.Child)
	}
}

func TestBuildPlanBuildsRatePlanForSubqueryArg(t *testing.T) {
	for _, fn := range []string{"rate", "irate"} {
		exprText := fmt.Sprintf("%s(sum(up)[5m:])", fn)
		expr, err := plan.ParseExpression(exprText)
		if err != nil {
			t.Fatalf("parse %q failed: %v", exprText, err)
		}
		execPlan, err := buildPlan(expr)
		if err != nil {
			t.Fatalf("expected local plan for %q, got error: %v", exprText, err)
		}
		ratePlan, ok := execPlan.(*localRatePlan)
		if !ok {
			t.Fatalf("expected localRatePlan for %q, got %T", exprText, execPlan)
		}
		if ratePlan.Func != fn {
			t.Fatalf("expected %q function, got %q", fn, ratePlan.Func)
		}
		if _, ok := ratePlan.Child.(*localSubqueryPlan); !ok {
			t.Fatalf("expected local subquery child for %q, got %T", exprText, ratePlan.Child)
		}
	}
}

func TestBuildPlanWithContextCreatesDeltaPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("delta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local delta plan, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*localDeltaPlan)
	if !ok {
		t.Fatalf("expected localDeltaPlan, got %T", execPlan)
	}
	if deltaPlan.Func != "delta" {
		t.Fatalf("expected delta func, got %q", deltaPlan.Func)
	}
	if _, ok := deltaPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", deltaPlan.Child)
	}
}

func TestBuildPlanWithContextCreatesIDeltaPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("idelta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local idelta plan, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*localDeltaPlan)
	if !ok {
		t.Fatalf("expected localDeltaPlan, got %T", execPlan)
	}
	if deltaPlan.Func != "idelta" {
		t.Fatalf("expected idelta func, got %q", deltaPlan.Func)
	}
	if _, ok := deltaPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", deltaPlan.Child)
	}
}

func TestBuildPlanWithContextCreatesChangesPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("changes(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local changes plan, got error: %v", err)
	}
	changesPlan, ok := execPlan.(*localChangesPlan)
	if !ok {
		t.Fatalf("expected localChangesPlan, got %T", execPlan)
	}
	if _, ok := changesPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", changesPlan.Child)
	}
}

func TestBuildPlanWithContextCreatesDerivPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("deriv(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local deriv plan, got error: %v", err)
	}
	derivPlan, ok := execPlan.(*localDerivPlan)
	if !ok {
		t.Fatalf("expected localDerivPlan, got %T", execPlan)
	}
	if _, ok := derivPlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", derivPlan.Child)
	}
}

func TestBuildPlanWithContextCreatesRatePlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("rate(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local rate plan, got error: %v", err)
	}
	ratePlan, ok := execPlan.(*localRatePlan)
	if !ok {
		t.Fatalf("expected localRatePlan, got %T", execPlan)
	}
	if ratePlan.Func != "rate" {
		t.Fatalf("expected rate func, got %q", ratePlan.Func)
	}
	if _, ok := ratePlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", ratePlan.Child)
	}
}

func TestBuildPlanWithContextCreatesIncreasePlanInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("increase(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected increase plan, got error: %v", err)
	}
	if _, ok := execPlan.(*localIncreasePlan); !ok {
		t.Fatalf("expected localIncreasePlan, got %T", execPlan)
	}
}

func TestBuildPlanWithContextCreatesIncreasePlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("increase(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local increase plan, got error: %v", err)
	}
	increasePlan, ok := execPlan.(*localIncreasePlan)
	if !ok {
		t.Fatalf("expected localIncreasePlan, got %T", execPlan)
	}
	if _, ok := increasePlan.Child.(*localSubqueryPlan); !ok {
		t.Fatalf("expected local subquery child, got %T", increasePlan.Child)
	}
}

func TestBuildPlanWithContextCreatesLocalAggregationOverIncreaseInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("sum(increase(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, planContext{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, PreferNativeAggregationPushdown: true})
	if err != nil {
		t.Fatalf("expected aggregation over increase plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if _, ok := agg.Child.(*localIncreasePlan); !ok {
		t.Fatalf("expected localIncreasePlan child, got %T", agg.Child)
	}
}

func TestLocalSubqueryPlanExecutesChildAcrossInstantWindow(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	calls := make([]time.Time, 0)
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.UTC())
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful local subquery execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one output series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected three subquery points, got %#v", matrix.Series[0].Values)
	}
	if matrix.Series[0].Values[0].Timestamp != 60 || matrix.Series[0].Values[1].Timestamp != 120 || matrix.Series[0].Values[2].Timestamp != 180 {
		t.Fatalf("unexpected subquery timestamps: %#v", matrix.Series[0].Values)
	}
	if len(calls) != 3 {
		t.Fatalf("expected three child evaluations, got %d", len(calls))
	}
}

func TestLocalVectorPlanInstant(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, _ evalParams) (model.RuntimeValue, error) {
		return model.ScalarValue{Timestamp: 12.5, Value: 4}, nil
	}}
	plan := &localVectorPlan{Expr: "vector(1)", Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(10, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful vector instant execution, got error: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one vector sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 4 || vector.Samples[0].Timestamp != 12.5 {
		t.Fatalf("unexpected vector sample: %#v", vector.Samples)
	}
	if len(vector.Samples[0].Metric) != 0 {
		t.Fatalf("expected empty metric map, got %#v", vector.Samples[0].Metric)
	}
}

func TestLocalVectorPlanRangeMode(t *testing.T) {
	calls := 0
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		calls++
		return model.ScalarValue{Timestamp: float64(params.EvaluationTime.Unix()), Value: 1}, nil
	}}
	plan := &localVectorPlan{Expr: "vector(1)", Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful vector range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one vector series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected three range samples, got %#v", matrix.Series[0].Values)
	}
	if calls != 3 {
		t.Fatalf("expected child evaluated at each step, got %d", calls)
	}
}

func TestLocalRoundPlanInstant(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, _ evalParams) (model.RuntimeValue, error) {
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"__name__": "a"}, Timestamp: 1, Value: 1.2}}}, nil
	}}
	plan := &localRoundPlan{Expr: "round(up)", Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(10, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful round instant execution, got error: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 1 {
		t.Fatalf("expected rounded value 1, got %#v", vector.Samples[0].Value)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected __name__ dropped, got %#v", vector.Samples[0].Metric)
	}
}

func TestLocalRoundPlanRangeMode(t *testing.T) {
	plan := &localRoundPlan{Expr: "round(up)", Child: testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		if params.EvaluationTime.Unix()%60 == 0 {
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 2.7}}}, nil
		}
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 1.2}}}, nil
	}}}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful range round execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected one rounded series with three points, got %#v", matrix.Series)
	}
	if matrix.Series[0].Values[0].Timestamp != 120 || matrix.Series[0].Values[2].Timestamp != 180 {
		t.Fatalf("unexpected range timestamps: %#v", matrix.Series[0].Values)
	}
	if matrix.Series[0].Values[0].Value != 3 || matrix.Series[0].Values[1].Value != 1 || matrix.Series[0].Values[2].Value != 3 {
		t.Fatalf("unexpected rounded series values: %#v", matrix.Series[0].Values)
	}
}

func TestLocalRangeFunctionPlanLastOverTimeInRangeMode(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		t0 := float64(params.EvaluationTime.Add(-time.Minute).Unix())
		t1 := float64(params.EvaluationTime.Unix())
		return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: t0, Value: t0}, {Timestamp: t1, Value: t1}}}}}, nil
	}}
	plan := &localRangeFunctionPlan{Expr: "last_over_time((up * 100)[5m:1m])", Func: "last_over_time", Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful range-mode last_over_time execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one output series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected three output points (120,150,180), got %#v", matrix.Series[0].Values)
	}
	if matrix.Series[0].Values[0].Timestamp != 120 || matrix.Series[0].Values[1].Timestamp != 150 || matrix.Series[0].Values[2].Timestamp != 180 {
		t.Fatalf("unexpected output timestamps: %#v", matrix.Series[0].Values)
	}
}

func TestLocalQuantileOverTimePlanInstant(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		return model.MatrixValue{Series: []model.RangeSeries{
			{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: float64(params.EvaluationTime.Add(-10 * time.Second).Unix()), Value: 2}, {Timestamp: float64(params.EvaluationTime.Add(-5 * time.Second).Unix()), Value: 1}, {Timestamp: float64(params.EvaluationTime.Unix()), Value: 3}}},
			{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: float64(params.EvaluationTime.Add(-10 * time.Second).Unix()), Value: 7}, {Timestamp: float64(params.EvaluationTime.Unix()), Value: 5}}},
		}}, nil
	}}
	plan := &localQuantileOverTimePlan{Expr: "quantile_over_time(0.5, (up*100)[5m:30s])", Quantile: 0.5, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful instant quantile_over_time execution, got error: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two quantile outputs, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 2 || vector.Samples[1].Value != 6 {
		t.Fatalf("unexpected quantile outputs: %#v", vector.Samples)
	}
}

func TestLocalQuantileOverTimePlanRangeMode(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		return model.MatrixValue{Series: []model.RangeSeries{
			{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: float64(params.EvaluationTime.Add(-10 * time.Second).Unix()), Value: 2}, {Timestamp: float64(params.EvaluationTime.Unix()), Value: 10}}},
		}}, nil
	}}
	plan := &localQuantileOverTimePlan{Expr: "quantile_over_time(0.5, (up*100)[5m:30s])", Quantile: 0.5, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful range-mode quantile_over_time execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one output series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected three output points, got %#v", matrix.Series[0].Values)
	}
	if matrix.Series[0].Values[0].Timestamp != 120 || matrix.Series[0].Values[1].Timestamp != 150 || matrix.Series[0].Values[2].Timestamp != 180 {
		t.Fatalf("expected outer-step timestamps, got %#v", matrix.Series[0].Values)
	}
	for _, point := range matrix.Series[0].Values {
		if point.Value != 6 {
			t.Fatalf("expected quantile 0.5 to be 6 at each step, got %#v", matrix.Series[0].Values)
		}
	}
}

func TestLocalRangeFunctionPlanRangeModeNormalizesToOuterStepTimestamps(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		shifted := float64(params.EvaluationTime.Add(-10 * time.Second).Unix())
		return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: shifted, Value: 1}}}}}, nil
	}}
	plan := &localRangeFunctionPlan{Expr: "last_over_time((up * 100)[5m:1m])", Func: "last_over_time", Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful range-mode execution, got error: %v", err)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("unexpected range-function output: %#v", matrix.Series)
	}
	if matrix.Series[0].Values[0].Timestamp != 120 || matrix.Series[0].Values[1].Timestamp != 150 || matrix.Series[0].Values[2].Timestamp != 180 {
		t.Fatalf("expected outer-step timestamps, got %#v", matrix.Series[0].Values)
	}
}

func TestLocalAbsentPlanRangeModeProducesMatrixForMissingSeries(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, _ evalParams) (model.RuntimeValue, error) {
		return model.VectorValue{}, nil
	}}
	plan := &localAbsentPlan{Expr: `absent(nonexistent{job="api"})`, OutputMetric: map[string]string{"job": "api"}, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful absent range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || matrix.Series[0].Metric["job"] != "api" {
		t.Fatalf("unexpected absent range output: %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected three absent range points, got %#v", matrix.Series[0].Values)
	}
}

func TestLocalAbsentOverTimePlanRangeModeProducesMatrixForMissingSeries(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, _ evalParams) (model.RuntimeValue, error) {
		return model.MatrixValue{}, nil
	}}
	plan := &localAbsentOverTimePlan{Expr: `absent_over_time(nonexistent{job="api"}[5m])`, OutputMetric: map[string]string{"job": "api"}, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful absent_over_time range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || matrix.Series[0].Metric["job"] != "api" {
		t.Fatalf("unexpected absent_over_time range output: %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected three absent_over_time range points, got %#v", matrix.Series[0].Values)
	}
}

func TestLocalSubqueryPlanUsesLocalPathForDelegatedMatrixRootInInstantMode(t *testing.T) {
	expr := mustParseExpr(t, "up[2m:1m]")
	calls := 0
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		calls++
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, DelegatedLeafCompatible: true, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
	if err != nil {
		t.Fatalf("expected local matrix-root subquery execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected local matrix-root points, got %#v", matrix.Series)
	}
	if calls != 3 {
		t.Fatalf("expected local child to be called per subquery step, got %d", calls)
	}
}

func TestLocalSubqueryPlanAppliesTimestampAndOffset(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m] @ 300 offset 1m")
	subquery, ok := expr.(*parser.SubqueryExpr)
	if !ok {
		t.Fatalf("expected subquery expression, got %T", expr)
	}
	calls := make([]int64, 0)
	child := testQueryPlan{executeFn: func(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.Unix())
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: subquery.Range, Step: subquery.Step, Offset: subquery.OriginalOffset, Timestamp: cloneInt64Pointer(subquery.Timestamp), StartOrEnd: subquery.StartOrEnd, Child: child}

	value, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(999, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful timestamp+offset subquery execution, got error: %v", err)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected one series with three points, got %#v", matrix.Series)
	}
	expected := []int64{120, 180, 240}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d child evaluations, got %d", len(expected), len(calls))
	}
	for i := range expected {
		if calls[i] != expected[i] {
			t.Fatalf("expected child call %d at %d, got %d", i, expected[i], calls[i])
		}
	}
}

func TestLocalSubqueryPlanRejectsLocalRangeMode(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, Child: testQueryPlan{}}

	_, err := plan.execute(context.Background(), &evaluator{}, evalParams{Mode: evalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: time.Minute})
	if err == nil {
		t.Fatal("expected unsupported local range-mode subquery execution")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("expected not-implemented error, got %v", err)
	}
}

type testQueryPlan struct {
	executeFn func(context.Context, *evaluator, evalParams) (model.RuntimeValue, error)
}

func (p testQueryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	if p.executeFn == nil {
		return model.VectorValue{}, nil
	}
	return p.executeFn(ctx, evaluator, params)
}

func (p testQueryPlan) explain() ExplainNode {
	return ExplainNode{Kind: "test", Strategy: "local", Expr: "test"}
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
