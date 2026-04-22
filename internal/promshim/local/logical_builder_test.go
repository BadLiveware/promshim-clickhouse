package local

import (
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildLogicalPlanCreatesDelegatedLeafPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical leaf plan, got error: %v", err)
	}
	leaf, ok := plan.(*logicalLeafExprPlan)
	if !ok {
		t.Fatalf("expected logicalLeafExprPlan, got %T", plan)
	}
	if leaf.ExprString() != "up" {
		t.Fatalf("expected expr string to be preserved, got %q", leaf.ExprString())
	}
	if leaf.ValueType() != parser.ValueTypeVector {
		t.Fatalf("expected vector value type, got %q", leaf.ValueType())
	}
}

func TestBuildLogicalPlanCreatesAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical aggregation plan, got error: %v", err)
	}
	agg, ok := plan.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", plan)
	}
	if agg.Op != parser.SUM {
		t.Fatalf("expected sum op, got %v", agg.Op)
	}
	if !sameStrings(agg.Grouping, []string{"job"}) {
		t.Fatalf("unexpected grouping: %#v", agg.Grouping)
	}
	if agg.ParamNumber != nil || agg.ParamString != "" {
		t.Fatalf("did not expect aggregation parameter, got %#v", agg)
	}
	if _, ok := agg.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected logical leaf child, got %T", agg.Child)
	}
}

func TestBuildLogicalPlanPreservesTimeModifierLeafExpression(t *testing.T) {
	expr, err := plan.ParseExpression("up offset 5m")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical leaf plan, got error: %v", err)
	}
	leaf, ok := logical.(*logicalLeafExprPlan)
	if !ok {
		t.Fatalf("expected logicalLeafExprPlan, got %T", logical)
	}
	if leaf.ExprString() != "up offset 5m" {
		t.Fatalf("expected offset expression to be preserved, got %q", leaf.ExprString())
	}
}

func TestBuildLogicalPlanCreatesTopKPlan(t *testing.T) {
	expr, err := plan.ParseExpression("topk(3, up)")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical topk plan, got error: %v", err)
	}
	agg, ok := logical.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", logical)
	}
	if agg.ParamNumber == nil || *agg.ParamNumber != 3 {
		t.Fatalf("expected topk parameter 3, got %#v", agg.ParamNumber)
	}
	if agg.ParamString != "" {
		t.Fatalf("did not expect string aggregation parameter, got %#v", agg.ParamString)
	}
}

func TestBuildLogicalPlanCreatesTier1AdditionalAggregationPlans(t *testing.T) {
	queries := []struct {
		query string
		op    parser.ItemType
		param *float64
	}{
		{query: "stddev(up)", op: parser.STDDEV},
		{query: "stdvar(up)", op: parser.STDVAR},
		{query: "group(up)", op: parser.GROUP},
		{query: "quantile(0.9, up)", op: parser.QUANTILE, param: floatPtr(0.9)},
	}
	for _, tc := range queries {
		logical, err := BuildLogicalPlan(mustParseExpr(t, tc.query))
		if err != nil {
			t.Fatalf("expected logical aggregation plan for %q, got error: %v", tc.query, err)
		}
		agg, ok := logical.(*logicalAggregationPlan)
		if !ok {
			t.Fatalf("expected logicalAggregationPlan for %q, got %T", tc.query, logical)
		}
		if agg.Op != tc.op {
			t.Fatalf("expected aggregation op %v for %q, got %v", tc.op, tc.query, agg.Op)
		}
		if tc.param == nil {
			if agg.ParamNumber != nil {
				t.Fatalf("did not expect numeric aggregation parameter for %q, got %#v", tc.query, agg.ParamNumber)
			}
		} else if agg.ParamNumber == nil || *agg.ParamNumber != *tc.param {
			t.Fatalf("expected numeric aggregation parameter %v for %q, got %#v", *tc.param, tc.query, agg.ParamNumber)
		}
	}
}

func TestBuildLogicalPlanCreatesHistogramQuantilePlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical histogram_quantile plan, got error: %v", err)
	}
	histogramPlan, ok := logical.(*logicalHistogramQuantilePlan)
	if !ok {
		t.Fatalf("expected logicalHistogramQuantilePlan, got %T", logical)
	}
	if histogramPlan.Quantile != 0.9 {
		t.Fatalf("expected quantile 0.9, got %#v", histogramPlan.Quantile)
	}
	agg, ok := histogramPlan.Child.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected histogram_quantile child aggregation plan, got %T", histogramPlan.Child)
	}
	if ratePlan, ok := agg.Child.(*logicalRatePlan); !ok || ratePlan.Func != "rate" {
		t.Fatalf("expected logicalRatePlan child under histogram_quantile aggregation, got %T (%#v)", agg.Child, agg.Child)
	}
}

func TestBuildLogicalPlanCreatesHistogramProjectionPlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_count(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical histogram projection plan, got error: %v", err)
	}
	histogramPlan, ok := logical.(*logicalHistogramProjectionPlan)
	if !ok {
		t.Fatalf("expected logicalHistogramProjectionPlan, got %T", logical)
	}
	if histogramPlan.Func != "histogram_count" {
		t.Fatalf("expected histogram_count function, got %#v", histogramPlan.Func)
	}
	agg, ok := histogramPlan.Child.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected histogram_count child aggregation plan, got %T", histogramPlan.Child)
	}
	if ratePlan, ok := agg.Child.(*logicalRatePlan); !ok || ratePlan.Func != "rate" {
		t.Fatalf("expected logicalRatePlan child under histogram_count aggregation, got %T (%#v)", agg.Child, agg.Child)
	}
}

func TestBuildLogicalPlanCreatesHistogramFractionPlan(t *testing.T) {
	expr, err := plan.ParseExpression("histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical histogram fraction plan, got error: %v", err)
	}
	histogramPlan, ok := logical.(*logicalHistogramFractionPlan)
	if !ok {
		t.Fatalf("expected logicalHistogramFractionPlan, got %T", logical)
	}
	if histogramPlan.Lower != 0 || histogramPlan.Upper != 1 {
		t.Fatalf("expected bounds [0,1], got [%v,%v]", histogramPlan.Lower, histogramPlan.Upper)
	}
	agg, ok := histogramPlan.Child.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected histogram_fraction child aggregation plan, got %T", histogramPlan.Child)
	}
	if ratePlan, ok := agg.Child.(*logicalRatePlan); !ok || ratePlan.Func != "rate" {
		t.Fatalf("expected logicalRatePlan child under histogram_fraction aggregation, got %T (%#v)", agg.Child, agg.Child)
	}
}

func TestBuildLogicalPlanCreatesTier1AdditionalRangeFunctionPlans(t *testing.T) {
	for _, fn := range []string{"stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "resets"} {
		logical, err := BuildLogicalPlan(mustParseExpr(t, fn+"(up[5m])"))
		if err != nil {
			t.Fatalf("expected logical %s plan, got error: %v", fn, err)
		}
		rangeFn, ok := logical.(*logicalRangeFunctionPlan)
		if !ok {
			t.Fatalf("expected logicalRangeFunctionPlan for %s, got %T", fn, logical)
		}
		if rangeFn.Func != fn {
			t.Fatalf("expected range function %s, got %q", fn, rangeFn.Func)
		}
	}
}

func TestBuildLogicalPlanCreatesIncreasePlan(t *testing.T) {
	expr, err := plan.ParseExpression("increase(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical increase plan, got error: %v", err)
	}
	increasePlan, ok := logical.(*logicalIncreasePlan)
	if !ok {
		t.Fatalf("expected logicalIncreasePlan, got %T", logical)
	}
	if _, ok := increasePlan.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected delegated leaf child, got %T", increasePlan.Child)
	}
}

func TestBuildLogicalPlanCreatesIncreasePlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("increase(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical increase plan, got error: %v", err)
	}
	increasePlan, ok := logical.(*logicalIncreasePlan)
	if !ok {
		t.Fatalf("expected logicalIncreasePlan, got %T", logical)
	}
	if _, ok := increasePlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", increasePlan.Child)
	}
}

func TestBuildLogicalPlanCreatesRatePlanForDirectSelectorArg(t *testing.T) {
	for _, fn := range []string{"rate", "irate"} {
		expr, err := plan.ParseExpression(fn + "(up[5m])")
		if err != nil {
			t.Fatal(err)
		}

		logical, err := BuildLogicalPlan(expr)
		if err != nil {
			t.Fatalf("expected logical %s plan, got error: %v", fn, err)
		}
		ratePlan, ok := logical.(*logicalRatePlan)
		if !ok {
			t.Fatalf("expected logicalRatePlan, got %T", logical)
		}
		if ratePlan.Func != fn {
			t.Fatalf("expected function %s, got %q", fn, ratePlan.Func)
		}
		if _, ok := ratePlan.Child.(*logicalLeafExprPlan); !ok {
			t.Fatalf("expected logical leaf child for %s, got %T", fn, ratePlan.Child)
		}
	}
}

func TestBuildLogicalPlanCreatesRatePlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("rate(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical rate plan, got error: %v", err)
	}
	ratePlan, ok := logical.(*logicalRatePlan)
	if !ok {
		t.Fatalf("expected logicalRatePlan, got %T", logical)
	}
	if ratePlan.Func != "rate" {
		t.Fatalf("expected function rate, got %q", ratePlan.Func)
	}
	if _, ok := ratePlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", ratePlan.Child)
	}
}

func TestBuildLogicalPlanCreatesIratePlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("irate(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical irate plan, got error: %v", err)
	}
	ratePlan, ok := logical.(*logicalRatePlan)
	if !ok {
		t.Fatalf("expected logicalRatePlan, got %T", logical)
	}
	if ratePlan.Func != "irate" {
		t.Fatalf("expected function irate, got %q", ratePlan.Func)
	}
	if _, ok := ratePlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", ratePlan.Child)
	}
}

func TestBuildLogicalPlanCreatesCounterPlansForDirectSelectorArgs(t *testing.T) {
	cases := []struct {
		query    string
		planType any
	}{
		{query: "delta(up[5m])", planType: (*logicalDeltaPlan)(nil)},
		{query: "idelta(up[5m])", planType: (*logicalDeltaPlan)(nil)},
		{query: "changes(up[5m])", planType: (*logicalChangesPlan)(nil)},
		{query: "deriv(up[5m])", planType: (*logicalDerivPlan)(nil)},
	}
	for _, tc := range cases {
		expr, err := plan.ParseExpression(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		logical, err := BuildLogicalPlan(expr)
		if err != nil {
			t.Fatalf("expected logical plan for %q, got error: %v", tc.query, err)
		}
		switch tc.planType.(type) {
		case *logicalDeltaPlan:
			plan, ok := logical.(*logicalDeltaPlan)
			if !ok {
				t.Fatalf("expected logicalDeltaPlan for %q, got %T", tc.query, logical)
			}
			if _, ok := plan.Child.(*logicalLeafExprPlan); !ok {
				t.Fatalf("expected logical leaf child for %q, got %T", tc.query, plan.Child)
			}
		case *logicalChangesPlan:
			plan, ok := logical.(*logicalChangesPlan)
			if !ok {
				t.Fatalf("expected logicalChangesPlan for %q, got %T", tc.query, logical)
			}
			if _, ok := plan.Child.(*logicalLeafExprPlan); !ok {
				t.Fatalf("expected logical leaf child for %q, got %T", tc.query, plan.Child)
			}
		case *logicalDerivPlan:
			plan, ok := logical.(*logicalDerivPlan)
			if !ok {
				t.Fatalf("expected logicalDerivPlan for %q, got %T", tc.query, logical)
			}
			if _, ok := plan.Child.(*logicalLeafExprPlan); !ok {
				t.Fatalf("expected logical leaf child for %q, got %T", tc.query, plan.Child)
			}
		}
	}
}

func TestBuildLogicalPlanCreatesDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("delta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical delta plan, got error: %v", err)
	}
	deltaPlan, ok := logical.(*logicalDeltaPlan)
	if !ok {
		t.Fatalf("expected logicalDeltaPlan, got %T", logical)
	}
	if deltaPlan.Func != "delta" {
		t.Fatalf("expected function delta, got %q", deltaPlan.Func)
	}
	if _, ok := deltaPlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", deltaPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesIDeltaPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("idelta(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical idelta plan, got error: %v", err)
	}
	deltaPlan, ok := logical.(*logicalDeltaPlan)
	if !ok {
		t.Fatalf("expected logicalDeltaPlan, got %T", logical)
	}
	if deltaPlan.Func != "idelta" {
		t.Fatalf("expected function idelta, got %q", deltaPlan.Func)
	}
	if _, ok := deltaPlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", deltaPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesChangesPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("changes(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical changes plan, got error: %v", err)
	}
	changesPlan, ok := logical.(*logicalChangesPlan)
	if !ok {
		t.Fatalf("expected logicalChangesPlan, got %T", logical)
	}
	if _, ok := changesPlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", changesPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesDerivPlanForSubqueryArg(t *testing.T) {
	expr, err := plan.ParseExpression("deriv(sum(up)[5m:])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical deriv plan, got error: %v", err)
	}
	derivPlan, ok := logical.(*logicalDerivPlan)
	if !ok {
		t.Fatalf("expected logicalDerivPlan, got %T", logical)
	}
	if _, ok := derivPlan.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected logical subquery child, got %T", derivPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesVectorPlan(t *testing.T) {
	expr, err := plan.ParseExpression("vector(0)")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical vector plan, got error: %v", err)
	}
	vecPlan, ok := logical.(*logicalVectorPlan)
	if !ok {
		t.Fatalf("expected logicalVectorPlan, got %T", logical)
	}
	if _, ok := vecPlan.Child.(*logicalScalarLiteralPlan); !ok {
		t.Fatalf("expected scalar child for vector(), got %T", vecPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesRoundPlan(t *testing.T) {
	expr, err := plan.ParseExpression("round(up)")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical round plan, got error: %v", err)
	}
	roundPlan, ok := logical.(*logicalRoundPlan)
	if !ok {
		t.Fatalf("expected logicalRoundPlan, got %T", logical)
	}
	if roundPlan.Decimals != nil {
		t.Fatalf("expected nil decimals for round(up), got %#v", roundPlan.Decimals)
	}
	if _, ok := roundPlan.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected vector child for round(), got %T", roundPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesPointwiseFunctionPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "abs(up)"))
	if err != nil {
		t.Fatalf("expected logical pointwise plan, got error: %v", err)
	}
	pointwise, ok := logical.(*logicalPointwiseFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalPointwiseFunctionPlan, got %T", logical)
	}
	if pointwise.Func != "abs" {
		t.Fatalf("expected abs function, got %q", pointwise.Func)
	}
	if _, ok := pointwise.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected vector child for abs(), got %T", pointwise.Child)
	}
}

func TestBuildLogicalPlanCreatesPointwiseFunctionPlanWithoutChildForDateDefault(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "minute()"))
	if err != nil {
		t.Fatalf("expected logical pointwise plan, got error: %v", err)
	}
	pointwise, ok := logical.(*logicalPointwiseFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalPointwiseFunctionPlan, got %T", logical)
	}
	if pointwise.Func != "minute" || pointwise.Child != nil {
		t.Fatalf("expected minute() without child, got %#v", pointwise)
	}
}

func TestBuildLogicalPlanCreatesSortPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "sort_by_label(up, \"job\", \"instance\")"))
	if err != nil {
		t.Fatalf("expected logical sort plan, got error: %v", err)
	}
	sortPlan, ok := logical.(*logicalSortPlan)
	if !ok {
		t.Fatalf("expected logicalSortPlan, got %T", logical)
	}
	if sortPlan.Func != "sort_by_label" || len(sortPlan.Labels) != 2 || sortPlan.Labels[0] != "job" || sortPlan.Labels[1] != "instance" {
		t.Fatalf("unexpected sort plan labels: %#v", sortPlan)
	}
}

func TestBuildLogicalPlanCreatesScalarBuiltinPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "time()"))
	if err != nil {
		t.Fatalf("expected logical scalar builtin plan, got error: %v", err)
	}
	scalarBuiltin, ok := logical.(*logicalScalarBuiltinPlan)
	if !ok {
		t.Fatalf("expected logicalScalarBuiltinPlan, got %T", logical)
	}
	if scalarBuiltin.Func != "time" {
		t.Fatalf("expected time builtin, got %q", scalarBuiltin.Func)
	}
}

func TestBuildLogicalPlanCreatesScalarConvertPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "scalar(up)"))
	if err != nil {
		t.Fatalf("expected logical scalar convert plan, got error: %v", err)
	}
	scalarConvert, ok := logical.(*logicalScalarConvertPlan)
	if !ok {
		t.Fatalf("expected logicalScalarConvertPlan, got %T", logical)
	}
	if _, ok := scalarConvert.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected vector child for scalar(), got %T", scalarConvert.Child)
	}
}

func TestBuildLogicalPlanCreatesPredictLinearPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "predict_linear(up[5m], 60)"))
	if err != nil {
		t.Fatalf("expected logical predict_linear plan, got error: %v", err)
	}
	rangePlan, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangePlan.Func != "predict_linear" || rangePlan.ParamNumber == nil || *rangePlan.ParamNumber != 60 {
		t.Fatalf("unexpected predict_linear plan: %#v", rangePlan)
	}
	if _, ok := rangePlan.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected matrix child for predict_linear(), got %T", rangePlan.Child)
	}
}

func TestBuildLogicalPlanCreatesDoubleExponentialSmoothingPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "double_exponential_smoothing(up[5m], 0.5, 0.3)"))
	if err != nil {
		t.Fatalf("expected logical smoothing plan, got error: %v", err)
	}
	rangePlan, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangePlan.Func != "double_exponential_smoothing" || len(rangePlan.ParamNumbers) != 2 || *rangePlan.ParamNumbers[0] != 0.5 || *rangePlan.ParamNumbers[1] != 0.3 {
		t.Fatalf("unexpected smoothing plan: %#v", rangePlan)
	}
}

func TestBuildLogicalPlanCreatesInfoPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "info(up, {k8s_cluster_name=\"prod\"})"))
	if err != nil {
		t.Fatalf("expected logical info plan, got error: %v", err)
	}
	infoPlan, ok := logical.(*logicalInfoPlan)
	if !ok {
		t.Fatalf("expected logicalInfoPlan, got %T", logical)
	}
	if len(infoPlan.SelectorMatchers) != 1 || infoPlan.SelectorMatchers[0].Name != "k8s_cluster_name" {
		t.Fatalf("unexpected info selector matchers: %#v", infoPlan.SelectorMatchers)
	}
	if _, ok := infoPlan.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected vector child for info(), got %T", infoPlan.Child)
	}
}

func TestBuildLogicalPlanCreatesPiBuiltinPlan(t *testing.T) {
	logical, err := BuildLogicalPlan(mustParseExpr(t, "pi()"))
	if err != nil {
		t.Fatalf("expected logical scalar builtin plan, got error: %v", err)
	}
	scalarBuiltin, ok := logical.(*logicalScalarBuiltinPlan)
	if !ok {
		t.Fatalf("expected logicalScalarBuiltinPlan, got %T", logical)
	}
	if scalarBuiltin.Func != "pi" {
		t.Fatalf("expected pi builtin, got %#v", scalarBuiltin)
	}
}

func TestBuildLogicalPlanCreatesNestedAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("count(count by (job) (up))")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical nested aggregation plan, got error: %v", err)
	}
	outer, ok := logical.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", logical)
	}
	if outer.Op != parser.COUNT {
		t.Fatalf("expected outer aggregation op count, got %v", outer.Op)
	}
	if _, ok := outer.Child.(*logicalAggregationPlan); !ok {
		t.Fatalf("expected nested aggregation child, got %T", outer.Child)
	}
}

func TestBuildLogicalPlanCreatesLastOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("last_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical last_over_time plan, got error: %v", err)
	}
	rangeFn, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangeFn.Func != "last_over_time" {
		t.Fatalf("expected last_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildLogicalPlanCreatesSumOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical sum_over_time plan, got error: %v", err)
	}
	rangeFn, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangeFn.Func != "sum_over_time" {
		t.Fatalf("expected sum_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildLogicalPlanCreatesAvgOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("avg_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical avg_over_time plan, got error: %v", err)
	}
	rangeFn, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangeFn.Func != "avg_over_time" {
		t.Fatalf("expected avg_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildLogicalPlanCreatesMaxOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("max_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical max_over_time plan, got error: %v", err)
	}
	rangeFn, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangeFn.Func != "max_over_time" {
		t.Fatalf("expected max_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildLogicalPlanCreatesMinOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("min_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical min_over_time plan, got error: %v", err)
	}
	rangeFn, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangeFn.Func != "min_over_time" {
		t.Fatalf("expected min_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildLogicalPlanCreatesCountOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("count_over_time(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical count_over_time plan, got error: %v", err)
	}
	rangeFn, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected logicalRangeFunctionPlan, got %T", logical)
	}
	if rangeFn.Func != "count_over_time" {
		t.Fatalf("expected count_over_time function, got %#v", rangeFn.Func)
	}
}

func TestBuildLogicalPlanCreatesQuantileOverTimePlan(t *testing.T) {
	expr, err := plan.ParseExpression("quantile_over_time(0.95, up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical quantile_over_time plan, got error: %v", err)
	}
	quantileFn, ok := logical.(*logicalQuantileOverTimePlan)
	if !ok {
		t.Fatalf("expected logicalQuantileOverTimePlan, got %T", logical)
	}
	if quantileFn.Quantile != 0.95 {
		t.Fatalf("expected quantile 0.95, got %#v", quantileFn.Quantile)
	}
}

func TestBuildLogicalPlanCreatesAbsentPlanWithDerivedLabels(t *testing.T) {
	expr, err := plan.ParseExpression(`absent(nonexistent{job="api",instance=~".*"})`)
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical absent plan, got error: %v", err)
	}
	absentPlan, ok := logical.(*logicalAbsentPlan)
	if !ok {
		t.Fatalf("expected logicalAbsentPlan, got %T", logical)
	}
	if len(absentPlan.OutputMetric) != 1 || absentPlan.OutputMetric["job"] != "api" {
		t.Fatalf("expected derived absent labels {job=api}, got %#v", absentPlan.OutputMetric)
	}
}

func TestBuildLogicalPlanCreatesAbsentOverTimePlanWithEmptyDerivedLabelsForComplexExpr(t *testing.T) {
	expr, err := plan.ParseExpression(`absent_over_time(sum(nonexistent{job="api"})[5m:1m])`)
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical absent_over_time plan, got error: %v", err)
	}
	absentPlan, ok := logical.(*logicalAbsentOverTimePlan)
	if !ok {
		t.Fatalf("expected logicalAbsentOverTimePlan, got %T", logical)
	}
	if len(absentPlan.OutputMetric) != 0 {
		t.Fatalf("expected empty derived labels for complex absent_over_time input, got %#v", absentPlan.OutputMetric)
	}
}

func TestBuildLogicalPlanCreatesNestedSubqueryRangeFunctionPlan(t *testing.T) {
	expr, err := plan.ParseExpression("last_over_time(last_over_time((up * 100)[5m:30s])[10m:1m])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected nested logical range-function/subquery plan, got error: %v", err)
	}
	outer, ok := logical.(*logicalRangeFunctionPlan)
	if !ok {
		t.Fatalf("expected outer logicalRangeFunctionPlan, got %T", logical)
	}
	if _, ok := outer.Child.(*logicalSubqueryPlan); !ok {
		t.Fatalf("expected outer child to be logicalSubqueryPlan, got %T", outer.Child)
	}
}

func TestBuildLogicalPlanCreatesNestedMatrixFunctionBinaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum_over_time((up * 100)[5m:30s]) + count_over_time((up * 100)[5m:30s])")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected nested matrix binary plan, got error: %v", err)
	}
	if _, ok := logical.(*logicalBinaryPlan); !ok {
		t.Fatalf("expected logicalBinaryPlan, got %T", logical)
	}
}

func TestBuildLogicalPlanCreatesSetOperatorPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up and on(job) up")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical set-operator plan, got error: %v", err)
	}
	binaryPlan, ok := logical.(*logicalBinaryPlan)
	if !ok {
		t.Fatalf("expected logicalBinaryPlan, got %T", logical)
	}
	if binaryPlan.Op != parser.LAND {
		t.Fatalf("expected LAND operator, got %v", binaryPlan.Op)
	}
	if binaryPlan.VectorMatching == nil || binaryPlan.VectorMatching.Card != parser.CardManyToMany {
		t.Fatalf("expected many-to-many vector matching, got %#v", binaryPlan.VectorMatching)
	}
}

func TestBuildLogicalPlanCreatesSubqueryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("(up * 100)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical subquery plan, got error: %v", err)
	}
	subquery, ok := logical.(*logicalSubqueryPlan)
	if !ok {
		t.Fatalf("expected logicalSubqueryPlan, got %T", logical)
	}
	if subquery.ExprString() != "(up * 100)[5m:30s]" {
		t.Fatalf("expected subquery expression to be preserved, got %q", subquery.ExprString())
	}
	if subquery.Child == nil {
		t.Fatal("expected subquery child plan")
	}
}

func TestBuildLogicalPlanCreatesSubqueryWithLocalAggregationChildPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum(up)[5m:30s]")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical subquery plan, got error: %v", err)
	}
	subquery, ok := logical.(*logicalSubqueryPlan)
	if !ok {
		t.Fatalf("expected logicalSubqueryPlan, got %T", logical)
	}
	if _, ok := subquery.Child.(*logicalAggregationPlan); !ok {
		t.Fatalf("expected local aggregation child inside subquery, got %T", subquery.Child)
	}
}

func TestBuildLogicalPlanCreatesVectorMatchingBinaryPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up * on(job) group_left sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	logical, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical vector matching binary plan, got error: %v", err)
	}
	binaryPlan, ok := logical.(*logicalBinaryPlan)
	if !ok {
		t.Fatalf("expected logicalBinaryPlan, got %T", logical)
	}
	if binaryPlan.VectorMatching == nil {
		t.Fatalf("expected vector matching metadata, got %#v", binaryPlan)
	}
	if binaryPlan.VectorMatching.Card != parser.CardManyToOne {
		t.Fatalf("expected many-to-one card, got %#v", binaryPlan.VectorMatching)
	}
	if !binaryPlan.VectorMatching.On || !sameStrings(binaryPlan.VectorMatching.MatchingLabels, []string{"job"}) {
		t.Fatalf("unexpected vector matching labels: %#v", binaryPlan.VectorMatching)
	}
}

func TestBuildLogicalPlanCreatesLabelReplacePlan(t *testing.T) {
	expr, err := plan.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := BuildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical label_replace plan, got error: %v", err)
	}
	labelPlan, ok := plan.(*logicalLabelReplacePlan)
	if !ok {
		t.Fatalf("expected logicalLabelReplacePlan, got %T", plan)
	}
	if labelPlan.Config.Dst != "job_copy" || labelPlan.Config.Src != "job" {
		t.Fatalf("unexpected label_replace config: %#v", labelPlan.Config)
	}
	if _, ok := labelPlan.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected logical leaf child, got %T", labelPlan.Child)
	}
}

func TestBuildExecPlanLowersLogicalAggregationPlan(t *testing.T) {
	logical := &logicalAggregationPlan{
		Expr:     mustParseExpr(t, "sum by (job) (up)"),
		Op:       parser.SUM,
		Grouping: []string{"job"},
		Child:    &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
	}

	plan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution aggregation plan, got error: %v", err)
	}
	agg, ok := plan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", plan)
	}
	if agg.Op != parser.SUM {
		t.Fatalf("expected sum op, got %v", agg.Op)
	}
	if agg.ParamNumber != nil || agg.ParamString != "" {
		t.Fatalf("did not expect aggregation parameter, got %#v", agg)
	}
	if _, ok := agg.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated child, got %T", agg.Child)
	}
}

func TestBuildExecPlanLowersLogicalTopKPlan(t *testing.T) {
	k := 3.0
	logical := &logicalAggregationPlan{
		Expr:        mustParseExpr(t, "topk(3, up)"),
		Op:          parser.TOPK,
		ParamNumber: &k,
		Child:       &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
	}

	execPlan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution topk plan, got error: %v", err)
	}
	agg, ok := execPlan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", execPlan)
	}
	if agg.ParamNumber == nil || *agg.ParamNumber != 3 {
		t.Fatalf("expected topk parameter 3, got %#v", agg.ParamNumber)
	}
}

func TestBuildExecPlanLowersLogicalHistogramQuantilePlan(t *testing.T) {
	rateExpr := mustParseExpr(t, "rate(http_request_duration_seconds_bucket[5m])")
	rateCall, ok := rateExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected rate call expr, got %T", rateExpr)
	}
	logical := &logicalHistogramQuantilePlan{
		Expr:     mustParseExpr(t, "histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))"),
		Quantile: 0.9,
		Child: &logicalAggregationPlan{
			Expr:     mustParseExpr(t, "sum by (le, job) (rate(http_request_duration_seconds_bucket[5m]))"),
			Op:       parser.SUM,
			Grouping: []string{"le", "job"},
			Child:    &logicalRatePlan{Expr: rateCall, Func: "rate", Child: &logicalLeafExprPlan{Expr: rateCall.Args[0]}},
		},
	}

	execPlan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution histogram_quantile plan, got error: %v", err)
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

func TestBuildExecPlanLowersLogicalVectorMatchingBinaryPlan(t *testing.T) {
	logical := &logicalBinaryPlan{
		Expr: mustParseExpr(t, "up * on(job) group_left sum by (job) (up)"),
		Op:   parser.MUL,
		VectorMatching: &parser.VectorMatching{
			Card:           parser.CardManyToOne,
			On:             true,
			MatchingLabels: []string{"job"},
		},
		LHS: &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
		RHS: &logicalAggregationPlan{
			Expr:     mustParseExpr(t, "sum by (job) (up)"),
			Op:       parser.SUM,
			Grouping: []string{"job"},
			Child:    &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
		},
	}

	execPlan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution vector matching binary plan, got error: %v", err)
	}
	binaryPlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if binaryPlan.Fragment == nil || binaryPlan.Fragment.BinaryJoin == nil || binaryPlan.Fragment.BinaryJoin.VectorMatching == nil || binaryPlan.Fragment.BinaryJoin.VectorMatching.Card != parser.CardManyToOne {
		t.Fatalf("expected many-to-one vector matching in native execution plan, got %#v", binaryPlan)
	}
}

func TestBuildExecPlanLowersLogicalHistogramProjectionPlan(t *testing.T) {
	for _, fn := range []string{"histogram_count", "histogram_sum", "histogram_avg"} {
		t.Run(fn, func(t *testing.T) {
			rateExpr := mustParseExpr(t, "rate(http_request_duration_seconds_bucket[5m])")
			rateCall, ok := rateExpr.(*parser.Call)
			if !ok {
				t.Fatalf("expected rate call expr, got %T", rateExpr)
			}
			logical := &logicalHistogramProjectionPlan{
				Expr: mustParseExpr(t, fn+"(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))"),
				Func: fn,
				Child: &logicalAggregationPlan{
					Expr:     mustParseExpr(t, "sum by (le, job) (rate(http_request_duration_seconds_bucket[5m]))"),
					Op:       parser.SUM,
					Grouping: []string{"le", "job"},
					Child:    &logicalRatePlan{Expr: rateCall, Func: "rate", Child: &logicalLeafExprPlan{Expr: rateCall.Args[0]}},
				},
			}

			execPlan, err := buildExecPlan(logical)
			if err != nil {
				t.Fatalf("expected execution histogram projection plan, got error: %v", err)
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

func TestBuildExecPlanLowersDirectLogicalHistogramProjectionPlanToNative(t *testing.T) {
	for _, fn := range []string{"histogram_count", "histogram_sum", "histogram_avg"} {
		t.Run(fn, func(t *testing.T) {
			logical := &logicalHistogramProjectionPlan{
				Expr:  mustParseExpr(t, fn+`(http_request_duration_seconds_bucket{job="api"})`),
				Func:  fn,
				Child: &logicalLeafExprPlan{Expr: mustParseExpr(t, `http_request_duration_seconds_bucket{job="api"}`)},
			}

			execPlan, err := buildExecPlan(logical)
			if err != nil {
				t.Fatalf("expected native direct histogram projection plan, got error: %v", err)
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

func TestBuildExecPlanLowersLogicalHistogramFractionPlan(t *testing.T) {
	rateExpr := mustParseExpr(t, "rate(http_request_duration_seconds_bucket[5m])")
	rateCall, ok := rateExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected rate call expr, got %T", rateExpr)
	}
	logical := &logicalHistogramFractionPlan{
		Expr:  mustParseExpr(t, "histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))"),
		Lower: 0,
		Upper: 1,
		Child: &logicalAggregationPlan{
			Expr:     mustParseExpr(t, "sum by (le, job) (rate(http_request_duration_seconds_bucket[5m]))"),
			Op:       parser.SUM,
			Grouping: []string{"le", "job"},
			Child:    &logicalRatePlan{Expr: rateCall, Func: "rate", Child: &logicalLeafExprPlan{Expr: rateCall.Args[0]}},
		},
	}

	execPlan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution histogram fraction plan, got error: %v", err)
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

func TestBuildExecPlanLowersLogicalAbsentPlan(t *testing.T) {
	logical := &logicalAbsentPlan{
		Expr:         mustParseExpr(t, `absent(nonexistent{job="api"})`),
		OutputMetric: map[string]string{"job": "api"},
		Child:        &logicalLeafExprPlan{Expr: mustParseExpr(t, `nonexistent{job="api"}`)},
	}

	execPlan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution absent plan, got error: %v", err)
	}
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "absent" {
		t.Fatalf("unexpected absent native plan: %#v", nativePlan)
	}
}

func TestBuildExecPlanLowersLogicalAbsentOverTimePlan(t *testing.T) {
	logical := &logicalAbsentOverTimePlan{
		Expr:         mustParseExpr(t, `absent_over_time(nonexistent{job="api"}[5m])`),
		OutputMetric: map[string]string{"job": "api"},
		Child:        &logicalLeafExprPlan{Expr: mustParseExpr(t, `nonexistent{job="api"}[5m]`)},
	}

	execPlan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution absent_over_time plan, got error: %v", err)
	}
	nativePlan, ok := execPlan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", execPlan)
	}
	if nativePlan.Kind != "absent_over_time" {
		t.Fatalf("unexpected absent_over_time native plan: %#v", nativePlan)
	}
}

func TestBuildExecPlanLowersLogicalLabelJoinPlan(t *testing.T) {
	cfg, err := model.BuildLabelJoinConfig("joined", "/", []string{"job", "namespace"})
	if err != nil {
		t.Fatal(err)
	}
	logical := &logicalLabelJoinPlan{
		Expr:   mustParseExpr(t, `label_join(up, "joined", "/", "job", "namespace")`),
		Config: cfg,
		Child:  &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
	}

	plan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution label_join plan, got error: %v", err)
	}
	joinPlan, ok := plan.(*localLabelJoinPlan)
	if !ok {
		t.Fatalf("expected localLabelJoinPlan, got %T", plan)
	}
	if joinPlan.Config.Dst != "joined" {
		t.Fatalf("unexpected label_join config: %#v", joinPlan.Config)
	}
	if _, ok := joinPlan.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated child, got %T", joinPlan.Child)
	}
}

func mustParseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := plan.ParseExpression(query)
	if err != nil {
		t.Fatalf("plan.ParseExpression(%q): %v", query, err)
	}
	return expr
}

func floatPtr(v float64) *float64 {
	return &v
}
