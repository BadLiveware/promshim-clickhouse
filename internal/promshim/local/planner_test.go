package local

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
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

func TestBuildPlanCreatesNativeSubqueryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("(up * 100)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected subquery plan, got error: %v", err)
	}
	subquery, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if subquery.Fragment == nil || subquery.Fragment.Kind != nativeplan.FragmentKindSubquery || subquery.Fragment.Subquery == nil {
		t.Fatalf("expected native subquery fragment, got %#v", subquery)
	}
	if subquery.Expr != "(up * 100)[5m:30s]" {
		t.Fatalf("expected subquery expression to be preserved, got %q", subquery.Expr)
	}
}

func TestBuildPlanCreatesNativeSubqueryPlanWithAggregationChild(t *testing.T) {
	expr, err := plan.ParseExpression("sum(up)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected subquery plan, got error: %v", err)
	}
	subquery, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if subquery.Fragment == nil || subquery.Fragment.Kind != nativeplan.FragmentKindSubquery || subquery.Fragment.Subquery == nil {
		t.Fatalf("expected native subquery fragment, got %#v", subquery)
	}
	if subquery.Fragment.Subquery.Child == nil || subquery.Fragment.Subquery.Child.Kind != nativeplan.FragmentKindAggregation {
		t.Fatalf("expected native aggregation child inside subquery, got %#v", subquery.Fragment.Subquery.Child)
	}
}

func TestResolveDelegatedPromQLRewritesAtStartEndForRange(t *testing.T) {
	expr, err := plan.ParseExpression("up @ start() + up @ end()")
	if err != nil {
		t.Fatal(err)
	}

	promQL, err := resolveDelegatedPromQL(expr, EvalParams{
		Mode:  EvalModeRange,
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
	promQL, err := resolveDelegatedPromQL(expr, EvalParams{Mode: EvalModeInstant, EvaluationTime: evalTime})
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

	promQL, err := resolveDelegatedPromQL(expr, EvalParams{Mode: EvalModeRange, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC(), Step: time.Minute})
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

func TestBuildPlanCreatesCountValuesAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression(`count_values("sample_value", up)`)
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected count_values aggregation plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if agg.Op != parser.COUNT_VALUES {
		t.Fatalf("expected count_values op, got %v", agg.Op)
	}
	if agg.ParamString != "sample_value" {
		t.Fatalf("expected count_values label parameter, got %#v", agg.ParamString)
	}
}

func TestBuildPlanCreatesLimitKAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("limitk(2, up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected limitk aggregation plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if agg.Op != parser.LIMITK {
		t.Fatalf("expected limitk op, got %v", agg.Op)
	}
	if agg.ParamNumber == nil || *agg.ParamNumber != 2 {
		t.Fatalf("expected limitk parameter 2, got %#v", agg.ParamNumber)
	}
}

func TestBuildPlanCreatesLimitRatioAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("limit_ratio(0.5, up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected limit_ratio aggregation plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if agg.Op != parser.LIMIT_RATIO {
		t.Fatalf("expected limit_ratio op, got %v", agg.Op)
	}
	if agg.ParamNumber == nil || *agg.ParamNumber != 0.5 {
		t.Fatalf("expected limit_ratio parameter 0.5, got %#v", agg.ParamNumber)
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
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "histogram_quantile" {
		t.Fatalf("expected histogram_quantile native plan, got %#v", nativePlan)
	}
	if len(nativePlan.Children) != 1 || nativePlan.Children[0].Kind != "aggregation" {
		t.Fatalf("expected aggregation child under histogram_quantile native plan, got %#v", nativePlan.Children)
	}
}

func TestBuildPlanCreatesHistogramQuantilesPlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_quantiles(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])), \"quantile\", 0.5, scalar(sum(up)))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected histogram_quantiles plan, got error: %v", err)
	}
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "histogram_quantiles" {
		t.Fatalf("expected histogram_quantiles native plan, got %#v", nativePlan)
	}
	if len(nativePlan.Children) != 3 {
		t.Fatalf("expected histogram and scalar children under histogram_quantiles native plan, got %#v", nativePlan.Children)
	}
}

func TestBuildPlanCreatesHistogramProjectionPlan(t *testing.T) {
	for _, fn := range []string{"histogram_count", "histogram_sum", "histogram_avg", "histogram_stddev", "histogram_stdvar"} {
		t.Run(fn, func(t *testing.T) {
			expr, err := plan.ParseExpression(fn + "(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
			if err != nil {
				t.Fatal(err)
			}

			execPlan, err := buildPlan(expr)
			if err != nil {
				t.Fatalf("expected histogram projection plan, got error: %v", err)
			}
			nativePlan, ok := execPlan.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
			}
			if nativePlan.Kind != fn {
				t.Fatalf("expected %s native plan, got %#v", fn, nativePlan)
			}
			if len(nativePlan.Children) != 1 || nativePlan.Children[0].Kind != "aggregation" {
				t.Fatalf("expected aggregation child under %s native plan, got %#v", fn, nativePlan.Children)
			}
		})
	}
}

func TestBuildPlanCreatesDirectHistogramProjectionNativePlan(t *testing.T) {
	for _, fn := range []string{"histogram_count", "histogram_sum", "histogram_avg", "histogram_stddev", "histogram_stdvar"} {
		t.Run(fn, func(t *testing.T) {
			expr, err := plan.ParseExpression(fn + `(http_request_duration_seconds_bucket{job="api"})`)
			if err != nil {
				t.Fatal(err)
			}

			execPlan, err := buildPlan(expr)
			if err != nil {
				t.Fatalf("expected direct histogram projection native plan, got error: %v", err)
			}
			nativePlan, ok := execPlan.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
			}
			if nativePlan.Kind != fn {
				t.Fatalf("expected %s native plan, got %#v", fn, nativePlan)
			}
		})
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
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "histogram_fraction" {
		t.Fatalf("expected histogram_fraction native plan, got %#v", nativePlan)
	}
	if len(nativePlan.Children) != 1 || nativePlan.Children[0].Kind != "aggregation" {
		t.Fatalf("expected aggregation child under histogram_fraction native plan, got %#v", nativePlan.Children)
	}
}

func TestBuildPlanCreatesNativeRatePlanForDirectSelector(t *testing.T) {
	for _, fn := range []string{"rate", "irate"} {
		expr, err := plan.ParseExpression(fn + "(up[5m])")
		if err != nil {
			t.Fatal(err)
		}
		execPlan, err := buildPlan(expr)
		if err != nil {
			t.Fatalf("expected %s plan, got error: %v", fn, err)
		}
		ratePlan, ok := execPlan.(*nativeSubtreePlan)
		if !ok {
			t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
		}
		if ratePlan.Fragment == nil || ratePlan.Fragment.RangeFunction == nil || ratePlan.Fragment.RangeFunction.Func != fn {
			t.Fatalf("expected native %s fragment, got %#v", fn, ratePlan)
		}
	}
}

func TestBuildPlanCreatesNativeIncreasePlan(t *testing.T) {
	expr, err := plan.ParseExpression("increase(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected increase plan, got error: %v", err)
	}
	increasePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if increasePlan.Fragment == nil || increasePlan.Fragment.RangeFunction == nil || increasePlan.Fragment.RangeFunction.Func != "increase" {
		t.Fatalf("expected native increase fragment, got %#v", increasePlan)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeFunctionPlanInRangeModeForDirectSelector(t *testing.T) {
	for _, query := range []string{"sum_over_time(up[5m])", "rate(up[5m])", "increase(up[5m])", "changes(up[5m])", "resets(up[5m])", "quantile_over_time(0.95, up[5m])", "first_over_time(up[5m])", "ts_of_first_over_time(up[5m])", "ts_of_last_over_time(up[5m])", "ts_of_max_over_time(up[5m])", "ts_of_min_over_time(up[5m])"} {
		expr, err := plan.ParseExpression(query)
		if err != nil {
			t.Fatal(err)
		}

		execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
		if err != nil {
			t.Fatalf("expected native range plan for %q, got error: %v", query, err)
		}
		rangePlan, ok := execPlan.(*nativeSubtreePlan)
		if !ok {
			t.Fatalf("expected nativeSubtreePlan for %q, got %T", query, execPlan)
		}
		if rangePlan.Fragment == nil || rangePlan.Fragment.RangeFunction == nil {
			t.Fatalf("expected native range function fragment for %q, got %#v", query, rangePlan)
		}
	}
}

func TestBuildPlanWithContextCreatesNativeRangeFunctionPlanInRangeModeForSubquery(t *testing.T) {
	for _, query := range []string{"sum_over_time(sum(up)[5m:1m])", "rate(sum(up)[5m:1m])", "increase(sum(up)[5m:1m])", "changes(sum(up)[5m:1m])", "resets(sum(up)[5m:1m])", "quantile_over_time(0.95, sum(up)[5m:1m])", "first_over_time(sum(up)[5m:1m])", "ts_of_first_over_time(sum(up)[5m:1m])", "ts_of_last_over_time(sum(up)[5m:1m])", "ts_of_max_over_time(sum(up)[5m:1m])", "ts_of_min_over_time(sum(up)[5m:1m])"} {
		expr, err := plan.ParseExpression(query)
		if err != nil {
			t.Fatal(err)
		}

		execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
		if err != nil {
			t.Fatalf("expected native range subquery plan for %q, got error: %v", query, err)
		}
		rangePlan, ok := execPlan.(*nativeSubtreePlan)
		if !ok {
			t.Fatalf("expected nativeSubtreePlan for %q, got %T", query, execPlan)
		}
		if rangePlan.Fragment == nil || rangePlan.Fragment.RangeFunction == nil {
			t.Fatalf("expected native range function fragment for %q, got %#v", query, rangePlan)
		}
		if rangePlan.Fragment.RangeFunction.Child == nil || rangePlan.Fragment.RangeFunction.Child.Subquery == nil {
			t.Fatalf("expected native subquery child for %q, got %#v", query, rangePlan.Fragment)
		}
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
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Fragment == nil || nativePlan.Kind != "vector" {
		t.Fatalf("expected native vector plan, got %#v", nativePlan)
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
	if nativePlan, ok := execPlan.(*nativeSubtreePlan); ok {
		if nativePlan.Fragment == nil || nativePlan.Fragment.Kind != nativeplan.FragmentKindValueTransform {
			t.Fatalf("expected value-transform native round fragment, got %#v", nativePlan)
		}
		return
	}
	roundPlan, ok := execPlan.(*localRoundPlan)
	if !ok {
		t.Fatalf("expected round plan, got %T", execPlan)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "last_over_time" {
		t.Fatalf("expected native last_over_time fragment, got %#v", rangeFn)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "sum_over_time" {
		t.Fatalf("expected native sum_over_time fragment, got %#v", rangeFn)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "avg_over_time" {
		t.Fatalf("expected native avg_over_time fragment, got %#v", rangeFn)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "max_over_time" {
		t.Fatalf("expected native max_over_time fragment, got %#v", rangeFn)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "min_over_time" {
		t.Fatalf("expected native min_over_time fragment, got %#v", rangeFn)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "count_over_time" {
		t.Fatalf("expected native count_over_time fragment, got %#v", rangeFn)
	}
}

func TestBuildPlanCreatesNativeRangeFunctionPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("sum_over_time((up * 100)[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected native sum_over_time subquery plan, got error: %v", err)
	}
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Child == nil || rangeFn.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native range function over subquery fragment, got %#v", rangeFn)
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
	rangeFn, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if rangeFn.Fragment == nil || rangeFn.Fragment.RangeFunction == nil || rangeFn.Fragment.RangeFunction.Func != "quantile_over_time" {
		t.Fatalf("expected native quantile_over_time fragment, got %#v", rangeFn)
	}
	if rangeFn.Fragment.RangeFunction.ParamNumber == nil || *rangeFn.Fragment.RangeFunction.ParamNumber != 0.95 {
		t.Fatalf("expected quantile parameter on native fragment, got %#v", rangeFn.Fragment.RangeFunction)
	}
	if rangeFn.Fragment.RangeFunction.Child == nil || rangeFn.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native quantile_over_time subquery child, got %#v", rangeFn.Fragment.RangeFunction)
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
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "absent" {
		t.Fatalf("unexpected absent native plan: %#v", nativePlan)
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
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "absent_over_time" {
		t.Fatalf("unexpected absent_over_time native plan: %#v", nativePlan)
	}
}

func TestBuildPlanCreatesNestedMatrixFunctionBinaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum_over_time((up * 100)[5m:30s]) + count_over_time((up * 100)[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected nested matrix binary native plan, got error: %v", err)
	}
	binaryPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if binaryPlan.Fragment == nil || binaryPlan.Fragment.BinaryJoin == nil {
		t.Fatalf("expected native binary join fragment, got %#v", binaryPlan)
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
	native, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan (subquery child accepts range-function fragments), got %T", execPlan)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindRangeFunction {
		t.Fatalf("expected outer range-function fragment, got %#v", native.Fragment)
	}
	outerSubquery := native.Fragment.RangeFunction.Child
	if outerSubquery == nil || outerSubquery.Kind != nativeplan.FragmentKindSubquery || outerSubquery.Subquery == nil {
		t.Fatalf("expected outer subquery fragment, got %#v", outerSubquery)
	}
	if outerSubquery.Subquery.Child == nil || outerSubquery.Subquery.Child.Kind != nativeplan.FragmentKindRangeFunction {
		t.Fatalf("expected subquery child to be range-function fragment, got %#v", outerSubquery.Subquery.Child)
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
	var buildErr *PlanBuildError
	if !errors.As(err, &buildErr) {
		t.Fatalf("expected PlanBuildError, got %T (%v)", err, err)
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
	var buildErr *PlanBuildError
	if !errors.As(err, &buildErr) {
		t.Fatalf("expected PlanBuildError, got %T (%v)", err, err)
	}
	if !strings.Contains(buildErr.Support.Reason, "literal scalar lower bound") {
		t.Fatalf("unexpected reason for histogram_fraction bound rejection: %#v", buildErr.Support)
	}
}

func TestDecideNativeAggregationPushdownAllowsDelegatedLeaf(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "sum by (job) (up)"))
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := logical.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", logical)
	}

	decision := decideNativeAggregationPushdown(agg, PlanContext{PreferNativeAggregationPushdown: true})
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

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	if _, ok := plan.(*nativeSubtreePlan); !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", plan)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationPlanForUnaryTransformedLeaf(t *testing.T) {
	expr, err := plan.ParseExpression("avg by (job) (-up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	native, ok := plan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", plan)
	}
	if native.Fragment == nil || native.Fragment.Aggregation == nil || native.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native subtree fragment source metadata, got %#v", native)
	}
	source := native.Fragment.Aggregation.Source
	if !strings.Contains(source.ValueExpr, "-") {
		t.Fatalf("expected unary value transform in native source, got %#v", source)
	}
	if !strings.Contains(source.TagsExpr, "__name__") {
		t.Fatalf("expected metric-name dropping tags transform, got %#v", source)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationPlanForVectorScalarLeaf(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up * 100)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	native, ok := plan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", plan)
	}
	if native.Fragment == nil || native.Fragment.Aggregation == nil || native.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native subtree fragment source metadata, got %#v", native)
	}
	source := native.Fragment.Aggregation.Source
	if !strings.Contains(source.ValueExpr, "100") || !strings.Contains(source.ValueExpr, "*") {
		t.Fatalf("expected vector-scalar arithmetic in native source, got %#v", source)
	}
	if !strings.Contains(source.TagsExpr, "__name__") {
		t.Fatalf("expected metric-name dropping tags transform, got %#v", source)
	}
}

func TestBuildPlanWithContextCreatesLocalPlanForInfo(t *testing.T) {
	expr, err := plan.ParseExpression("info(up)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, NativeLoweringMode: NativeLoweringModeOff})
	if err != nil {
		t.Fatalf("expected local info() plan, got error: %v", err)
	}
	if _, ok := built.(*localInfoPlan); !ok {
		t.Fatalf("expected localInfoPlan, got %T", built)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForInfo(t *testing.T) {
	expr, err := plan.ParseExpression("info(up)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native info() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindInfoJoin || native.Fragment.InfoJoin == nil {
		t.Fatalf("expected info join fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForInfoRegexMetricSelector(t *testing.T) {
	expr, err := plan.ParseExpression("info(up, {__name__=~\".+_info\"})")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native regex info() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindInfoJoin || native.Fragment.InfoJoin == nil {
		t.Fatalf("expected info join fragment, got %#v", native)
	}
	if native.Fragment.InfoJoin.InfoMetricName != "" {
		t.Fatalf("expected matcher-driven info selector without fixed metric name, got %#v", native.Fragment.InfoJoin)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForRootSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression(`sum(up)[5m:1m]`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native root-subquery plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindSubquery || native.Fragment.Subquery == nil {
		t.Fatalf("expected root subquery fragment, got %#v", native)
	}
	if native.Fragment.Subquery.Child == nil || native.Fragment.Subquery.Child.Kind != nativeplan.FragmentKindAggregation {
		t.Fatalf("expected native aggregation child under root subquery, got %#v", native.Fragment.Subquery.Child)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForLabelJoinRootSubquery(t *testing.T) {
	expr, err := plan.ParseExpression(`label_join(up, "joined", "/", "job", "namespace")[5m:1m]`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), NativeLoweringMode: NativeLoweringModePrefer})
	if err != nil {
		t.Fatalf("expected native root-subquery plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindSubquery || native.Fragment.Subquery == nil || native.Fragment.Subquery.Child == nil {
		t.Fatalf("expected subquery fragment, got %#v", native)
	}
	if native.Fragment.Subquery.Child.Kind != nativeplan.FragmentKindLabelTransform {
		t.Fatalf("expected native label transform child under root subquery, got %#v", native.Fragment.Subquery.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeRangePlanForAnchoredPointwiseFunction(t *testing.T) {
	expr, err := plan.ParseExpression("abs(up @ 1710000000)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModePrefer})
	if err != nil {
		t.Fatalf("expected native anchored pointwise plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindUnarySourceExpr {
		t.Fatalf("expected anchored unary native fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForPointwiseFunction(t *testing.T) {
	expr, err := plan.ParseExpression("abs(up)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native pointwise plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindUnarySourceExpr || !strings.Contains(native.Fragment.ValueExpr, "abs") {
		t.Fatalf("expected abs native fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeAggregationForAnchoredSelector(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up @ 1710000000)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native-capable anchored aggregation plan, got error: %v", err)
	}
	nativeRoot, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", built)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindAggregation || nativeRoot.Fragment.Aggregation == nil {
		t.Fatalf("expected native aggregation root, got %#v", nativeRoot)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeAggregationForStartEndAnchors(t *testing.T) {
	testCases := []string{
		"sum by (job) (up @ start())",
		"sum by (job) (up @ end())",
	}
	for _, query := range testCases {
		t.Run(query, func(t *testing.T) {
			expr, err := plan.ParseExpression(query)
			if err != nil {
				t.Fatal(err)
			}

			built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
			if err != nil {
				t.Fatalf("expected native-capable anchored aggregation plan, got error: %v", err)
			}
			nativeRoot, ok := built.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan root, got %T", built)
			}
			if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindAggregation || nativeRoot.Fragment.Aggregation == nil {
				t.Fatalf("expected native aggregation root, got %#v", nativeRoot)
			}
		})
	}
}

func TestBuildPlanWithContextCreatesNativePlanForScalarConvert(t *testing.T) {
	expr, err := plan.ParseExpression("scalar(up)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native scalar() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindScalarConvert || native.Fragment.ScalarConvert == nil {
		t.Fatalf("expected scalar convert fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesLocalPlanForPredictLinear(t *testing.T) {
	expr, err := plan.ParseExpression("predict_linear(up[5m], 60)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, NativeLoweringMode: NativeLoweringModeOff})
	if err != nil {
		t.Fatalf("expected local predict_linear plan, got error: %v", err)
	}
	local, ok := built.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", built)
	}
	if local.Func != "predict_linear" || local.ParamNumber == nil || *local.ParamNumber != 60 {
		t.Fatalf("unexpected predict_linear plan: %#v", local)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForPredictLinear(t *testing.T) {
	expr, err := plan.ParseExpression("predict_linear(up[5m], 60)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native predict_linear plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindRangeFunction || native.Fragment.RangeFunction == nil || native.Fragment.RangeFunction.Func != "predict_linear" {
		t.Fatalf("expected predict_linear range fragment, got %#v", native)
	}
	if native.Fragment.RangeFunction.ParamNumber == nil || *native.Fragment.RangeFunction.ParamNumber != 60 {
		t.Fatalf("expected predict_linear duration on native fragment, got %#v", native.Fragment.RangeFunction)
	}
}

func TestBuildPlanWithContextCreatesLocalPlanForDoubleExponentialSmoothing(t *testing.T) {
	expr, err := plan.ParseExpression("double_exponential_smoothing(up[5m], 0.5, 0.3)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, NativeLoweringMode: NativeLoweringModeOff})
	if err != nil {
		t.Fatalf("expected local smoothing plan, got error: %v", err)
	}
	local, ok := built.(*localRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected localRangeFunctionPlan, got %T", built)
	}
	if local.Func != "double_exponential_smoothing" || len(local.ParamNumbers) != 2 || *local.ParamNumbers[0] != 0.5 || *local.ParamNumbers[1] != 0.3 {
		t.Fatalf("unexpected smoothing plan: %#v", local)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForDoubleExponentialSmoothing(t *testing.T) {
	expr, err := plan.ParseExpression("double_exponential_smoothing(up[5m], 0.5, 0.3)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native smoothing plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindRangeFunction || native.Fragment.RangeFunction == nil || native.Fragment.RangeFunction.Func != "double_exponential_smoothing" {
		t.Fatalf("expected smoothing range fragment, got %#v", native)
	}
	if len(native.Fragment.RangeFunction.ParamNumbers) != 2 || *native.Fragment.RangeFunction.ParamNumbers[0] != 0.5 || *native.Fragment.RangeFunction.ParamNumbers[1] != 0.3 {
		t.Fatalf("expected smoothing params on native fragment, got %#v", native.Fragment.RangeFunction)
	}
}

func TestBuildPlanWithContextNormalizesHoltWintersAlias(t *testing.T) {
	expr, err := plan.ParseExpression("holt_winters(up[5m], 0.5, 0.3)")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native alias plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.RangeFunction == nil || native.Fragment.RangeFunction.Func != "double_exponential_smoothing" {
		t.Fatalf("expected holt_winters alias to normalize to double_exponential_smoothing, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForSyntheticDateFunction(t *testing.T) {
	expr, err := plan.ParseExpression("minute()")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native minute() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindSyntheticSeries || native.Fragment.Synthetic == nil || native.Fragment.Synthetic.Func != "minute" {
		t.Fatalf("expected synthetic minute() fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForSortByLabel(t *testing.T) {
	expr, err := plan.ParseExpression("sort_by_label(up, \"instance\", \"job\")")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native sort_by_label() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindSortTransform || native.Fragment.SortTransform == nil || len(native.Fragment.SortTransform.Labels) != 2 {
		t.Fatalf("expected sort transform fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForClampWithScalarParameterChild(t *testing.T) {
	expr, err := plan.ParseExpression("clamp_min(up, scalar(sum(up)))")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native clamp_min() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindClampTransform || native.Fragment.ClampTransform == nil || native.Fragment.ClampTransform.Min == nil {
		t.Fatalf("expected clamp transform fragment, got %#v", native)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForScalarBuiltin(t *testing.T) {
	expr, err := plan.ParseExpression("time()")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native time() plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if native.Fragment == nil || native.Fragment.Kind != nativeplan.FragmentKindSyntheticSeries || native.Fragment.Synthetic == nil || native.Fragment.Synthetic.Func != "time" {
		t.Fatalf("expected synthetic time() fragment, got %#v", native)
	}
}

func TestDecideNativeAggregationPushdownAllowsLabelMutationChild(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, `sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`))
	if err != nil {
		t.Fatal(err)
	}
	agg, ok := logical.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", logical)
	}

	decision := decideNativeAggregationPushdown(agg, PlanContext{PreferNativeAggregationPushdown: true})
	if !decision.Eligible {
		t.Fatalf("expected eligible pushdown, got %#v", decision)
	}
	if decision.Source.PromQLLeaf == nil || decision.Source.PromQLLeaf.String() != "up" {
		t.Fatalf("expected delegated-safe leaf source metadata, got %#v", decision)
	}
}

func TestDecideNativeAggregationPushdownRejectsWhenDisabled(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "sum by (job) (up)"))
	if err != nil {
		t.Fatal(err)
	}
	agg := logical.(*logicalAggregationPlan)
	decision := decideNativeAggregationPushdown(agg, PlanContext{PreferNativeAggregationPushdown: false})
	if decision.Eligible {
		t.Fatalf("expected disabled pushdown to be rejected, got %#v", decision)
	}
	if !strings.Contains(decision.Reason, "disabled") {
		t.Fatalf("expected disabled reason, got %#v", decision)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationForLabelMutationChild(t *testing.T) {
	expr, err := plan.ParseExpression(`sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	nativeRoot, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindAggregation || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindLabelTransform {
		t.Fatalf("expected label-transform aggregation source, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestExplainPlanDescribesNativeAggregationStrategy(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "native_sql" {
		t.Fatalf("expected native_sql strategy, got %#v", explain)
	}
	if explain.SelectedStrategy != "native_sql" || explain.NativeScope != "subtree" {
		t.Fatalf("expected selected native subtree strategy metadata, got %#v", explain)
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
	if len(explain.RulesApplied) == 0 || explain.RenderedSQL == "" {
		t.Fatalf("expected optimizer metadata and rendered SQL on native explain, got %#v", explain)
	}
	if !containsString(explain.InferredPredicates, `__name__="up"`) {
		t.Fatalf("expected inferred metric-name predicate on native explain, got %#v", explain.InferredPredicates)
	}
	if !containsString(explain.PushedPredicates, `__name__="up"`) {
		t.Fatalf("expected pushed metric-name predicate on native explain, got %#v", explain.PushedPredicates)
	}
	if !containsString(explain.RequiredColumns, "tags") || !containsString(explain.MaterializedColumns, "time_series") {
		t.Fatalf("expected projection/materialization metadata on native explain, got required=%#v materialized=%#v", explain.RequiredColumns, explain.MaterializedColumns)
	}
	if !containsString(explain.SemanticBarriers, "aggregation_boundary") {
		t.Fatalf("expected semantic barrier metadata on native explain, got %#v", explain.SemanticBarriers)
	}
	if strings.Contains(strings.ToUpper(explain.RenderedSQL), "SELECT *") {
		t.Fatalf("expected final rendered SQL to avoid SELECT *, got %q", explain.RenderedSQL)
	}
}

func TestExplainPlanDescribesNativeTransformedAggregationStrategy(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up * 100)")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
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

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeInstant,
		EvaluationTime:                  time.Unix(300, 0).UTC(),
		PreferNativeAggregationPushdown: false,
	})
	if err != nil {
		t.Fatalf("expected local aggregation plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "local" {
		t.Fatalf("expected local strategy, got %#v", explain)
	}
	if explain.SelectedStrategy != "local" || explain.NativeScope != "none" {
		t.Fatalf("expected local selected strategy metadata, got %#v", explain)
	}
	if !strings.Contains(explain.Reason, "disabled") || !strings.Contains(explain.FallbackReason, "disabled") {
		t.Fatalf("expected disabled fallback reason, got %#v", explain)
	}
}

func TestExplainPlanDescribesNativeAggregationStrategyForLabelMutationChild(t *testing.T) {
	expr, err := plan.ParseExpression(`sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`)
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native aggregation plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "native_sql" || explain.SelectedStrategy != "native_sql" || explain.NativeScope != "subtree" {
		t.Fatalf("expected native subtree strategy, got %#v", explain)
	}
	if explain.Lowering == nil || !explain.Lowering.AggregationPushdownEligible {
		t.Fatalf("expected eligible aggregation lowering metadata, got %#v", explain.Lowering)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "native_sql_expression" {
		t.Fatalf("expected native label-transform child explain, got %#v", explain.Children)
	}
	if len(explain.Children[0].Children) != 1 || explain.Children[0].Children[0].Strategy != "delegated_promql" {
		t.Fatalf("expected delegated leaf under native label-transform explain, got %#v", explain.Children)
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
	binaryPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if binaryPlan.Fragment == nil || binaryPlan.Fragment.BinaryJoin == nil {
		t.Fatalf("expected native binary join fragment, got %#v", binaryPlan)
	}
	if binaryPlan.Fragment.BinaryJoin.VectorMatching == nil || binaryPlan.Fragment.BinaryJoin.VectorMatching.Card != parser.CardManyToOne {
		t.Fatalf("expected many-to-one vector matching card, got %#v", binaryPlan.Fragment.BinaryJoin)
	}
	if !binaryPlan.Fragment.BinaryJoin.VectorMatching.On || !sameStrings(binaryPlan.Fragment.BinaryJoin.VectorMatching.MatchingLabels, []string{"job"}) {
		t.Fatalf("unexpected vector matching labels: %#v", binaryPlan.Fragment.BinaryJoin.VectorMatching)
	}
}

func TestExplainPlanDescribesNativeVectorMatchingBinaryStrategy(t *testing.T) {
	expr, err := plan.ParseExpression("up * on(job) group_left sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), PreferNativeAggregationPushdown: true})
	if err != nil {
		t.Fatalf("expected native vector matching plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(execPlan, analysis.Root)
	if explain.Strategy != "native_sql" || explain.Kind != "binary" {
		t.Fatalf("expected native binary strategy, got %#v", explain)
	}
	if explain.JoinShape != nativeplan.JoinShapeManyToOne || !sameStrings(explain.JoinLabels, []string{"job"}) {
		t.Fatalf("expected join explain metadata, got %#v", explain)
	}
	if explain.RenderedSQL == "" || !strings.Contains(explain.RenderedSQL, "join_group") {
		t.Fatalf("expected rendered join SQL with join_group, got %#v", explain)
	}
	if len(explain.Children) != 2 {
		t.Fatalf("expected binary explain children, got %#v", explain.Children)
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
	if nativePlan, ok := execPlan.(*nativeSubtreePlan); ok {
		if nativePlan.Fragment == nil || nativePlan.Fragment.Kind != nativeplan.FragmentKindBinaryVectorJoin || nativePlan.Fragment.BinaryJoin == nil {
			t.Fatalf("expected native binary join fragment, got %#v", nativePlan)
		}
		if nativePlan.Fragment.BinaryJoin.Op != parser.LAND || nativePlan.Fragment.BinaryJoin.JoinShape != nativeplan.JoinShapeManyToMany {
			t.Fatalf("expected many-to-many LAND native join, got %#v", nativePlan.Fragment.BinaryJoin)
		}
		return
	}
	binaryPlan, ok := execPlan.(*localBinaryPlan)
	if !ok {
		t.Fatalf("expected set operator plan, got %T", execPlan)
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
	if nativePlan, ok := plan.(*nativeSubtreePlan); ok {
		if nativePlan.Fragment == nil || nativePlan.Fragment.Kind != nativeplan.FragmentKindUnarySourceExpr {
			t.Fatalf("expected unary source native fragment, got %#v", nativePlan)
		}
		return
	}
	if _, ok := plan.(*localUnaryPlan); !ok {
		t.Fatalf("expected unary plan, got %T", plan)
	}
}

func TestBuildPlanCreatesNativeLabelReplacePlan(t *testing.T) {
	expr, err := plan.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected label_replace plan, got error: %v", err)
	}
	nativeRoot, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindLabelTransform || nativeRoot.Fragment.LabelTransform == nil || nativeRoot.Fragment.LabelTransform.Func != "label_replace" {
		t.Fatalf("expected native label_replace fragment, got %#v", nativeRoot)
	}
}

func TestBuildPlanCreatesNativeLabelJoinPlan(t *testing.T) {
	expr, err := plan.ParseExpression(`label_join(up, "joined", "/", "job", "namespace")`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected label_join plan, got error: %v", err)
	}
	nativeRoot, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", built)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindLabelTransform || nativeRoot.Fragment.LabelTransform == nil || nativeRoot.Fragment.LabelTransform.Func != "label_join" {
		t.Fatalf("expected native label_join fragment, got %#v", nativeRoot)
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
	var buildErr *PlanBuildError
	if !errors.As(err, &buildErr) {
		t.Fatalf("expected PlanBuildError, got %T (%v)", err, err)
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

func TestBuildPlanBuildsNativeIncreasePlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("increase(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected native plan for increase subquery, got error: %v", err)
	}
	increasePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if increasePlan.Fragment == nil || increasePlan.Fragment.RangeFunction == nil || increasePlan.Fragment.RangeFunction.Func != "increase" {
		t.Fatalf("expected native increase fragment, got %#v", increasePlan)
	}
	if increasePlan.Fragment.RangeFunction.Child == nil || increasePlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native increase subquery child, got %#v", increasePlan.Fragment)
	}
}

func TestBuildPlanBuildsNativeDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("delta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected native plan for delta subquery, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if deltaPlan.Fragment == nil || deltaPlan.Fragment.RangeFunction == nil || deltaPlan.Fragment.RangeFunction.Func != "delta" {
		t.Fatalf("expected native delta fragment, got %#v", deltaPlan)
	}
	if deltaPlan.Fragment.RangeFunction.Child == nil || deltaPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native delta subquery child, got %#v", deltaPlan.Fragment)
	}
}

func TestBuildPlanBuildsNativeIDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("idelta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected native plan for idelta subquery, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if deltaPlan.Fragment == nil || deltaPlan.Fragment.RangeFunction == nil || deltaPlan.Fragment.RangeFunction.Func != "idelta" {
		t.Fatalf("expected native idelta fragment, got %#v", deltaPlan)
	}
	if deltaPlan.Fragment.RangeFunction.Child == nil || deltaPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native idelta subquery child, got %#v", deltaPlan.Fragment)
	}
}

func TestBuildPlanBuildsNativeChangesPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("changes(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected native plan for changes subquery, got error: %v", err)
	}
	changesPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if changesPlan.Fragment == nil || changesPlan.Fragment.RangeFunction == nil || changesPlan.Fragment.RangeFunction.Func != "changes" {
		t.Fatalf("expected native changes fragment, got %#v", changesPlan)
	}
	if changesPlan.Fragment.RangeFunction.Child == nil || changesPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native changes subquery child, got %#v", changesPlan.Fragment)
	}
}

func TestBuildPlanBuildsNativeDerivPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("deriv(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected native plan for deriv subquery, got error: %v", err)
	}
	derivPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if derivPlan.Fragment == nil || derivPlan.Fragment.RangeFunction == nil || derivPlan.Fragment.RangeFunction.Func != "deriv" {
		t.Fatalf("expected native deriv fragment, got %#v", derivPlan)
	}
	if derivPlan.Fragment.RangeFunction.Child == nil || derivPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native deriv subquery child, got %#v", derivPlan.Fragment)
	}
}

func TestBuildPlanBuildsNativeRatePlanForSubqueryArg(t *testing.T) {
	for _, fn := range []string{"rate", "irate"} {
		exprText := fmt.Sprintf("%s(sum(up)[5m:])", fn)
		expr, err := plan.ParseExpression(exprText)
		if err != nil {
			t.Fatalf("parse %q failed: %v", exprText, err)
		}
		execPlan, err := buildPlan(expr)
		if err != nil {
			t.Fatalf("expected native plan for %q, got error: %v", exprText, err)
		}
		ratePlan, ok := execPlan.(*nativeSubtreePlan)
		if !ok {
			t.Fatalf("expected nativeSubtreePlan for %q, got %T", exprText, execPlan)
		}
		if ratePlan.Fragment == nil || ratePlan.Fragment.RangeFunction == nil || ratePlan.Fragment.RangeFunction.Func != fn {
			t.Fatalf("expected native %q fragment, got %#v", fn, ratePlan)
		}
		if ratePlan.Fragment.RangeFunction.Child == nil || ratePlan.Fragment.RangeFunction.Child.Subquery == nil {
			t.Fatalf("expected native %q subquery child, got %#v", fn, ratePlan.Fragment)
		}
	}
}

func TestBuildPlanWithContextCreatesDeltaPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("delta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected native delta plan, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if deltaPlan.Fragment == nil || deltaPlan.Fragment.RangeFunction == nil || deltaPlan.Fragment.RangeFunction.Func != "delta" {
		t.Fatalf("expected native delta fragment, got %#v", deltaPlan)
	}
	if deltaPlan.Fragment.RangeFunction.Child == nil || deltaPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native subquery child, got %#v", deltaPlan.Fragment)
	}
}

func TestBuildPlanWithContextCreatesIDeltaPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("idelta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected native idelta plan, got error: %v", err)
	}
	deltaPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if deltaPlan.Fragment == nil || deltaPlan.Fragment.RangeFunction == nil || deltaPlan.Fragment.RangeFunction.Func != "idelta" {
		t.Fatalf("expected native idelta fragment, got %#v", deltaPlan)
	}
	if deltaPlan.Fragment.RangeFunction.Child == nil || deltaPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native subquery child, got %#v", deltaPlan.Fragment)
	}
}

func TestBuildPlanWithContextCreatesChangesPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("changes(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected native changes plan, got error: %v", err)
	}
	changesPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if changesPlan.Fragment == nil || changesPlan.Fragment.RangeFunction == nil || changesPlan.Fragment.RangeFunction.Func != "changes" {
		t.Fatalf("expected native changes fragment, got %#v", changesPlan)
	}
	if changesPlan.Fragment.RangeFunction.Child == nil || changesPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native subquery child, got %#v", changesPlan.Fragment)
	}
}

func TestBuildPlanWithContextCreatesDerivPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("deriv(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected native deriv plan, got error: %v", err)
	}
	derivPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if derivPlan.Fragment == nil || derivPlan.Fragment.RangeFunction == nil || derivPlan.Fragment.RangeFunction.Func != "deriv" {
		t.Fatalf("expected native deriv fragment, got %#v", derivPlan)
	}
	if derivPlan.Fragment.RangeFunction.Child == nil || derivPlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native subquery child, got %#v", derivPlan.Fragment)
	}
}

func TestBuildPlanWithContextCreatesRatePlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("rate(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected native rate plan, got error: %v", err)
	}
	ratePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if ratePlan.Fragment == nil || ratePlan.Fragment.RangeFunction == nil || ratePlan.Fragment.RangeFunction.Func != "rate" {
		t.Fatalf("expected native rate fragment, got %#v", ratePlan)
	}
	if ratePlan.Fragment.RangeFunction.Child == nil || ratePlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native subquery child, got %#v", ratePlan.Fragment)
	}
}

func TestBuildPlanWithContextCreatesIncreasePlanInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("increase(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected increase plan, got error: %v", err)
	}
	if _, ok := execPlan.(*nativeSubtreePlan); !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
}

func TestBuildPlanWithContextCreatesIncreasePlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("increase(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected native increase plan, got error: %v", err)
	}
	increasePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if increasePlan.Fragment == nil || increasePlan.Fragment.RangeFunction == nil || increasePlan.Fragment.RangeFunction.Func != "increase" {
		t.Fatalf("expected native increase fragment, got %#v", increasePlan)
	}
	if increasePlan.Fragment.RangeFunction.Child == nil || increasePlan.Fragment.RangeFunction.Child.Subquery == nil {
		t.Fatalf("expected native subquery child, got %#v", increasePlan.Fragment)
	}
}

func TestBuildPlanWithContextCreatesLocalAggregationOverIncreaseInRangeMode(t *testing.T) {
	expr, err := plan.ParseExpression("sum(increase(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, PreferNativeAggregationPushdown: true})
	if err != nil {
		t.Fatalf("expected aggregation over increase plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if _, ok := agg.Child.(*nativeSubtreePlan); !ok {
		t.Fatalf("expected nativeSubtreePlan child, got %T", agg.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverRateUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("sum(rate(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native root aggregation over rate, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindAggregation || nativeRoot.Fragment.Aggregation == nil {
		t.Fatalf("expected native aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Source == nil || nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindRangeFunction {
		t.Fatalf("expected rate child aggregation source, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeRootGroupedAggregationOverRateUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("sum by(job) (rate(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native grouped aggregation over rate, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil {
		t.Fatalf("expected native aggregation fragment, got %#v", nativeRoot)
	}
	if !sameStrings(nativeRoot.Fragment.Aggregation.Grouping, []string{"job"}) {
		t.Fatalf("expected grouping [job], got %#v", nativeRoot.Fragment.Aggregation)
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverIncreaseUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("sum(increase(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native aggregation over increase, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindRangeFunction {
		t.Fatalf("expected increase aggregation source, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverScaledRateUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("sum(8 * rate(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native aggregation over scaled rate, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindValueTransform {
		t.Fatalf("expected value-transform aggregation source, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeUnaryRootOverAggregatedRateUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("- sum(rate(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native unary root over aggregated rate, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindValueTransform || nativeRoot.Fragment.ValueTransform == nil || nativeRoot.Fragment.ValueTransform.Child == nil {
		t.Fatalf("expected value-transform unary fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.ValueTransform.Child.Kind != nativeplan.FragmentKindAggregation {
		t.Fatalf("expected aggregated child under unary wrapper, got %#v", nativeRoot.Fragment.ValueTransform.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeComparisonRootOverAggregatedIncreaseUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("sum by(job) (increase(up[5m])) > 0")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native comparison root over aggregated increase, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindValueTransform || nativeRoot.Fragment.ValueTransform == nil || nativeRoot.Fragment.ValueTransform.Child == nil {
		t.Fatalf("expected value-transform comparison fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.ValueTransform.FilterExpr == "" {
		t.Fatalf("expected comparison filter template, got %#v", nativeRoot.Fragment.ValueTransform)
	}
	if nativeRoot.Fragment.ValueTransform.Child.Kind != nativeplan.FragmentKindAggregation {
		t.Fatalf("expected aggregated child under comparison wrapper, got %#v", nativeRoot.Fragment.ValueTransform.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeScalarWrapperRootOverHistogramQuantileUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_quantile(0.5, sum by(le, job) (rate(http_request_duration_seconds_bucket[5m]))) * 1")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native scalar wrapper over histogram_quantile, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindValueTransform || nativeRoot.Fragment.ValueTransform == nil || nativeRoot.Fragment.ValueTransform.Child == nil {
		t.Fatalf("expected value-transform histogram wrapper, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.ValueTransform.Child.Kind != nativeplan.FragmentKindHistogramFunction {
		t.Fatalf("expected histogram function child under scalar wrapper, got %#v", nativeRoot.Fragment.ValueTransform.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeScalarWrapperRootOverRateRatioUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m]) * 1e3")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native scalar wrapper over rate ratio, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindValueTransform || nativeRoot.Fragment.ValueTransform == nil || nativeRoot.Fragment.ValueTransform.Child == nil {
		t.Fatalf("expected value-transform ratio wrapper, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.ValueTransform.Child.Kind != nativeplan.FragmentKindBinaryVectorJoin {
		t.Fatalf("expected binary-join child under scalar wrapper, got %#v", nativeRoot.Fragment.ValueTransform.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeRoundRootOverAggregatedRateRatioUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("round(sum(rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m])) by(pod))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native round root over aggregated rate ratio, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindValueTransform || nativeRoot.Fragment.ValueTransform == nil || nativeRoot.Fragment.ValueTransform.Child == nil {
		t.Fatalf("expected value-transform round fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.ValueTransform.Child.Kind != nativeplan.FragmentKindAggregation {
		t.Fatalf("expected aggregated child under round wrapper, got %#v", nativeRoot.Fragment.ValueTransform.Child)
	}
}

func TestBuildPlanWithContextCreatesNativeCountValuesRootUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression(`count_values("sample_value", up)`)
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native count_values root, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native count_values aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Op != parser.COUNT_VALUES || nativeRoot.Fragment.Aggregation.ParamString != "sample_value" {
		t.Fatalf("expected count_values aggregation metadata, got %#v", nativeRoot.Fragment.Aggregation)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindLeafSource {
		t.Fatalf("expected leaf child under count_values, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeLimitKRootUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("limitk(2, up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native limitk root, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Op != parser.LIMITK {
		t.Fatalf("expected native limitk aggregation fragment, got %#v", nativeRoot)
	}
}

func TestBuildPlanWithContextCreatesNativeLimitRatioRootUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("limit_ratio(0.5, up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native limit_ratio root, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Op != parser.LIMIT_RATIO {
		t.Fatalf("expected native limit_ratio aggregation fragment, got %#v", nativeRoot)
	}
}

func TestBuildPlanWithContextCreatesNativeSetOperatorRootsUnderForceSupported(t *testing.T) {
	for _, query := range []string{"up and on(job) up", "up or on(job) up", "up unless on(job) up"} {
		expr, err := plan.ParseExpression(query)
		if err != nil {
			t.Fatal(err)
		}
		execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
		if err != nil {
			t.Fatalf("expected native set-operator root for %q, got error: %v", query, err)
		}
		nativeRoot, ok := execPlan.(*nativeSubtreePlan)
		if !ok {
			t.Fatalf("expected nativeSubtreePlan root for %q, got %T", query, execPlan)
		}
		if nativeRoot.Fragment == nil || nativeRoot.Fragment.Kind != nativeplan.FragmentKindBinaryVectorJoin || nativeRoot.Fragment.BinaryJoin == nil {
			t.Fatalf("expected native binary join fragment for %q, got %#v", query, nativeRoot)
		}
		if nativeRoot.Fragment.BinaryJoin.JoinShape != nativeplan.JoinShapeManyToMany {
			t.Fatalf("expected many-to-many join shape for %q, got %#v", query, nativeRoot.Fragment.BinaryJoin)
		}
		if nativeRoot.Fragment.BinaryJoin.Op.String() != mustParseExpr(t, query).(*parser.BinaryExpr).Op.String() {
			t.Fatalf("expected matching set op for %q, got %#v", query, nativeRoot.Fragment.BinaryJoin)
		}
	}
}

func TestBuildPlanWithContextCreatesNativeTopKRootOverIRateUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("topk(5, irate(up[1m]))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native topk root over irate, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native topk aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Op != parser.TOPK {
		t.Fatalf("expected topk aggregation op, got %#v", nativeRoot.Fragment.Aggregation)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindRangeFunction {
		t.Fatalf("expected irate child under topk, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeTopKRootOverHistogramQuantileUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("topk(2, histogram_quantile(0.9, sum(rate(http_request_duration_seconds_bucket[5m])) by(le)))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native topk root over histogram quantile, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native topk aggregation fragment, got %#v", nativeRoot)
	}
	if nativeRoot.Fragment.Aggregation.Op != parser.TOPK {
		t.Fatalf("expected topk aggregation op, got %#v", nativeRoot.Fragment.Aggregation)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindHistogramFunction {
		t.Fatalf("expected histogram quantile child under topk, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestBuildPlanWithContextCreatesNativeSumOverOrVectorZeroUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression("sum(rate(up[5m]) or vector(0))")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native sum over or-vector-zero, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Fragment == nil || nativeRoot.Fragment.Aggregation == nil || nativeRoot.Fragment.Aggregation.Source == nil {
		t.Fatalf("expected native aggregation fragment, got %#v", nativeRoot)
	}
	if !nativeRoot.Fragment.Aggregation.EmitZeroOnEmpty {
		t.Fatalf("expected zero-fill aggregation flag, got %#v", nativeRoot.Fragment.Aggregation)
	}
	if nativeRoot.Fragment.Aggregation.Source.Kind != nativeplan.FragmentKindRangeFunction {
		t.Fatalf("expected rate child under zero-fill aggregation, got %#v", nativeRoot.Fragment.Aggregation.Source)
	}
}

func TestLocalSubqueryPlanExecutesChildAcrossInstantWindow(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	calls := make([]time.Time, 0)
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.UTC())
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
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

func TestScalarLiteralPlanRangeMode(t *testing.T) {
	plan := &scalarLiteralPlan{Expr: "1", Value: 1}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful scalar range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("unexpected scalar range result: %#v", matrix.Series)
	}
	for _, point := range matrix.Series[0].Values {
		if point.Value != 1 {
			t.Fatalf("expected constant scalar value, got %#v", matrix.Series)
		}
	}
}

func TestScalarBuiltinPlanRangeMode(t *testing.T) {
	plan := &scalarBuiltinPlan{Expr: "time()", Func: "time"}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful scalar builtin range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("unexpected scalar builtin range result: %#v", matrix.Series)
	}
	if matrix.Series[0].Values[0].Value != 120 || matrix.Series[0].Values[1].Value != 150 || matrix.Series[0].Values[2].Value != 180 {
		t.Fatalf("unexpected time() range values: %#v", matrix.Series[0].Values)
	}
}

func TestLocalScalarConvertPlanInstantAndRange(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		if params.Mode == EvalModeInstant && params.EvaluationTime.Equal(time.Unix(120, 0).UTC()) {
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 7}}}, nil
		}
		if params.Mode == EvalModeInstant && params.EvaluationTime.Equal(time.Unix(150, 0).UTC()) {
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 7}, {Metric: map[string]string{"job": "worker"}, Timestamp: 1, Value: 8}}}, nil
		}
		return model.VectorValue{}, nil
	}}
	plan := &localScalarConvertPlan{Expr: "scalar(up)", Child: child}

	instant, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(120, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful scalar instant execution, got error: %v", err)
	}
	scalar, ok := instant.(model.ScalarValue)
	if !ok || scalar.Value != 7 {
		t.Fatalf("unexpected scalar instant result: %#v", instant)
	}
	rangeValue, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected successful scalar range execution, got error: %v", err)
	}
	matrix, ok := rangeValue.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", rangeValue)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("unexpected scalar range result: %#v", matrix.Series)
	}
	if matrix.Series[0].Values[0].Value != 7 || !math.IsNaN(matrix.Series[0].Values[1].Value) || !math.IsNaN(matrix.Series[0].Values[2].Value) {
		t.Fatalf("unexpected scalar() range values: %#v", matrix.Series[0].Values)
	}
}

func TestLocalPointwiseFunctionPlanEvaluatesScalarParameterChildren(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"__name__": "up", "job": "api"}, Timestamp: 12.5, Value: -3}}}, nil
	}}
	param := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		return model.ScalarValue{Timestamp: float64(params.EvaluationTime.Unix()), Value: 2}, nil
	}}
	plan := &localPointwiseFunctionPlan{Expr: "clamp_min(up, scalar(sum(up)))", Func: "clamp_min", ParamChildren: []Plan{param}, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(10, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful clamp_min execution, got error: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected clamp_min sample: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected clamp_min() to drop metric name, got %#v", vector.Samples[0].Metric)
	}
}

func TestLocalVectorPlanInstant(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
		return model.ScalarValue{Timestamp: 12.5, Value: 4}, nil
	}}
	plan := &localVectorPlan{Expr: "vector(1)", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(10, 0).UTC()})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls++
		return model.ScalarValue{Timestamp: float64(params.EvaluationTime.Unix()), Value: 1}, nil
	}}
	plan := &localVectorPlan{Expr: "vector(1)", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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

func TestLocalSortPlanInstant(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "b"}, Timestamp: 1, Value: 2}, {Metric: map[string]string{"job": "a"}, Timestamp: 1, Value: 1}}}, nil
	}}
	plan := &localSortPlan{Expr: "sort(up)", Func: "sort", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(10, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful sort instant execution, got error: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 2 || vector.Samples[0].Metric["job"] != "a" || vector.Samples[1].Metric["job"] != "b" {
		t.Fatalf("unexpected sort vector result: %#v", vector.Samples)
	}
}

func TestLocalSortPlanRangePassesThroughChild(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		if params.Mode != EvalModeRange {
			return nil, fmt.Errorf("expected range mode, got %s", params.Mode)
		}
		return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}}}, {Metric: map[string]string{"job": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}}}}}, nil
	}}
	plan := &localSortPlan{Expr: "sort(up)", Func: "sort", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(10, 0).UTC(), End: time.Unix(10, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected successful sort range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 2 || matrix.Series[0].Metric["job"] != "b" || matrix.Series[1].Metric["job"] != "a" {
		t.Fatalf("expected range sort to preserve child ordering, got %#v", matrix.Series)
	}
}

func TestLocalRoundPlanInstant(t *testing.T) {
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"__name__": "a"}, Timestamp: 1, Value: 1.2}}}, nil
	}}
	plan := &localRoundPlan{Expr: "round(up)", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(10, 0).UTC()})
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
	plan := &localRoundPlan{Expr: "round(up)", Child: testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		if params.EvaluationTime.Unix()%60 == 0 {
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 2.7}}}, nil
		}
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 1.2}}}, nil
	}}}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		t0 := float64(params.EvaluationTime.Add(-time.Minute).Unix())
		t1 := float64(params.EvaluationTime.Unix())
		return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: t0, Value: t0}, {Timestamp: t1, Value: t1}}}}}, nil
	}}
	plan := &localRangeFunctionPlan{Expr: "last_over_time((up * 100)[5m:1m])", Func: "last_over_time", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		return model.MatrixValue{Series: []model.RangeSeries{
			{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: float64(params.EvaluationTime.Add(-10 * time.Second).Unix()), Value: 2}, {Timestamp: float64(params.EvaluationTime.Add(-5 * time.Second).Unix()), Value: 1}, {Timestamp: float64(params.EvaluationTime.Unix()), Value: 3}}},
			{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: float64(params.EvaluationTime.Add(-10 * time.Second).Unix()), Value: 7}, {Timestamp: float64(params.EvaluationTime.Unix()), Value: 5}}},
		}}, nil
	}}
	plan := &localQuantileOverTimePlan{Expr: "quantile_over_time(0.5, (up*100)[5m:30s])", Quantile: 0.5, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		return model.MatrixValue{Series: []model.RangeSeries{
			{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: float64(params.EvaluationTime.Add(-10 * time.Second).Unix()), Value: 2}, {Timestamp: float64(params.EvaluationTime.Unix()), Value: 10}}},
		}}, nil
	}}
	plan := &localQuantileOverTimePlan{Expr: "quantile_over_time(0.5, (up*100)[5m:30s])", Quantile: 0.5, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		shifted := float64(params.EvaluationTime.Add(-10 * time.Second).Unix())
		return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: shifted, Value: 1}}}}}, nil
	}}
	plan := &localRangeFunctionPlan{Expr: "last_over_time((up * 100)[5m:1m])", Func: "last_over_time", Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
		return model.VectorValue{}, nil
	}}
	plan := &localAbsentPlan{Expr: `absent(nonexistent{job="api"})`, OutputMetric: map[string]string{"job": "api"}, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
		return model.MatrixValue{}, nil
	}}
	plan := &localAbsentOverTimePlan{Expr: `absent_over_time(nonexistent{job="api"}[5m])`, OutputMetric: map[string]string{"job": "api"}, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second})
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

func TestExecuteRangeVectorPlanPreservesSparseDisappearingSeries(t *testing.T) {
	calls := 0
	plan := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls++
		switch params.EvaluationTime.Unix() {
		case 120:
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 120, Value: 1}}}, nil
		case 150:
			return model.VectorValue{}, nil
		case 180:
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 180, Value: 2}, {Metric: map[string]string{"job": "worker"}, Timestamp: 180, Value: 9}}}, nil
		default:
			return model.VectorValue{}, nil
		}
	}}

	value, err := executeRangeVectorPlan(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(120, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: 30 * time.Second}, "sparse_test", plan.execute)
	if err != nil {
		t.Fatalf("expected successful sparse range execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if calls != 3 {
		t.Fatalf("expected three instant evaluations, got %d", calls)
	}
	if len(matrix.Series) != 2 {
		t.Fatalf("expected two sparse output series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 2 || matrix.Series[0].Values[0].Timestamp != 120 || matrix.Series[0].Values[1].Timestamp != 180 {
		t.Fatalf("expected api series only at present steps, got %#v", matrix.Series[0])
	}
	if len(matrix.Series[1].Values) != 1 || matrix.Series[1].Values[0].Timestamp != 180 {
		t.Fatalf("expected worker series only at late appearance step, got %#v", matrix.Series[1])
	}
}

func TestLocalSubqueryPlanUsesLocalPathForDelegatedMatrixRootInInstantMode(t *testing.T) {
	expr := mustParseExpr(t, "up[2m:1m]")
	calls := 0
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls++
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, DelegatedLeafCompatible: true, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
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
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.Unix())
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: subquery.Range, Step: subquery.Step, Offset: subquery.OriginalOffset, Timestamp: cloneInt64Pointer(subquery.Timestamp), StartOrEnd: subquery.StartOrEnd, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(999, 0).UTC()})
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

func TestLocalSubqueryPlanDefaultsMissingStepToOneMinute(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	calls := make([]int64, 0)
	child := testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.Unix())
		ts := float64(params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: ts, Value: ts}}}, nil
	}}
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: 0, Child: child}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
	if err != nil {
		t.Fatalf("expected successful default-step subquery execution, got error: %v", err)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected one series with three points, got %#v", matrix.Series)
	}
	want := []int64{60, 120, 180}
	if len(calls) != len(want) {
		t.Fatalf("expected %d child evaluations, got %d", len(want), len(calls))
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("expected child call %d at %d, got %d", i, want[i], calls[i])
		}
	}
}

func TestLocalSubqueryPlanExecutesLocalRangeMode(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	calls := make([]int64, 0)
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, Child: testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 1}}}, nil
	}}}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected local range-mode subquery execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 6 {
		t.Fatalf("expected one series with six points, got %#v", matrix.Series)
	}
	want := []int64{-120, -60, 0, 60, 120, 180}
	if len(calls) != len(want) {
		t.Fatalf("expected %d child evaluations, got %d", len(want), len(calls))
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("expected child call %d at %d, got %d", i, want[i], calls[i])
		}
	}
}

func TestLocalSubqueryPlanExecutesAnchoredLocalRangeMode(t *testing.T) {
	expr := mustParseExpr(t, `(up * 100)[2m:1m] @ end()`)
	calls := make([]int64, 0)
	plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, StartOrEnd: parser.END, Child: testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 1}}}, nil
	}}}

	value, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("expected anchored local range-mode subquery execution, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 3 {
		t.Fatalf("expected anchored subquery to materialize one fixed window, got %#v", matrix.Series)
	}
	want := []int64{60, 120, 180}
	if len(calls) != len(want) {
		t.Fatalf("expected %d child evaluations, got %d", len(want), len(calls))
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("expected anchored child call %d at %d, got %d", i, want[i], calls[i])
		}
	}
}

type testQueryPlan struct {
	executeFn func(context.Context, *Evaluator, EvalParams) (model.RuntimeValue, error)
}

func (p testQueryPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	if p.executeFn == nil {
		return model.VectorValue{}, nil
	}
	return p.executeFn(ctx, Evaluator, params)
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

func TestBuildPlanWithContextBuildsNativeAbsentPlanUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression(`absent(up{job="api"})`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native absent plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan for absent under force_supported, got %T", built)
	}
	if native.Kind != "absent" {
		t.Fatalf("expected absent native kind, got %#v", native)
	}
}

func TestBuildPlanWithContextBuildsNativeAbsentOverTimeRangePlanUnderForceSupported(t *testing.T) {
	expr, err := plan.ParseExpression(`absent_over_time(up{job="api"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native absent_over_time range plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan for absent_over_time under force_supported, got %T", built)
	}
	if native.Kind != "absent_over_time" {
		t.Fatalf("expected absent_over_time native kind, got %#v", native)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
