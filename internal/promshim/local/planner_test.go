package local

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildPlanCreatesDelegatedLeafPlan(t *testing.T) {
	expr, err := logical.ParseExpression("up")
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
	expr, err := logical.ParseExpression("sum by (job) (up)")
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
	expr, err := logical.ParseExpression("up @ 1710000000")
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
	expr, err := logical.ParseExpression("(up * 100)[5m:30s]")
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
	if subquery.Info == nil || subquery.Info.SubtreeShape != nativeplan.SubtreeShapeSubquery {
		t.Fatalf("expected native subquery shape, got %#v", subquery.Info)
	}
	if subquery.Expr != "(up * 100)[5m:30s]" {
		t.Fatalf("expected subquery expression to be preserved, got %q", subquery.Expr)
	}
}

func TestBuildPlanCreatesNativeSubqueryPlanWithAggregationChild(t *testing.T) {
	expr, err := logical.ParseExpression("sum(up)[5m:30s]")
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
	if subquery.Info == nil || subquery.Info.SubtreeShape != nativeplan.SubtreeShapeSubquery {
		t.Fatalf("expected native subquery shape, got %#v", subquery.Info)
	}
	if len(subquery.Info.Children) != 1 || subquery.Info.Children[0].SubtreeShape != nativeplan.SubtreeShapeAggregation {
		t.Fatalf("expected native aggregation child inside subquery, got %#v", subquery.Info.Children)
	}
}

func TestResolveDelegatedPromQLRewritesAtStartEndForRange(t *testing.T) {
	expr, err := logical.ParseExpression("up @ start() + up @ end()")
	if err != nil {
		t.Fatal(err)
	}

	promQL, err := resolveDelegatedPromQL(expr, EvalParams{
		Mode:  EvalModeRange,
		Start: time.Unix(100, 0).UTC(),
		End:   time.Unix(200, 0).UTC(),
	}, 0)
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
	expr, err := logical.ParseExpression("up @ start()")
	if err != nil {
		t.Fatal(err)
	}

	evalTime := time.Unix(321, 0).UTC()
	promQL, err := resolveDelegatedPromQL(expr, EvalParams{Mode: EvalModeInstant, EvaluationTime: evalTime}, 0)
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
	expr, err := logical.ParseExpression("up[5m:1m] @ start()")
	if err != nil {
		t.Fatal(err)
	}

	promQL, err := resolveDelegatedPromQL(expr, EvalParams{Mode: EvalModeRange, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC(), Step: time.Minute}, 0)
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

// TestResolveDelegatedPromQLFillsNoStepSubqueryInterval locks that delegated
// PromQL text makes the no-step subquery default explicit, so ClickHouse's
// PromQL engine cannot substitute its own default (or the outer step) for
// promshim's configured default evaluation interval. Explicit steps are
// never rewritten.
func TestResolveDelegatedPromQLFillsNoStepSubqueryInterval(t *testing.T) {
	rangeParams := EvalParams{Mode: EvalModeRange, Start: time.Unix(100, 0).UTC(), End: time.Unix(200, 0).UTC(), Step: 300 * time.Second}
	for _, tc := range []struct {
		name        string
		query       string
		interval    time.Duration
		want        string
		wantAbsent  string
		occurrences int
	}{
		{name: "no_step_filled_with_1m_fallback", query: "up[15m:]", interval: 0, want: ":1m]", occurrences: 1},
		{name: "no_step_filled_with_configured_interval", query: "up[15m:]", interval: 30 * time.Second, want: ":30s]", occurrences: 1},
		{name: "explicit_step_untouched", query: "up[15m:2m]", interval: 30 * time.Second, want: ":2m]", wantAbsent: ":30s]", occurrences: 1},
		{name: "nested_subqueries_both_filled", query: "max_over_time(last_over_time(up[10m:])[20m:])", interval: 90 * time.Second, want: ":1m30s]", occurrences: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := logical.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			promQL, err := resolveDelegatedPromQL(expr, rangeParams, tc.interval)
			if err != nil {
				t.Fatalf("resolveDelegatedPromQL: %v", err)
			}
			if got := strings.Count(promQL, tc.want); got != tc.occurrences {
				t.Fatalf("expected %d occurrence(s) of %q in delegated text, got %d: %q", tc.occurrences, tc.want, got, promQL)
			}
			if tc.wantAbsent != "" && strings.Contains(promQL, tc.wantAbsent) {
				t.Fatalf("expected delegated text to not contain %q, got %q", tc.wantAbsent, promQL)
			}
			if strings.Contains(promQL, ":]") {
				t.Fatalf("expected no empty subquery step to survive delegation, got %q", promQL)
			}
		})
	}
}

func TestBuildPlanCreatesAvgAggregationPlan(t *testing.T) {
	expr, err := logical.ParseExpression("avg(up)")
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
	expr, err := logical.ParseExpression("topk(3, up)")
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
	expr, err := logical.ParseExpression(`count_values("sample_value", up)`)
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
	expr, err := logical.ParseExpression("limitk(2, up)")
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
	expr, err := logical.ParseExpression("limit_ratio(0.5, up)")
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
	expr, err := logical.ParseExpression("histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
	expr, err := logical.ParseExpression("histogram_quantiles(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])), \"quantile\", 0.5, scalar(sum(up)))")
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
			expr, err := logical.ParseExpression(fn + "(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
			expr, err := logical.ParseExpression(fn + `(http_request_duration_seconds_bucket{job="api"})`)
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
	expr, err := logical.ParseExpression("histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
		expr, err := logical.ParseExpression(fn + "(up[5m])")
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
		if ratePlan.Info == nil || ratePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || ratePlan.Info.NodeType != fn {
			t.Fatalf("expected native %s range-function shape, got %#v", fn, ratePlan.Info)
		}
	}
}

func TestBuildPlanCreatesNativeIncreasePlan(t *testing.T) {
	expr, err := logical.ParseExpression("increase(up[5m])")
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
	if increasePlan.Info == nil || increasePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || increasePlan.Info.NodeType != "increase" {
		t.Fatalf("expected native increase range-function shape, got %#v", increasePlan.Info)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeFunctionPlanInRangeModeForDirectSelector(t *testing.T) {
	for _, query := range []string{"sum_over_time(up[5m])", "rate(up[5m])", "increase(up[5m])", "changes(up[5m])", "resets(up[5m])", "quantile_over_time(0.95, up[5m])", "first_over_time(up[5m])", "ts_of_first_over_time(up[5m])", "ts_of_last_over_time(up[5m])", "ts_of_max_over_time(up[5m])", "ts_of_min_over_time(up[5m])"} {
		expr, err := logical.ParseExpression(query)
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
		if rangePlan.Info == nil || rangePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
			t.Fatalf("expected native range function shape for %q, got %#v", query, rangePlan.Info)
		}
	}
}

func TestBuildPlanWithContextCreatesNativeRangeFunctionPlanInRangeModeForSubquery(t *testing.T) {
	for _, query := range []string{"sum_over_time(sum(up)[5m:1m])", "rate(sum(up)[5m:1m])", "increase(sum(up)[5m:1m])", "changes(sum(up)[5m:1m])", "resets(sum(up)[5m:1m])", "quantile_over_time(0.95, sum(up)[5m:1m])", "first_over_time(sum(up)[5m:1m])", "ts_of_first_over_time(sum(up)[5m:1m])", "ts_of_last_over_time(sum(up)[5m:1m])", "ts_of_max_over_time(sum(up)[5m:1m])", "ts_of_min_over_time(sum(up)[5m:1m])"} {
		expr, err := logical.ParseExpression(query)
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
		if rangePlan.Info == nil || rangePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
			t.Fatalf("expected native range function shape for %q, got %#v", query, rangePlan.Info)
		}
		if rangePlan.Info.RangeFunctionSubquery == nil {
			t.Fatalf("expected native subquery child for %q, got %#v", query, rangePlan.Info)
		}
	}
}

func TestBuildPlanCreatesVectorPlan(t *testing.T) {
	expr, err := logical.ParseExpression("vector(0)")
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
	if nativePlan.Info == nil || nativePlan.Kind != "vector" {
		t.Fatalf("expected native vector plan, got %#v", nativePlan)
	}
}

func TestBuildPlanCreatesRoundPlan(t *testing.T) {
	expr, err := logical.ParseExpression("round(up)")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected round plan, got error: %v", err)
	}
	if nativePlan, ok := execPlan.(*nativeSubtreePlan); ok {
		if nativePlan.Info == nil || nativePlan.Info.SubtreeShape != nativeplan.SubtreeShapeValueTransform {
			t.Fatalf("expected value-transform native round shape, got %#v", nativePlan.Info)
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
	expr, err := logical.ParseExpression("count(count by (job) (up))")
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
	expr, err := logical.ParseExpression("last_over_time(up[5m])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "last_over_time" {
		t.Fatalf("expected native last_over_time range-function shape, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesSumOverTimePlan(t *testing.T) {
	expr, err := logical.ParseExpression("sum_over_time(up[5m])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "sum_over_time" {
		t.Fatalf("expected native sum_over_time range-function shape, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesAvgOverTimePlan(t *testing.T) {
	expr, err := logical.ParseExpression("avg_over_time(up[5m])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "avg_over_time" {
		t.Fatalf("expected native avg_over_time range-function shape, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesMaxOverTimePlan(t *testing.T) {
	expr, err := logical.ParseExpression("max_over_time(up[5m])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "max_over_time" {
		t.Fatalf("expected native max_over_time range-function shape, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesMinOverTimePlan(t *testing.T) {
	expr, err := logical.ParseExpression("min_over_time(up[5m])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "min_over_time" {
		t.Fatalf("expected native min_over_time range-function shape, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesCountOverTimePlan(t *testing.T) {
	expr, err := logical.ParseExpression("count_over_time(up[5m])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "count_over_time" {
		t.Fatalf("expected native count_over_time range-function shape, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesNativeRangeFunctionPlanForSubqueryArg(t *testing.T) {
	expr, err := logical.ParseExpression("sum_over_time((up * 100)[5m:30s])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native range function over subquery, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesQuantileOverTimePlan(t *testing.T) {
	expr, err := logical.ParseExpression("quantile_over_time(0.95, (up * 100)[5m:30s])")
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
	if rangeFn.Info == nil || rangeFn.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || rangeFn.Info.NodeType != "quantile_over_time" {
		t.Fatalf("expected native quantile_over_time range-function shape, got %#v", rangeFn.Info)
	}
	if rangeFn.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native quantile_over_time subquery child, got %#v", rangeFn.Info)
	}
}

func TestBuildPlanCreatesAbsentPlan(t *testing.T) {
	expr, err := logical.ParseExpression(`absent(nonexistent{job="api",instance=~".*"})`)
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
	expr, err := logical.ParseExpression(`absent_over_time(nonexistent{job="api"}[5m])`)
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
	expr, err := logical.ParseExpression("sum_over_time((up * 100)[5m:30s]) + count_over_time((up * 100)[5m:30s])")
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
	if binaryPlan.Info == nil || binaryPlan.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
		t.Fatalf("expected native binary join shape, got %#v", binaryPlan.Info)
	}
}

func TestBuildPlanCreatesNestedSubqueryRangeFunctionPlan(t *testing.T) {
	expr, err := logical.ParseExpression("last_over_time(last_over_time((up * 100)[5m:30s])[10m:1m])")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		t.Fatalf("expected outer range-function shape, got %#v", native.Info)
	}
	if native.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected outer subquery child, got %#v", native.Info)
	}
	if len(native.Info.Children) == 0 || native.Info.Children[0].SubtreeShape != nativeplan.SubtreeShapeSubquery {
		t.Fatalf("expected subquery child info, got %#v", native.Info.Children)
	}
	subChildren := native.Info.Children[0].Children
	if len(subChildren) == 0 || subChildren[0].SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		t.Fatalf("expected subquery child to be range-function, got %#v", subChildren)
	}
}

func TestBuildPlanRejectsNonLiteralHistogramQuantileParameter(t *testing.T) {
	expr, err := logical.ParseExpression("histogram_quantile(1 / 2, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
	expr, err := logical.ParseExpression("histogram_fraction(time(), 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
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
	expr, err := logical.ParseExpression("sum by (job) (up)")
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
	expr, err := logical.ParseExpression("avg by (job) (-up)")
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
	if native.Info == nil || native.Info.Aggregation == nil || native.Info.Aggregation.SourceView == nil {
		t.Fatalf("expected native subtree aggregation source metadata, got %#v", native.Info)
	}
	source := native.Info.Aggregation.SourceView
	if !strings.Contains(source.ValueExpr, "-") {
		t.Fatalf("expected unary value transform in native source, got %#v", source)
	}
	if !strings.Contains(source.TagsExpr, "__name__") {
		t.Fatalf("expected metric-name dropping tags transform, got %#v", source)
	}
}

func TestBuildPlanWithContextCreatesNativeAggregationPlanForVectorScalarLeaf(t *testing.T) {
	expr, err := logical.ParseExpression("sum by (job) (up * 100)")
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
	if native.Info == nil || native.Info.Aggregation == nil || native.Info.Aggregation.SourceView == nil {
		t.Fatalf("expected native subtree aggregation source metadata, got %#v", native.Info)
	}
	source := native.Info.Aggregation.SourceView
	if !strings.Contains(source.ValueExpr, "100") || !strings.Contains(source.ValueExpr, "*") {
		t.Fatalf("expected vector-scalar arithmetic in native source, got %#v", source)
	}
	if !strings.Contains(source.TagsExpr, "__name__") {
		t.Fatalf("expected metric-name dropping tags transform, got %#v", source)
	}
}

func TestBuildPlanWithContextCreatesLocalPlanForInfo(t *testing.T) {
	expr, err := logical.ParseExpression("info(up)")
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

func TestBuildPlanWithContextLocalPushdownSuppressesNativeRootOnly(t *testing.T) {
	expr, err := logical.ParseExpression("sum by (job) (rate(up[5m]))")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(300, 0).UTC(),
		Step:                            30 * time.Second,
		NativeLoweringMode:              NativeLoweringModeLocalPushdown,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected local-pushdown plan, got error: %v", err)
	}
	root, ok := built.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected local aggregation root, got %T", built)
	}
	if _, ok := root.Child.(*nativeSubtreePlan); !ok {
		t.Fatalf("expected native child under local root, got %T", root.Child)
	}
}

func TestLocalPushdownHistogramChildNarrowsNativeAggregationTags(t *testing.T) {
	expr, err := logical.ParseExpression("histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket[1h])))")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeInstant,
		EvaluationTime:                  time.Unix(3600, 0).UTC(),
		NativeLoweringMode:              NativeLoweringModeLocalPushdown,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected local-pushdown histogram plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
	if explain.Kind != "histogram_quantile" || explain.Strategy != "local" || len(explain.Children) != 1 {
		t.Fatalf("expected local histogram root with one child, got %#v", explain)
	}
	child := explain.Children[0]
	if child.Kind != "aggregation" || child.Strategy != "native_sql" || child.RenderedSQL == "" {
		t.Fatalf("expected native aggregation child with rendered SQL, got %#v", child)
	}
	if !strings.Contains(child.RenderedSQL, "if(mapContains(src.tags, 'le')") {
		t.Fatalf("expected histogram child selector to project le tag only, got %q", child.RenderedSQL)
	}
	if strings.Contains(child.RenderedSQL, "arrayConcat([tuple('__name__'") || strings.Contains(child.RenderedSQL, "mapKeys(src.tags)") {
		t.Fatalf("expected histogram child selector to avoid full tag materialization, got %q", child.RenderedSQL)
	}
}

func TestLocalPushdownGenericAggregationKeepsFullTagsBeforeRate(t *testing.T) {
	expr, err := logical.ParseExpression("sum by (le) (rate(http_request_duration_seconds_bucket[1h]))")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeInstant,
		EvaluationTime:                  time.Unix(3600, 0).UTC(),
		NativeLoweringMode:              NativeLoweringModeLocalPushdown,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected local-pushdown aggregation plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
	if explain.Kind != "aggregation" || explain.Strategy != "local" || len(explain.Children) != 1 {
		t.Fatalf("expected local aggregation root with one child, got %#v", explain)
	}
	child := explain.Children[0]
	if child.Strategy != "native_sql" || child.RenderedSQL == "" {
		t.Fatalf("expected native child with rendered SQL, got %#v", child)
	}
	if !strings.Contains(child.RenderedSQL, "arrayConcat([tuple('__name__'") || !strings.Contains(child.RenderedSQL, "mapKeys(src.tags)") {
		t.Fatalf("expected generic aggregation child to preserve full tags before rate, got %q", child.RenderedSQL)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForInfo(t *testing.T) {
	expr, err := logical.ParseExpression("info(up)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeInfoJoin {
		t.Fatalf("expected info join shape, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForInfoRegexMetricSelector(t *testing.T) {
	expr, err := logical.ParseExpression("info(up, {__name__=~\".+_info\"})")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeInfoJoin {
		t.Fatalf("expected info join shape, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForRootSubqueryInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression(`sum(up)[5m:1m]`)
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeSubquery {
		t.Fatalf("expected root subquery shape, got %#v", native.Info)
	}
	if len(native.Info.Children) == 0 || native.Info.Children[0].SubtreeShape != nativeplan.SubtreeShapeAggregation {
		t.Fatalf("expected native aggregation child under root subquery, got %#v", native.Info.Children)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForLabelJoinRootSubquery(t *testing.T) {
	expr, err := logical.ParseExpression(`label_join(up, "joined", "/", "job", "namespace")[5m:1m]`)
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeSubquery {
		t.Fatalf("expected subquery shape, got %#v", native.Info)
	}
	if len(native.Info.Children) == 0 || native.Info.Children[0].SubtreeShape != nativeplan.SubtreeShapeLabelTransform {
		t.Fatalf("expected native label transform child under root subquery, got %#v", native.Info.Children)
	}
}

func TestBuildPlanWithContextCreatesNativeRangePlanForAnchoredPointwiseFunction(t *testing.T) {
	expr, err := logical.ParseExpression("abs(up @ 1710000000)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeUnarySourceExpr {
		t.Fatalf("expected anchored unary native shape, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForPointwiseFunction(t *testing.T) {
	expr, err := logical.ParseExpression("abs(up)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeUnarySourceExpr {
		t.Fatalf("expected abs native shape, got %#v", native.Info)
	}
	if native.Info.SourceExpr == nil || !strings.Contains(native.Info.SourceExpr.ValueExpr, "abs") {
		t.Fatalf("expected abs value expression, got %#v", native.Info.SourceExpr)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeAggregationForAnchoredSelector(t *testing.T) {
	expr, err := logical.ParseExpression("sum by (job) (up @ 1710000000)")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil {
		t.Fatalf("expected native aggregation root, got %#v", nativeRoot.Info)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeAggregationForStartEndAnchors(t *testing.T) {
	testCases := []string{
		"sum by (job) (up @ start())",
		"sum by (job) (up @ end())",
	}
	for _, query := range testCases {
		t.Run(query, func(t *testing.T) {
			expr, err := logical.ParseExpression(query)
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
			if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil {
				t.Fatalf("expected native aggregation root, got %#v", nativeRoot.Info)
			}
		})
	}
}

func TestBuildPlanWithContextCreatesNativeRangeBinaryWithStartEndAnchors(t *testing.T) {
	expr, err := logical.ParseExpression("up @ start() + up @ end()")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModePrefer})
	if err != nil {
		t.Fatalf("expected native binary plan for anchored range binary, got error: %v", err)
	}
	nativePlan, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan for anchored range binary, got %T", built)
	}
	if nativePlan.Info == nil || nativePlan.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
		t.Fatalf("expected native binary join subtree, got %#v", nativePlan.Info)
	}
}

func TestBuildPlanWithContextCreatesNativeRangeBinaryWithOffsets(t *testing.T) {
	expr, err := logical.ParseExpression("up - up offset 60s")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: 30 * time.Second, NativeLoweringMode: NativeLoweringModePrefer})
	if err != nil {
		t.Fatalf("expected native binary plan for offset range binary, got error: %v", err)
	}
	nativePlan, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan for offset range binary, got %T", built)
	}
	if nativePlan.Info == nil || nativePlan.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
		t.Fatalf("expected native binary join subtree, got %#v", nativePlan.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForScalarConvert(t *testing.T) {
	expr, err := logical.ParseExpression("scalar(up)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeScalarConvert {
		t.Fatalf("expected scalar convert subtree, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesLocalPlanForPredictLinear(t *testing.T) {
	expr, err := logical.ParseExpression("predict_linear(up[5m], 60)")
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
	expr, err := logical.ParseExpression("predict_linear(up[5m], 4 * 3600)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || native.Info.NodeType != "predict_linear" {
		t.Fatalf("expected predict_linear range subtree, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesLocalPlanForDoubleExponentialSmoothing(t *testing.T) {
	expr, err := logical.ParseExpression("double_exponential_smoothing(up[5m], 0.5, 0.3)")
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
	expr, err := logical.ParseExpression("double_exponential_smoothing(up[5m], 0.5, 0.3)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || native.Info.NodeType != "double_exponential_smoothing" {
		t.Fatalf("expected smoothing range subtree, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextNormalizesHoltWintersAlias(t *testing.T) {
	expr, err := logical.ParseExpression("holt_winters(up[5m], 0.5, 0.3)")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || native.Info.NodeType != "double_exponential_smoothing" {
		t.Fatalf("expected holt_winters alias to normalize to double_exponential_smoothing, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForSyntheticDateFunction(t *testing.T) {
	expr, err := logical.ParseExpression("minute()")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeSyntheticSeries || native.Info.NodeType != "minute" {
		t.Fatalf("expected synthetic minute() subtree, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForSortByLabel(t *testing.T) {
	expr, err := logical.ParseExpression("sort_by_label(up, \"instance\", \"job\")")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeSortTransform {
		t.Fatalf("expected sort transform subtree, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForClampWithScalarParameterChild(t *testing.T) {
	expr, err := logical.ParseExpression("clamp_min(up, scalar(sum(up)))")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeClampTransform {
		t.Fatalf("expected clamp transform subtree, got %#v", native.Info)
	}
}

func TestBuildPlanWithContextCreatesNativePlanForScalarBuiltin(t *testing.T) {
	expr, err := logical.ParseExpression("time()")
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
	if native.Info == nil || native.Info.SubtreeShape != nativeplan.SubtreeShapeSyntheticSeries || native.Info.NodeType != "time" {
		t.Fatalf("expected synthetic time() subtree, got %#v", native.Info)
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
	expr, err := logical.ParseExpression(`sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`)
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeLabelTransform {
		t.Fatalf("expected label-transform aggregation source, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestExplainPlanDescribesNativeAggregationStrategy(t *testing.T) {
	expr, err := logical.ParseExpression("sum by (job) (up)")
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
	if explain.Children[0].Lowering == nil || explain.Children[0].Lowering.SubtreeShape == "" {
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
	if !strings.Contains(explain.RenderedSQL, "src.tags['job']") || strings.Contains(explain.RenderedSQL, "mapKeys(src.tags)") {
		t.Fatalf("expected aggregation child selector to project only job label, got %q", explain.RenderedSQL)
	}
}

func TestExplainPlanIncludesSparseRangeWindowPhysicalDecision(t *testing.T) {
	expr, err := logical.ParseExpression("avg_over_time(up[1h])")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:               EvalModeRange,
		Start:              time.Unix(0, 0).UTC(),
		End:                time.Unix(3*3600, 0).UTC(),
		Step:               time.Hour,
		NativeLoweringMode: NativeLoweringModeForceSupported,
	})
	if err != nil {
		t.Fatalf("expected native range-function plan, got error: %v", err)
	}
	explain := ExplainPlanWithLowering(plan, analysis.Root)
	decision, ok := findPhysicalDecision(explain.PhysicalDecisions, "range_window_aggregate")
	if !ok {
		t.Fatalf("expected range-window physical decision, got %#v", explain.PhysicalDecisions)
	}
	if decision.Strategy != string(physical.RangeWindowAggregateStrategySparseDirectAggregate) {
		t.Fatalf("physical strategy = %q, want sparse direct aggregate; decisions=%#v", decision.Strategy, explain.PhysicalDecisions)
	}
}

func TestExplainPlanIncludesSparseRateAndNoCapPhysicalDecisions(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantKind     string
		wantStrategy string
		wantDecision bool
	}{
		{
			name:         "sparse direct rate aggregation",
			query:        "sum by (job) (rate(up[1h]))",
			wantKind:     "range_function_rows",
			wantStrategy: string(physical.RangeFunctionRowsStrategySparseDirectRateAggregation),
			wantDecision: true,
		},
		{
			name:         "fused rate aggregation preserves no thread cap",
			query:        "sum by (job) (rate(up[1h]))",
			wantKind:     "query_settings",
			wantStrategy: "no_thread_cap",
			wantDecision: true,
		},
		{
			name:         "direct range aggregation applies thread-cap guardrail setting",
			query:        "sum by (job) (up)",
			wantKind:     "query_settings",
			wantStrategy: "set_max_threads",
			wantDecision: true,
		},
		{
			name:         "subquery rate over aggregation preserves no thread cap",
			query:        "rate(sum by (job) (up)[5m:1m])",
			wantKind:     "query_settings",
			wantStrategy: "no_thread_cap",
			wantDecision: true,
		},
		{
			name:         "nested subquery rate over aggregation leaves root query settings unset",
			query:        "rate(sum by (job) (up)[5m:1m]) + on(job) up",
			wantKind:     "query_settings",
			wantDecision: false,
		},
		{
			name:         "mixed root leaves query settings unset",
			query:        "sum(avg_over_time(up[1h])) + sum(rate((sum by (job) (up))[5m:1m]))",
			wantKind:     "query_settings",
			wantDecision: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := logical.ParseExpression(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
				Mode:                            EvalModeRange,
				Start:                           time.Unix(0, 0).UTC(),
				End:                             time.Unix(3*3600, 0).UTC(),
				Step:                            time.Hour,
				NativeLoweringMode:              NativeLoweringModeForceSupported,
				PreferNativeAggregationPushdown: true,
			})
			if err != nil {
				t.Fatalf("expected native plan, got error: %v", err)
			}
			explain := ExplainPlanWithLowering(plan, analysis.Root)
			decision, ok := findPhysicalDecision(explain.PhysicalDecisions, tt.wantKind)
			if !tt.wantDecision {
				if ok {
					t.Fatalf("expected no %q physical decision, got %#v", tt.wantKind, explain.PhysicalDecisions)
				}
				return
			}
			if !ok {
				t.Fatalf("expected %q physical decision, got %#v", tt.wantKind, explain.PhysicalDecisions)
			}
			if decision.Strategy != tt.wantStrategy {
				t.Fatalf("physical strategy = %q, want %q; decisions=%#v", decision.Strategy, tt.wantStrategy, explain.PhysicalDecisions)
			}
		})
	}
}

func TestExplainPlanIncludesSubqueryNodeNoThreadCapDecision(t *testing.T) {
	expr, err := logical.ParseExpression("rate((sum by (job) (up))[30m:1m])")
	if err != nil {
		t.Fatal(err)
	}

	plan, analysis, err := BuildPlanWithContextAndAnalysis(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(3*3600, 0).UTC(),
		Step:                            30 * time.Second,
		NativeLoweringMode:              NativeLoweringModeForceSupported,
		PreferNativeAggregationPushdown: true,
	})
	if err != nil {
		t.Fatalf("expected native subquery/rate plan, got error: %v", err)
	}

	explain := ExplainPlanWithLowering(plan, analysis.Root)
	subquery, ok := findExplainNodeByKind(explain, "subquery")
	if !ok {
		t.Fatalf("expected subquery explain node, got %#v", explain)
	}
	decision, ok := findPhysicalDecision(subquery.PhysicalDecisions, "query_settings")
	if !ok {
		t.Fatalf("expected query_settings decision on subquery node, got %#v", subquery.PhysicalDecisions)
	}
	if decision.Strategy != "no_thread_cap" {
		t.Fatalf("subquery query_settings strategy = %q, want no_thread_cap; decisions=%#v", decision.Strategy, subquery.PhysicalDecisions)
	}
	if decision.Reason != physical.ThreadPreferenceReasonSubqueryRateRows {
		t.Fatalf("subquery query_settings reason = %q, want %q", decision.Reason, physical.ThreadPreferenceReasonSubqueryRateRows)
	}
	if len(decision.Guards) == 0 || decision.Guards[0] != "needs_subquery_step_grid" {
		t.Fatalf("expected subquery step-grid guard prefix, got %#v", decision.Guards)
	}
	if len(decision.Rejected) != 1 || decision.Rejected[0].Strategy != "set_max_threads" || decision.Rejected[0].Reason != "suppressed by no-thread-cap preference" {
		t.Fatalf("expected canonical no-thread-cap rejected alternative, got %#v", decision.Rejected)
	}
}

func findPhysicalDecision(decisions []physical.Decision, kind string) (physical.Decision, bool) {
	for _, decision := range decisions {
		if decision.Kind == kind {
			return decision, true
		}
	}
	return physical.Decision{}, false
}

func findExplainNodeByKind(node ExplainNode, kind string) (ExplainNode, bool) {
	if node.Kind == kind {
		return node, true
	}
	for _, child := range node.Children {
		if found, ok := findExplainNodeByKind(child, kind); ok {
			return found, true
		}
	}
	return ExplainNode{}, false
}

func TestExplainPlanDescribesNativeTransformedAggregationStrategy(t *testing.T) {
	expr, err := logical.ParseExpression("sum by (job) (up * 100)")
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
	expr, err := logical.ParseExpression("sum by (job) (up)")
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
	expr, err := logical.ParseExpression(`sum by (job) (label_join(up, "joined", "/", "job", "namespace"))`)
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
	expr, err := logical.ParseExpression("1 + 2")
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
	expr, err := logical.ParseExpression("up * on(job) group_left sum by (job) (up)")
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
	if binaryPlan.Info == nil || binaryPlan.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
		t.Fatalf("expected native binary join subtree, got %#v", binaryPlan.Info)
	}
	if binaryPlan.Info.JoinShape != nativeplan.JoinShapeManyToOne {
		t.Fatalf("expected many-to-one join shape, got %#v", binaryPlan.Info.JoinShape)
	}
	if !sameStrings(binaryPlan.Info.JoinLabels, []string{"job"}) {
		t.Fatalf("unexpected join labels: %#v", binaryPlan.Info.JoinLabels)
	}
}

func TestExplainPlanDescribesNativeVectorMatchingBinaryStrategy(t *testing.T) {
	expr, err := logical.ParseExpression("up * on(job) group_left sum by (job) (up)")
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
	expr, err := logical.ParseExpression("up and on(job) up")
	if err != nil {
		t.Fatal(err)
	}

	execPlan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected set-operator plan, got error: %v", err)
	}
	if nativePlan, ok := execPlan.(*nativeSubtreePlan); ok {
		if nativePlan.Info == nil || nativePlan.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
			t.Fatalf("expected native binary join subtree, got %#v", nativePlan.Info)
		}
		if nativePlan.Info.JoinShape != nativeplan.JoinShapeManyToMany {
			t.Fatalf("expected many-to-many LAND native join, got %#v", nativePlan.Info.JoinShape)
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
	expr, err := logical.ParseExpression("-up")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlan(expr)
	if err != nil {
		t.Fatalf("expected unary plan, got error: %v", err)
	}
	if nativePlan, ok := plan.(*nativeSubtreePlan); ok {
		if nativePlan.Info == nil || nativePlan.Info.SubtreeShape != nativeplan.SubtreeShapeUnarySourceExpr {
			t.Fatalf("expected unary source native subtree, got %#v", nativePlan.Info)
		}
		return
	}
	if _, ok := plan.(*localUnaryPlan); !ok {
		t.Fatalf("expected unary plan, got %T", plan)
	}
}

func TestBuildPlanCreatesNativeLabelReplacePlan(t *testing.T) {
	expr, err := logical.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeLabelTransform || nativeRoot.Info.NodeType != "label_replace" {
		t.Fatalf("expected native label_replace subtree, got %#v", nativeRoot.Info)
	}
}

func TestBuildPlanCreatesNativeLabelJoinPlan(t *testing.T) {
	expr, err := logical.ParseExpression(`label_join(up, "joined", "/", "job", "namespace")`)
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeLabelTransform || nativeRoot.Info.NodeType != "label_join" {
		t.Fatalf("expected native label_join subtree, got %#v", nativeRoot.Info)
	}
}

func TestBuildPlanAcceptsPrometheus3UTF8LabelMutationDestinations(t *testing.T) {
	tests := []struct {
		name  string
		query string
		fn    string
	}{
		{name: "label_replace", query: `label_replace(up, "~invalid", "", "src", "(.*)")`, fn: "label_replace"},
		{name: "label_join", query: `label_join(up, "~invalid", "-", "instance")`, fn: "label_join"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := logical.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}

			built, err := buildPlan(expr)
			if err != nil {
				t.Fatalf("expected %s plan, got error: %v", tc.fn, err)
			}
			nativeRoot, ok := built.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan, got %T", built)
			}
			if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeLabelTransform || nativeRoot.Info.NodeType != tc.fn {
				t.Fatalf("expected native %s subtree, got %#v", tc.fn, nativeRoot.Info)
			}
		})
	}
}

func TestBuildPlanRejectsInvalidLabelReplaceRegex(t *testing.T) {
	expr, err := logical.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "[")`)
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
	expr, err := logical.ParseExpression(`label_join(up, "", "/", "job")`)
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
	expr, err := logical.ParseExpression(`count_values("", up)`)
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

func TestBuildPlanRejectsWhitespaceCountValuesLabel(t *testing.T) {
	expr, err := logical.ParseExpression(`count_values(" ", up)`)
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
	expr, err := logical.ParseExpression("topk(1 + 2, up)")
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
	if buildErr.Support.Difficulty != logical.DifficultyMedium {
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
	expr, err := logical.ParseExpression("increase(sum(up)[5m:])")
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
	if increasePlan.Info == nil || increasePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || increasePlan.Info.NodeType != "increase" {
		t.Fatalf("expected native increase subtree, got %#v", increasePlan.Info)
	}
	if increasePlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native increase subquery child, got %#v", increasePlan.Info)
	}
}

func TestBuildPlanBuildsNativeDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := logical.ParseExpression("delta(sum(up)[5m:])")
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
	if deltaPlan.Info == nil || deltaPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || deltaPlan.Info.NodeType != "delta" {
		t.Fatalf("expected native delta subtree, got %#v", deltaPlan.Info)
	}
	if deltaPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native delta subquery child, got %#v", deltaPlan.Info)
	}
}

func TestBuildPlanBuildsNativeIDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := logical.ParseExpression("idelta(sum(up)[5m:])")
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
	if deltaPlan.Info == nil || deltaPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || deltaPlan.Info.NodeType != "idelta" {
		t.Fatalf("expected native idelta subtree, got %#v", deltaPlan.Info)
	}
	if deltaPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native idelta subquery child, got %#v", deltaPlan.Info)
	}
}

func TestBuildPlanBuildsNativeChangesPlanForSubqueryArg(t *testing.T) {
	expr, err := logical.ParseExpression("changes(sum(up)[5m:])")
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
	if changesPlan.Info == nil || changesPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || changesPlan.Info.NodeType != "changes" {
		t.Fatalf("expected native changes subtree, got %#v", changesPlan.Info)
	}
	if changesPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native changes subquery child, got %#v", changesPlan.Info)
	}
}

func TestBuildPlanBuildsNativeDerivPlanForSubqueryArg(t *testing.T) {
	expr, err := logical.ParseExpression("deriv(sum(up)[5m:])")
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
	if derivPlan.Info == nil || derivPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || derivPlan.Info.NodeType != "deriv" {
		t.Fatalf("expected native deriv subtree, got %#v", derivPlan.Info)
	}
	if derivPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native deriv subquery child, got %#v", derivPlan.Info)
	}
}

func TestBuildPlanBuildsNativeRatePlanForSubqueryArg(t *testing.T) {
	for _, fn := range []string{"rate", "irate"} {
		exprText := fmt.Sprintf("%s(sum(up)[5m:])", fn)
		expr, err := logical.ParseExpression(exprText)
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
		if ratePlan.Info == nil || ratePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || ratePlan.Info.NodeType != fn {
			t.Fatalf("expected native %q subtree, got %#v", fn, ratePlan.Info)
		}
		if ratePlan.Info.RangeFunctionSubquery == nil {
			t.Fatalf("expected native %q subquery child, got %#v", fn, ratePlan.Info)
		}
	}
}

func TestBuildPlanWithContextCreatesDeltaPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression("delta(sum(up)[5m:])")
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
	if deltaPlan.Info == nil || deltaPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || deltaPlan.Info.NodeType != "delta" {
		t.Fatalf("expected native delta subtree, got %#v", deltaPlan.Info)
	}
	if deltaPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native subquery child, got %#v", deltaPlan.Info)
	}
}

func TestBuildPlanWithContextCreatesIDeltaPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression("idelta(sum(up)[5m:])")
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
	if deltaPlan.Info == nil || deltaPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || deltaPlan.Info.NodeType != "idelta" {
		t.Fatalf("expected native idelta subtree, got %#v", deltaPlan.Info)
	}
	if deltaPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native subquery child, got %#v", deltaPlan.Info)
	}
}

func TestBuildPlanWithContextCreatesChangesPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression("changes(sum(up)[5m:])")
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
	if changesPlan.Info == nil || changesPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || changesPlan.Info.NodeType != "changes" {
		t.Fatalf("expected native changes subtree, got %#v", changesPlan.Info)
	}
	if changesPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native subquery child, got %#v", changesPlan.Info)
	}
}

func TestBuildPlanWithContextCreatesDerivPlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression("deriv(sum(up)[5m:])")
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
	if derivPlan.Info == nil || derivPlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || derivPlan.Info.NodeType != "deriv" {
		t.Fatalf("expected native deriv subtree, got %#v", derivPlan.Info)
	}
	if derivPlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native subquery child, got %#v", derivPlan.Info)
	}
}

func TestBuildPlanWithContextCreatesRatePlanForSubqueryInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression("rate(sum(up)[5m:])")
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
	if ratePlan.Info == nil || ratePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || ratePlan.Info.NodeType != "rate" {
		t.Fatalf("expected native rate subtree, got %#v", ratePlan.Info)
	}
	if ratePlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native subquery child, got %#v", ratePlan.Info)
	}
}

func TestBuildPlanWithContextCreatesIncreasePlanInRangeMode(t *testing.T) {
	expr, err := logical.ParseExpression("increase(up[5m])")
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
	expr, err := logical.ParseExpression("increase(sum(up)[5m:])")
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
	if increasePlan.Info == nil || increasePlan.Info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction || increasePlan.Info.NodeType != "increase" {
		t.Fatalf("expected native increase subtree, got %#v", increasePlan.Info)
	}
	if increasePlan.Info.RangeFunctionSubquery == nil {
		t.Fatalf("expected native subquery child, got %#v", increasePlan.Info)
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverRangeFunctionInPreferRangeMode(t *testing.T) {
	cases := []struct {
		name            string
		query           string
		wantSourceShape nativeplan.SubtreeShape
	}{
		{name: "sum_rate", query: "sum(rate(up[5m]))", wantSourceShape: nativeplan.SubtreeShapeRangeFunction},
		{name: "grouped_sum_rate", query: "sum by(job) (rate(up[5m]))", wantSourceShape: nativeplan.SubtreeShapeRangeFunction},
		{name: "sum_increase", query: "sum(increase(up[5m]))", wantSourceShape: nativeplan.SubtreeShapeRangeFunction},
		{name: "scaled_rate", query: "sum(8 * rate(up[5m]))", wantSourceShape: nativeplan.SubtreeShapeValueTransform},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := logical.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}

			execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModePrefer, PreferNativeAggregationPushdown: true})
			if err != nil {
				t.Fatalf("expected native aggregation plan, got error: %v", err)
			}
			nativeRoot, ok := execPlan.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
			}
			if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
				t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
			}
			if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != tc.wantSourceShape {
				t.Fatalf("expected aggregation source shape %s, got %#v", tc.wantSourceShape, nativeRoot.Info.Aggregation.SourceInfo)
			}
		})
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverRateUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("sum(rate(up[5m]))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil {
		t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
	}
	if nativeRoot.Info.Aggregation.SourceInfo == nil || nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		t.Fatalf("expected rate child aggregation source, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestBuildPlanWithContextCreatesNativeRootGroupedAggregationOverRateUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("sum by(job) (rate(up[5m]))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil {
		t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
	}
	aggPlan, ok := nativeRoot.Node.(*logical.AggregationPlan)
	if !ok {
		t.Fatalf("expected AggregationPlan node, got %T", nativeRoot.Node)
	}
	if !sameStrings(aggPlan.Grouping, []string{"job"}) {
		t.Fatalf("expected grouping [job], got %#v", aggPlan.Grouping)
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverIncreaseUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("sum(increase(up[5m]))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		t.Fatalf("expected increase aggregation source, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestBuildPlanWithContextCreatesNativeRootAggregationOverScaledRateUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("sum(8 * rate(up[5m]))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeValueTransform {
		t.Fatalf("expected value-transform aggregation source, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestBuildPlanWithContextCreatesNativeUnaryRootOverAggregatedRateUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("- sum(rate(up[5m]))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeValueTransform || nativeRoot.Info.ValueTransform == nil {
		t.Fatalf("expected value-transform unary subtree, got %#v", nativeRoot.Info)
	}
	if shape := valueTransformChildShape(nativeRoot.Info); shape != nativeplan.SubtreeShapeAggregation {
		t.Fatalf("expected aggregated child under unary wrapper, got %#v", shape)
	}
}

func TestBuildPlanWithContextCreatesNativeComparisonRootOverAggregatedIncreaseUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("sum by(job) (increase(up[5m])) > 0")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeValueTransform || nativeRoot.Info.ValueTransform == nil {
		t.Fatalf("expected value-transform comparison subtree, got %#v", nativeRoot.Info)
	}
	if nativeRoot.Info.ValueTransform.FilterExpr == "" {
		t.Fatalf("expected comparison filter template, got %#v", nativeRoot.Info.ValueTransform)
	}
	if shape := valueTransformChildShape(nativeRoot.Info); shape != nativeplan.SubtreeShapeAggregation {
		t.Fatalf("expected aggregated child under comparison wrapper, got %#v", shape)
	}
}

func TestBuildPlanWithContextCreatesNativeScalarWrapperRootOverHistogramQuantileUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("histogram_quantile(0.5, sum by(le, job) (rate(http_request_duration_seconds_bucket[5m]))) * 1")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeValueTransform || nativeRoot.Info.ValueTransform == nil {
		t.Fatalf("expected value-transform histogram wrapper, got %#v", nativeRoot.Info)
	}
	if shape := valueTransformChildShape(nativeRoot.Info); shape != nativeplan.SubtreeShapeHistogramFunction {
		t.Fatalf("expected histogram function child under scalar wrapper, got %#v", shape)
	}
}

func TestBuildPlanWithContextCreatesNativeScalarWrapperRootOverRateRatioUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m]) * 1e3")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeValueTransform || nativeRoot.Info.ValueTransform == nil {
		t.Fatalf("expected value-transform ratio wrapper, got %#v", nativeRoot.Info)
	}
	if shape := valueTransformChildShape(nativeRoot.Info); shape != nativeplan.SubtreeShapeBinaryVectorJoin {
		t.Fatalf("expected binary-join child under scalar wrapper, got %#v", shape)
	}
}

func TestBuildPlanWithContextCreatesNativeRoundRootOverAggregatedRateRatioUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("round(sum(rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m])) by(pod))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeValueTransform || nativeRoot.Info.ValueTransform == nil {
		t.Fatalf("expected value-transform round subtree, got %#v", nativeRoot.Info)
	}
	if shape := valueTransformChildShape(nativeRoot.Info); shape != nativeplan.SubtreeShapeAggregation {
		t.Fatalf("expected aggregated child under round wrapper, got %#v", shape)
	}
}

func TestBuildPlanWithContextCreatesNativeCountValuesRootUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression(`count_values("sample_value", up)`)
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native count_values aggregation subtree, got %#v", nativeRoot.Info)
	}
	aggPlan, ok := nativeRoot.Node.(*logical.AggregationPlan)
	if !ok {
		t.Fatalf("expected AggregationPlan node, got %T", nativeRoot.Node)
	}
	if aggPlan.Op != parser.COUNT_VALUES || aggPlan.ParamString != "sample_value" {
		t.Fatalf("expected count_values aggregation metadata, got op=%v param=%q", aggPlan.Op, aggPlan.ParamString)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeLeafSource {
		t.Fatalf("expected leaf child under count_values, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestBuildPlanWithContextCreatesNativeLimitKRootUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("limitk(2, up)")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil {
		t.Fatalf("expected native limitk aggregation subtree, got %#v", nativeRoot.Info)
	}
	if aggPlan, ok := nativeRoot.Node.(*logical.AggregationPlan); !ok || aggPlan.Op != parser.LIMITK {
		t.Fatalf("expected LIMITK aggregation op, got %T / %#v", nativeRoot.Node, nativeRoot.Node)
	}
}

func TestBuildPlanWithContextCreatesNativeLimitRatioRootUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("limit_ratio(0.5, up)")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil {
		t.Fatalf("expected native limit_ratio aggregation subtree, got %#v", nativeRoot.Info)
	}
	if aggPlan, ok := nativeRoot.Node.(*logical.AggregationPlan); !ok || aggPlan.Op != parser.LIMIT_RATIO {
		t.Fatalf("expected LIMIT_RATIO aggregation op, got %T / %#v", nativeRoot.Node, nativeRoot.Node)
	}
}

func TestBuildPlanWithContextCreatesNativeSetAndRootUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("up and on(job) up")
	if err != nil {
		t.Fatal(err)
	}
	execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native set-and root, got error: %v", err)
	}
	nativeRoot, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan root, got %T", execPlan)
	}
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
		t.Fatalf("expected native binary join subtree, got %#v", nativeRoot.Info)
	}
	if nativeRoot.Info.JoinShape != nativeplan.JoinShapeManyToMany {
		t.Fatalf("expected many-to-many LAND join shape, got %#v", nativeRoot.Info.JoinShape)
	}
}

func TestBuildPlanWithContextCreatesNativeSetOrAndUnlessInPreferMode(t *testing.T) {
	for _, tc := range []struct {
		query string
		op    parser.ItemType
	}{
		{query: "up or on(job) up", op: parser.LOR},
		{query: "up unless on(job) up", op: parser.LUNLESS},
	} {
		t.Run(tc.query, func(t *testing.T) {
			expr, err := logical.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			execPlan, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModePrefer})
			if err != nil {
				t.Fatalf("expected native set-operator plan for %q, got error: %v", tc.query, err)
			}
			nativePlan, ok := execPlan.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan for %q, got %T", tc.query, execPlan)
			}
			if nativePlan.Info == nil || nativePlan.Info.SubtreeShape != nativeplan.SubtreeShapeBinaryVectorJoin {
				t.Fatalf("expected native binary join subtree for %q, got %#v", tc.query, nativePlan.Info)
			}
			if nativePlan.Info.JoinShape != nativeplan.JoinShapeManyToMany {
				t.Fatalf("expected many-to-many native join for %q, got %#v", tc.query, nativePlan.Info.JoinShape)
			}
		})
	}
}

func TestBuildPlanWithContextCreatesNativeTopKRootOverIRateUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("topk(5, irate(up[1m]))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native topk aggregation subtree, got %#v", nativeRoot.Info)
	}
	if aggPlan, ok := nativeRoot.Node.(*logical.AggregationPlan); !ok || aggPlan.Op != parser.TOPK {
		t.Fatalf("expected topk aggregation op, got %T / %#v", nativeRoot.Node, nativeRoot.Node)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		t.Fatalf("expected irate child under topk, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestBuildPlanWithContextCreatesNativeTopKRootOverHistogramQuantileUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("topk(2, histogram_quantile(0.9, sum(rate(http_request_duration_seconds_bucket[5m])) by(le)))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native topk aggregation subtree, got %#v", nativeRoot.Info)
	}
	if aggPlan, ok := nativeRoot.Node.(*logical.AggregationPlan); !ok || aggPlan.Op != parser.TOPK {
		t.Fatalf("expected topk aggregation op, got %T / %#v", nativeRoot.Node, nativeRoot.Node)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeHistogramFunction {
		t.Fatalf("expected histogram quantile child under topk, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
	}
}

func TestBuildPlanWithContextCreatesNativeSumOverOrVectorZeroUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression("sum(rate(up[5m]) or vector(0))")
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
	if nativeRoot.Info == nil || nativeRoot.Info.SubtreeShape != nativeplan.SubtreeShapeAggregation || nativeRoot.Info.Aggregation == nil || nativeRoot.Info.Aggregation.SourceInfo == nil {
		t.Fatalf("expected native aggregation subtree, got %#v", nativeRoot.Info)
	}
	if !nativeRoot.Info.Aggregation.EmitZeroOnEmpty {
		t.Fatalf("expected zero-fill aggregation flag, got %#v", nativeRoot.Info.Aggregation)
	}
	if nativeRoot.Info.Aggregation.SourceInfo.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		t.Fatalf("expected rate child under zero-fill aggregation, got %#v", nativeRoot.Info.Aggregation.SourceInfo)
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

func TestLocalSubqueryPlanRejectsInnerPointCap(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	calls := 0
	plan := &localSubqueryPlan{
		Expr:               expr,
		Range:              2 * time.Minute,
		Step:               time.Minute,
		MaxPointsPerSeries: 2,
		Child: testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
			calls++
			return model.VectorValue{}, nil
		}},
	}

	_, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
	if err == nil || !strings.Contains(err.Error(), "exceeding configured limit 2") {
		t.Fatalf("expected subquery point cap error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected cap to reject before child execution, got %d calls", calls)
	}
}

func TestLocalSubqueryPlanChecksContextCancellation(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[2m:1m]")
	calls := 0
	plan := &localSubqueryPlan{
		Expr:  expr,
		Range: 2 * time.Minute,
		Step:  time.Minute,
		Child: testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
			calls++
			return model.VectorValue{}, nil
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := plan.execute(ctx, &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(180, 0).UTC()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected cancellation check before child execution, got %d calls", calls)
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

// TestBuildPlanThreadsDefaultEvaluationIntervalToSubqueryPlan asserts the
// planner captures PlanContext.DefaultEvaluationInterval on the subquery
// plan node at build time, so nested subqueries keep the configured
// server-side default even though child EvalParams are reconstructed.
func TestBuildPlanThreadsDefaultEvaluationIntervalToSubqueryPlan(t *testing.T) {
	expr, err := logical.ParseExpression("(up * 100)[10m:]")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, NativeLoweringMode: NativeLoweringModeOff, DefaultEvaluationInterval: 30 * time.Second})
	if err != nil {
		t.Fatalf("expected local subquery plan, got error: %v", err)
	}
	subquery, ok := built.(*localSubqueryPlan)
	if !ok {
		t.Fatalf("expected localSubqueryPlan, got %T", built)
	}
	if subquery.DefaultEvaluationInterval != 30*time.Second {
		t.Fatalf("expected DefaultEvaluationInterval 30s threaded from PlanContext, got %v", subquery.DefaultEvaluationInterval)
	}
}

// TestLocalSubqueryPlanRangeModeNoStepUsesDefaultEvaluationInterval locks
// issue #35: a no-step subquery evaluated in range mode must fill its step
// with the server-side default evaluation interval (1m), never the outer
// query step. With the outer step at 300s the buggy behavior would evaluate
// the child every 300s (5 calls); the fixed behavior evaluates every 60s.
func TestLocalSubqueryPlanRangeModeNoStepUsesDefaultEvaluationInterval(t *testing.T) {
	expr := mustParseExpr(t, "(up * 100)[10m:]")
	calls := make([]int64, 0)
	plan := &localSubqueryPlan{Expr: expr, Range: 10 * time.Minute, Step: 0, Child: testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
		calls = append(calls, params.EvaluationTime.Unix())
		return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 1}}}, nil
	}}}

	_, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeRange, Start: time.Unix(600, 0).UTC(), End: time.Unix(1200, 0).UTC(), Step: 300 * time.Second})
	if err != nil {
		t.Fatalf("expected range-mode no-step subquery execution, got error: %v", err)
	}
	if len(calls) != 21 {
		t.Fatalf("expected 21 child evaluations on the 60s default grid, got %d (%v)", len(calls), calls)
	}
	for i, ts := range calls {
		if want := int64(i * 60); ts != want {
			t.Fatalf("expected child call %d at %d (60s default interval), got %d", i, want, ts)
		}
	}
}

// TestLocalSubqueryPlanUsesConfiguredDefaultEvaluationInterval covers the
// configured (non-1m) server default and confirms an explicit subquery step
// always wins over it.
func TestLocalSubqueryPlanUsesConfiguredDefaultEvaluationInterval(t *testing.T) {
	newChild := func(calls *[]int64) testQueryPlan {
		return testQueryPlan{executeFn: func(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
			*calls = append(*calls, params.EvaluationTime.Unix())
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: float64(params.EvaluationTime.Unix()), Value: 1}}}, nil
		}}
	}

	t.Run("no_step_uses_configured_interval", func(t *testing.T) {
		expr := mustParseExpr(t, "(up * 100)[2m:]")
		calls := make([]int64, 0)
		plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: 0, DefaultEvaluationInterval: 30 * time.Second, Child: newChild(&calls)}
		if _, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(120, 0).UTC()}); err != nil {
			t.Fatalf("expected configured-interval subquery execution, got error: %v", err)
		}
		want := []int64{0, 30, 60, 90, 120}
		if len(calls) != len(want) {
			t.Fatalf("expected %d child evaluations, got %d (%v)", len(want), len(calls), calls)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("expected child call %d at %d, got %d", i, want[i], calls[i])
			}
		}
	})

	t.Run("explicit_step_wins_over_configured_interval", func(t *testing.T) {
		expr := mustParseExpr(t, "(up * 100)[2m:1m]")
		calls := make([]int64, 0)
		plan := &localSubqueryPlan{Expr: expr, Range: 2 * time.Minute, Step: time.Minute, DefaultEvaluationInterval: 30 * time.Second, Child: newChild(&calls)}
		if _, err := plan.execute(context.Background(), &Evaluator{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(120, 0).UTC()}); err != nil {
			t.Fatalf("expected explicit-step subquery execution, got error: %v", err)
		}
		want := []int64{0, 60, 120}
		if len(calls) != len(want) {
			t.Fatalf("expected %d child evaluations, got %d (%v)", len(want), len(calls), calls)
		}
		for i := range want {
			if calls[i] != want[i] {
				t.Fatalf("expected child call %d at %d, got %d", i, want[i], calls[i])
			}
		}
	})
}

// TestLocalSubqueryPlanDelegatedPathUsesPlanCapturedInterval locks that the
// delegated branch of localSubqueryPlan.execute fills a no-step subquery with
// the plan-captured DefaultEvaluationInterval, matching the local
// executionWindow branch, rather than the evaluator-level default. The two are
// set to divergent values here (plan 30s, evaluator 90s) so the delegated
// PromQL text unambiguously reveals which one was used; in production both are
// sourced from the same Options.DefaultEvaluationInterval and cannot differ.
func TestLocalSubqueryPlanDelegatedPathUsesPlanCapturedInterval(t *testing.T) {
	var capturedPromQL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		capturedPromQL = r.FormValue("param_promql")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, `{"tags":[["job","api"]],"timestamp":"2026-04-20 11:34:00.000","value":1}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	// Vector-valued root wrapping a no-step subquery so the instant-mode
	// delegated path is taken (a matrix-root subquery would fall to local).
	expr := mustParseExpr(t, "last_over_time(up[15m:])")
	plan := &localSubqueryPlan{
		Expr:                      expr,
		Range:                     15 * time.Minute,
		DelegatedLeafCompatible:   true,
		DefaultEvaluationInterval: 30 * time.Second,
	}
	evaluator := &Evaluator{database: "observability", table: "prometheus", client: client, defaultEvaluationInterval: 90 * time.Second}

	if _, err := plan.execute(context.Background(), evaluator, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1234, 0).UTC()}); err != nil {
		t.Fatalf("expected delegated subquery execution, got error: %v", err)
	}

	// The subquery step is the plan-captured 30s (the range carries a 1ms
	// delegation pad, so match on the step token only).
	if !strings.Contains(capturedPromQL, ":30s]") {
		t.Fatalf("expected delegated PromQL to fill the no-step subquery with the plan-captured 30s interval, got %q", capturedPromQL)
	}
	if strings.Contains(capturedPromQL, "1m30s") {
		t.Fatalf("delegated PromQL used the evaluator-level 90s interval instead of the plan-captured value, got %q", capturedPromQL)
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

func TestBuildPlanWithContextBuildsNativeScalarLiteralPlanUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression(`42`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native scalar literal plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan for scalar literal under force_supported, got %T", built)
	}
	if native.Kind != "scalar_literal" {
		t.Fatalf("expected scalar_literal native kind, got %#v", native)
	}
}

func TestBuildPlanWithContextBuildsNativeScalarArithmeticPlanUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression(`1 * 2 + 4 / 6 - 10 % 2 ^ 2`)
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("expected native scalar arithmetic plan, got error: %v", err)
	}
	native, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan for scalar arithmetic under force_supported, got %T", built)
	}
	if native.Kind != "binary" {
		t.Fatalf("expected binary native kind, got %#v", native)
	}
}

func TestBuildPlanWithContextBuildsNativeVectorScalarExpressionPlansUnderForceSupported(t *testing.T) {
	tests := []string{
		`demo_num_cpus + (1 == bool 2)`,
		`demo_memory_usage_bytes + (1 * 2 + 4 / 6 - 10)`,
		`(1 * 2 + 4 / 6 - (10%7)^2) + demo_memory_usage_bytes`,
		`demo_memory_usage_bytes == bool 1.2345`,
		`1.2345 == bool demo_memory_usage_bytes`,
		`time() + time()`,
		`time() == bool time()`,
		`demo_num_cpus >= time()`,
		`time() >= demo_num_cpus`,
		`time() >= bool 1`,
		`1 >= bool time()`,
	}

	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			expr, err := logical.ParseExpression(query)
			if err != nil {
				t.Fatal(err)
			}

			built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), NativeLoweringMode: NativeLoweringModeForceSupported})
			if err != nil {
				t.Fatalf("expected native scalar/vector composition plan, got error: %v", err)
			}
			native, ok := built.(*nativeSubtreePlan)
			if !ok {
				t.Fatalf("expected nativeSubtreePlan under force_supported, got %T", built)
			}
			if native.Kind != "binary" {
				t.Fatalf("expected binary native kind, got %#v", native)
			}
		})
	}
}

func TestBuildPlanWithContextBuildsNativeAbsentPlanUnderForceSupported(t *testing.T) {
	expr, err := logical.ParseExpression(`absent(up{job="api"})`)
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
	expr, err := logical.ParseExpression(`absent_over_time(up{job="api"}[5m])`)
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

// valueTransformChildShape returns the SubtreeShape of the vector child
// wrapped by a ValueTransform-shaped LoweringInfo. Single-child wrappers
// (unary, round, histogram_quantile, sort/sort_desc, etc.) always walk
// Children[0]. Two-child BinaryPlan wrappers consult
// ValueTransform.VectorChildOnLeft to select Children[0] vs Children[1].
func valueTransformChildShape(info *nativeplan.LoweringInfo) nativeplan.SubtreeShape {
	if info == nil || info.ValueTransform == nil || len(info.Children) == 0 {
		return ""
	}
	idx := 0
	if len(info.Children) > 1 && !info.ValueTransform.VectorChildOnLeft {
		idx = 1
	}
	if idx >= len(info.Children) || info.Children[idx] == nil {
		return ""
	}
	return info.Children[idx].SubtreeShape
}
