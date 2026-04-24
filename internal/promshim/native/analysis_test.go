package native

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestAnalyzeAggregationTracksNativeSourceExpression(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum by (job) (up * 100)`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	binaryExpr, ok := agg.Expr.(*parser.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary child expr, got %T", agg.Expr)
	}
	scalarExpr, ok := binaryExpr.RHS.(*parser.NumberLiteral)
	if !ok {
		t.Fatalf("expected scalar rhs, got %T", binaryExpr.RHS)
	}

	child := &logicalpkg.BinaryPlan{
		Expr: binaryExpr,
		Op:   binaryExpr.Op,
		LHS:  &logicalpkg.LeafExprPlan{Expr: binaryExpr.LHS},
		RHS:  &logicalpkg.ScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
	}
	logical := &logicalpkg.AggregationPlan{
		Expr:     agg,
		Op:       agg.Op,
		Grouping: append([]string(nil), agg.Grouping...),
		Child:    child,
	}

	analysis := Analyze(logical)
	info := analysis.InfoFor(logical)
	if info == nil || info.Aggregation == nil {
		t.Fatalf("expected aggregation analysis info, got %#v", info)
	}
	if !info.Aggregation.Eligible {
		t.Fatalf("expected aggregation pushdown eligibility, got %#v", info.Aggregation)
	}
	if info.Aggregation.SourceView == nil {
		t.Fatalf("expected aggregation source view, got %#v", info.Aggregation)
	}
	if !strings.Contains(info.Aggregation.SourceView.ValueExpr, "100") || !strings.Contains(info.Aggregation.SourceView.ValueExpr, "*") {
		t.Fatalf("expected transformed native source value expression, got %#v", info.Aggregation.SourceView)
	}
	if info.LabelLineage.MetricName != LabelLineageDropped {
		t.Fatalf("expected aggregation to drop metric name, got %#v", info.LabelLineage)
	}
}

func TestAnalyzeTier1AdditionalAggregationsMarkNativeLowerable(t *testing.T) {
	cases := []struct {
		query string
		op    parser.ItemType
	}{
		{query: `stddev by (job) (up)`, op: parser.STDDEV},
		{query: `stdvar by (job) (up)`, op: parser.STDVAR},
		{query: `group by (job) (up)`, op: parser.GROUP},
		{query: `quantile by (job) (0.9, up)`, op: parser.QUANTILE},
		{query: `topk by (job) (3, up)`, op: parser.TOPK},
		{query: `limitk by (job) (2, up)`, op: parser.LIMITK},
		{query: `limit_ratio by (job) (0.5, up)`, op: parser.LIMIT_RATIO},
		{query: `count_values("sample_value", up)`, op: parser.COUNT_VALUES},
	}
	for _, tc := range cases {
		aggExpr := mustParseExpr(t, tc.query)
		agg, ok := aggExpr.(*parser.AggregateExpr)
		if !ok {
			t.Fatalf("expected aggregate expr for %q, got %T", tc.query, aggExpr)
		}
		logical := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Grouping: append([]string(nil), agg.Grouping...), Without: agg.Without, Child: &logicalpkg.LeafExprPlan{Expr: agg.Expr}}
		if agg.Param != nil {
			switch param := agg.Param.(type) {
			case *parser.NumberLiteral:
				logical.ParamNumber = &param.Val
			case *parser.StringLiteral:
				logical.ParamString = param.Val
			default:
				t.Fatalf("expected supported aggregation parameter for %q, got %T", tc.query, agg.Param)
			}
		}
		info := Analyze(logical).InfoFor(logical)
		if info == nil || !info.NativeLowerable {
			t.Fatalf("expected %s aggregation to be native-lowerable, got %#v", tc.query, info)
		}
		if info.SubtreeShape != SubtreeShapeAggregation {
			t.Fatalf("expected SubtreeShapeAggregation for %q, got %#v", tc.query, info)
		}
		if (tc.op == parser.TOPK || tc.op == parser.LIMITK || tc.op == parser.LIMIT_RATIO) && info.LabelLineage.MetricName != LabelLineageOriginal {
			t.Fatalf("expected selection aggregation to preserve metric-name lineage, got %#v", info.LabelLineage)
		}
		if tc.op == parser.COUNT_VALUES {
			if info.LabelLineage.MetricName != LabelLineageDropped || info.LabelLineage.Known["sample_value"] != LabelLineageSynthetic {
				t.Fatalf("expected count_values synthetic label lineage, got %#v", info.LabelLineage)
			}
		}
	}
}

func TestAnalyzePointwiseFunctionMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `abs(up)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.PointwiseFunctionPlan{Expr: call, Func: "abs", Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected abs() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeUnarySourceExpr {
		t.Fatalf("expected SubtreeShapeUnarySourceExpr, got %#v", info)
	}
	if info.SourceExpr == nil || !strings.Contains(info.SourceExpr.ValueExpr, "abs") {
		t.Fatalf("expected abs source expression view, got %#v", info.SourceExpr)
	}
}

func TestAnalyzeInfoMarksNativeLowerableForDefaultTargetInfo(t *testing.T) {
	callExpr := mustParseExpr(t, `info(up)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.InfoPlan{Expr: call, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected info() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeInfoJoin {
		t.Fatalf("expected SubtreeShapeInfoJoin, got %#v", info)
	}
}

func TestAnalyzeInfoMarksRegexMetricNameMatcherNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `info(up, {__name__=~".+_info"})`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	selector := call.Args[1].(*parser.VectorSelector)
	logical := &logicalpkg.InfoPlan{Expr: call, SelectorMatchers: CloneMatchers(selector.LabelMatchers), Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected regex info() metric-name matcher to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeInfoJoin {
		t.Fatalf("expected SubtreeShapeInfoJoin, got %#v", info)
	}
}

func TestAnalyzeVectorMarksNativeLowerableForScalarLiteral(t *testing.T) {
	callExpr := mustParseExpr(t, `vector(1)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.VectorPlan{Expr: call, Child: &logicalpkg.ScalarLiteralPlan{Expr: call.Args[0], Value: 1}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected vector() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeSyntheticSeries || info.SyntheticSeries == nil || info.SyntheticSeries.Value != 1 {
		t.Fatalf("expected lifted synthetic literal vector view, got %#v", info)
	}
}

func TestAnalyzeSortByLabelMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `sort_by_label(up, "instance", "job")`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.SortPlan{Expr: call, Func: "sort_by_label", Labels: []string{"instance", "job"}, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected sort_by_label to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeSortTransform {
		t.Fatalf("expected SubtreeShapeSortTransform, got %#v", info)
	}
}

func TestAnalyzeClampMinMarksNativeLowerableForScalarParameterChild(t *testing.T) {
	callExpr := mustParseExpr(t, `clamp_min(up, scalar(sum(up)))`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	aggExpr := call.Args[1].(*parser.Call).Args[0].(*parser.AggregateExpr)
	logical := &logicalpkg.PointwiseFunctionPlan{
		Expr:  call,
		Func:  "clamp_min",
		Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]},
		ParamChildren: []logicalpkg.Node{
			&logicalpkg.ScalarConvertPlan{Expr: call.Args[1], Child: &logicalpkg.AggregationPlan{Expr: aggExpr, Op: aggExpr.Op, Grouping: append([]string(nil), aggExpr.Grouping...), Without: aggExpr.Without, Child: &logicalpkg.LeafExprPlan{Expr: aggExpr.Expr}}},
		},
		ParamNumbers: []*float64{nil},
	}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected clamp_min to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeClampTransform {
		t.Fatalf("expected SubtreeShapeClampTransform, got %#v", info)
	}
	if info.LabelLineage.MetricName != LabelLineageDropped {
		t.Fatalf("expected clamp_min to drop metric name, got %#v", info.LabelLineage)
	}
}

func TestAnalyzeScalarConvertMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `scalar(up)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.ScalarConvertPlan{Expr: call, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected scalar() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeScalarConvert {
		t.Fatalf("expected SubtreeShapeScalarConvert, got %#v", info)
	}
}

func TestAnalyzeSyntheticDateFunctionMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `minute()`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.PointwiseFunctionPlan{Expr: call, Func: "minute"}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected minute() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeSyntheticSeries || info.SyntheticSeries == nil || info.SyntheticSeries.Func != "minute" {
		t.Fatalf("expected synthetic minute() view, got %#v", info)
	}
}

func TestAnalyzeScalarBuiltinMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `time()`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.ScalarBuiltinPlan{Expr: call, Func: "time"}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected time() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeSyntheticSeries || info.SyntheticSeries == nil || info.SyntheticSeries.Func != "time" {
		t.Fatalf("expected synthetic time() view, got %#v", info)
	}
}

func TestAnalyzeHistogramProjectionMarksNativeLowerable(t *testing.T) {
	for _, fn := range []string{"histogram_count", "histogram_sum", "histogram_avg", "histogram_stddev", "histogram_stdvar"} {
		t.Run(fn, func(t *testing.T) {
			callExpr := mustParseExpr(t, fn+`(http_request_duration_seconds_bucket{job="api"})`)
			call, ok := callExpr.(*parser.Call)
			if !ok {
				t.Fatalf("expected call expr, got %T", callExpr)
			}
			logical := &logicalpkg.HistogramProjectionPlan{Expr: call, Func: fn, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
			info := Analyze(logical).InfoFor(logical)
			if info == nil || !info.NativeLowerable {
				t.Fatalf("expected %s to be native-lowerable, got %#v", fn, info)
			}
			if info.SubtreeShape != SubtreeShapeHistogramProjection {
				t.Fatalf("expected histogram projection shape for %s, got %#v", fn, info)
			}
		})
	}
}

func TestAnalyzeHistogramQuantileMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	agg := call.Args[1].(*parser.AggregateExpr)
	rateCall := agg.Expr.(*parser.Call)
	logical := &logicalpkg.HistogramQuantilePlan{Expr: call, Quantile: 0.9, Child: &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Grouping: append([]string(nil), agg.Grouping...), Without: agg.Without, Child: &logicalpkg.RatePlan{Expr: rateCall, Func: "rate", Child: &logicalpkg.LeafExprPlan{Expr: rateCall.Args[0]}}}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected histogram_quantile to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeHistogramFunction || info.HistogramFunc != "histogram_quantile" {
		t.Fatalf("expected histogram_quantile shape, got %#v", info)
	}
}

func TestAnalyzeHistogramQuantilesMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `histogram_quantiles(sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])), "quantile", 0.5, scalar(sum(up)))`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	histAgg := call.Args[0].(*parser.AggregateExpr)
	histRate := histAgg.Expr.(*parser.Call)
	scalarCall := call.Args[3].(*parser.Call)
	scalarAgg := scalarCall.Args[0].(*parser.AggregateExpr)
	literal := 0.5
	logical := &logicalpkg.HistogramQuantilesPlan{
		Expr:         call,
		Label:        "quantile",
		ParamNumbers: []*float64{&literal, nil},
		ParamChildren: []logicalpkg.Node{
			&logicalpkg.ScalarLiteralPlan{Expr: call.Args[2], Value: literal},
			&logicalpkg.ScalarConvertPlan{Expr: call.Args[3], Child: &logicalpkg.AggregationPlan{Expr: scalarAgg, Op: scalarAgg.Op, Grouping: append([]string(nil), scalarAgg.Grouping...), Without: scalarAgg.Without, Child: &logicalpkg.LeafExprPlan{Expr: scalarAgg.Expr}}},
		},
		Child: &logicalpkg.AggregationPlan{Expr: histAgg, Op: histAgg.Op, Grouping: append([]string(nil), histAgg.Grouping...), Without: histAgg.Without, Child: &logicalpkg.RatePlan{Expr: histRate, Func: "rate", Child: &logicalpkg.LeafExprPlan{Expr: histRate.Args[0]}}},
	}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected histogram_quantiles to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeHistogramFunction || info.HistogramFunc != "histogram_quantiles" {
		t.Fatalf("expected histogram_quantiles shape, got %#v", info)
	}
}

func TestAnalyzeHistogramFractionMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `histogram_fraction(0, 1, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	agg := call.Args[2].(*parser.AggregateExpr)
	rateCall := agg.Expr.(*parser.Call)
	logical := &logicalpkg.HistogramFractionPlan{Expr: call, Lower: 0, Upper: 1, Child: &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Grouping: append([]string(nil), agg.Grouping...), Without: agg.Without, Child: &logicalpkg.RatePlan{Expr: rateCall, Func: "rate", Child: &logicalpkg.LeafExprPlan{Expr: rateCall.Args[0]}}}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected histogram_fraction to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeHistogramFunction || info.HistogramFunc != "histogram_fraction" {
		t.Fatalf("expected histogram_fraction shape, got %#v", info)
	}
}

func TestAnalyzeAbsentMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `absent(up{job="api"})`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.AbsentPlan{Expr: call, OutputMetric: map[string]string{"job": "api"}, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected absent() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeAbsent || info.AbsentFunc != "absent" {
		t.Fatalf("expected absent shape, got %#v", info)
	}
}

func TestAnalyzeAbsentOverTimeMarksNativeLowerableForRangeSelectorChild(t *testing.T) {
	callExpr := mustParseExpr(t, `absent_over_time(up{job="api"}[5m])`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.AbsentOverTimePlan{Expr: call, OutputMetric: map[string]string{"job": "api"}, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected absent_over_time() to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeAbsent || info.AbsentFunc != "absent_over_time" {
		t.Fatalf("expected absent_over_time shape, got %#v", info)
	}
}

func TestAnalyzeLabelJoinMarksSyntheticDestination(t *testing.T) {
	callExpr := mustParseExpr(t, `label_join(up, "joined", "/", "job", "namespace")`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	cfg, err := model.BuildLabelJoinConfig("joined", "/", []string{"job", "namespace"})
	if err != nil {
		t.Fatal(err)
	}
	logical := &logicalpkg.LabelJoinPlan{
		Expr:   call,
		Config: cfg,
		Child:  &logicalpkg.LeafExprPlan{Expr: call.Args[0]},
	}

	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.NativeLowerable || info.SubtreeShape != SubtreeShapeLabelTransform {
		t.Fatalf("expected native label_join transform, got %#v", info)
	}
	if got := info.LabelLineage.Known["joined"]; got != LabelLineageSynthetic {
		t.Fatalf("expected synthetic destination lineage, got %#v", info.LabelLineage)
	}
}

func TestAnalyzeLabelReplaceCanRestoreMetricNameLineage(t *testing.T) {
	callExpr := mustParseExpr(t, `label_replace(rate(up[5m]), "__name__", "rate_$1", "__name__", "(.+)")`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	cfg, err := model.BuildLabelReplaceConfig("__name__", "rate_$1", "__name__", "(.+)")
	if err != nil {
		t.Fatal(err)
	}
	logical := &logicalpkg.LabelReplacePlan{Expr: call, Config: cfg, Child: &logicalpkg.RatePlan{Expr: call.Args[0], Func: "rate", Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0].(*parser.Call).Args[0]}}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable || info.SubtreeShape != SubtreeShapeLabelTransform {
		t.Fatalf("expected native label_replace transform, got %#v", info)
	}
	if info.LabelLineage.MetricName != LabelLineageMutated {
		t.Fatalf("expected metric-name lineage to be mutated, got %#v", info.LabelLineage)
	}
}

func TestAnalyzeRateOverSubqueryMarksNativeLowerable(t *testing.T) {
	rateExpr := mustParseExpr(t, `rate(sum(up)[5m:1m])`)
	call, ok := rateExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", rateExpr)
	}
	subquery, ok := call.Args[0].(*parser.SubqueryExpr)
	if !ok {
		t.Fatalf("expected subquery arg, got %T", call.Args[0])
	}
	aggExpr, ok := subquery.Expr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate child, got %T", subquery.Expr)
	}
	logical := &logicalpkg.RatePlan{
		Expr: call,
		Func: "rate",
		Child: &logicalpkg.SubqueryPlan{
			Expr:   subquery,
			Range:  subquery.Range,
			Step:   subquery.Step,
			Offset: subquery.OriginalOffset,
			Child: &logicalpkg.AggregationPlan{
				Expr:     aggExpr,
				Op:       aggExpr.Op,
				Grouping: append([]string(nil), aggExpr.Grouping...),
				Child:    &logicalpkg.LeafExprPlan{Expr: aggExpr.Expr},
			},
		},
	}

	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.NativeLowerable {
		t.Fatalf("expected rate-over-subquery to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != "rate" {
		t.Fatalf("expected native rate range-function shape, got %#v", info)
	}
	if info.LabelLineage.MetricName != LabelLineageDropped {
		t.Fatalf("expected rate to drop metric name, got %#v", info.LabelLineage)
	}
}

func TestAnalyzeIncreaseAndDeltaOverSubqueryMarkNativeLowerable(t *testing.T) {
	cases := []struct {
		query string
		kind  string
	}{
		{query: `increase(sum(up)[5m:1m])`, kind: "increase"},
		{query: `delta(sum(up)[5m:1m])`, kind: "delta"},
		{query: `idelta(sum(up)[5m:1m])`, kind: "idelta"},
	}
	for _, tc := range cases {
		expr := mustParseExpr(t, tc.query)
		call, ok := expr.(*parser.Call)
		if !ok {
			t.Fatalf("expected call expr for %q, got %T", tc.query, expr)
		}
		subquery, ok := call.Args[0].(*parser.SubqueryExpr)
		if !ok {
			t.Fatalf("expected subquery arg for %q, got %T", tc.query, call.Args[0])
		}
		aggExpr, ok := subquery.Expr.(*parser.AggregateExpr)
		if !ok {
			t.Fatalf("expected aggregate child for %q, got %T", tc.query, subquery.Expr)
		}
		child := &logicalpkg.SubqueryPlan{
			Expr:   subquery,
			Range:  subquery.Range,
			Step:   subquery.Step,
			Offset: subquery.OriginalOffset,
			Child: &logicalpkg.AggregationPlan{
				Expr:     aggExpr,
				Op:       aggExpr.Op,
				Grouping: append([]string(nil), aggExpr.Grouping...),
				Child:    &logicalpkg.LeafExprPlan{Expr: aggExpr.Expr},
			},
		}
		var logical logicalpkg.Node
		switch tc.kind {
		case "increase":
			logical = &logicalpkg.IncreasePlan{Expr: call, Child: child}
		default:
			logical = &logicalpkg.DeltaPlan{Expr: call, Func: tc.kind, Child: child}
		}
		info := Analyze(logical).InfoFor(logical)
		if info == nil || !info.NativeLowerable {
			t.Fatalf("expected %s to be native-lowerable, got %#v", tc.kind, info)
		}
		if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != tc.kind {
			t.Fatalf("expected native %s range-function shape, got %#v", tc.kind, info)
		}
		if info.LabelLineage.MetricName != LabelLineageDropped {
			t.Fatalf("expected %s to drop metric name, got %#v", tc.kind, info.LabelLineage)
		}
	}
}

func TestAnalyzeChangesAndDerivOverSubqueryMarkNativeLowerable(t *testing.T) {
	cases := []struct {
		query string
		kind  string
	}{
		{query: `changes(sum(up)[5m:1m])`, kind: "changes"},
		{query: `deriv(sum(up)[5m:1m])`, kind: "deriv"},
	}
	for _, tc := range cases {
		expr := mustParseExpr(t, tc.query)
		call, ok := expr.(*parser.Call)
		if !ok {
			t.Fatalf("expected call expr for %q, got %T", tc.query, expr)
		}
		subquery, ok := call.Args[0].(*parser.SubqueryExpr)
		if !ok {
			t.Fatalf("expected subquery arg for %q, got %T", tc.query, call.Args[0])
		}
		aggExpr, ok := subquery.Expr.(*parser.AggregateExpr)
		if !ok {
			t.Fatalf("expected aggregate child for %q, got %T", tc.query, subquery.Expr)
		}
		child := &logicalpkg.SubqueryPlan{
			Expr:   subquery,
			Range:  subquery.Range,
			Step:   subquery.Step,
			Offset: subquery.OriginalOffset,
			Child: &logicalpkg.AggregationPlan{
				Expr:     aggExpr,
				Op:       aggExpr.Op,
				Grouping: append([]string(nil), aggExpr.Grouping...),
				Child:    &logicalpkg.LeafExprPlan{Expr: aggExpr.Expr},
			},
		}
		var logical logicalpkg.Node
		switch tc.kind {
		case "changes":
			logical = &logicalpkg.ChangesPlan{Expr: call, Child: child}
		default:
			logical = &logicalpkg.DerivPlan{Expr: call, Child: child}
		}
		info := Analyze(logical).InfoFor(logical)
		if info == nil || !info.NativeLowerable {
			t.Fatalf("expected %s to be native-lowerable, got %#v", tc.kind, info)
		}
		if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != tc.kind {
			t.Fatalf("expected native %s range-function shape, got %#v", tc.kind, info)
		}
		if info.LabelLineage.MetricName != LabelLineageDropped {
			t.Fatalf("expected %s to drop metric name, got %#v", tc.kind, info.LabelLineage)
		}
	}
}

func TestAnalyzeTier1AdditionalRangeFunctionsMarkNativeLowerable(t *testing.T) {
	for _, fn := range []string{"first_over_time", "stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "resets", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time"} {
		callExpr := mustParseExpr(t, fn+`(up[5m])`)
		call, ok := callExpr.(*parser.Call)
		if !ok {
			t.Fatalf("expected call expr for %q, got %T", fn, callExpr)
		}
		logical := &logicalpkg.RangeFunctionPlan{Expr: call, Func: fn, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
		info := Analyze(logical).InfoFor(logical)
		if info == nil || !info.NativeLowerable {
			t.Fatalf("expected %s to be native-lowerable, got %#v", fn, info)
		}
		if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != fn {
			t.Fatalf("expected native %s range-function shape, got %#v", fn, info)
		}
	}
}

func TestAnalyzeQuantileOverTimeMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `quantile_over_time(0.95, up[5m])`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.QuantileOverTimePlan{Expr: call, Quantile: 0.95, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[1]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected quantile_over_time to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != "quantile_over_time" {
		t.Fatalf("expected native quantile_over_time range-function shape, got %#v", info)
	}
	if info.LabelLineage.MetricName != LabelLineageDropped {
		t.Fatalf("expected quantile_over_time to drop metric name, got %#v", info.LabelLineage)
	}
}

func TestAnalyzePredictLinearMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `predict_linear(up[5m], 60)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	duration := 60.0
	logical := &logicalpkg.RangeFunctionPlan{Expr: call, Func: "predict_linear", ParamNumber: &duration, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected predict_linear to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != "predict_linear" {
		t.Fatalf("expected native predict_linear range-function shape, got %#v", info)
	}
}

func TestAnalyzeDoubleExponentialSmoothingMarksNativeLowerable(t *testing.T) {
	callExpr := mustParseExpr(t, `double_exponential_smoothing(up[5m], 0.5, 0.3)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	sf, tf := 0.5, 0.3
	logical := &logicalpkg.RangeFunctionPlan{Expr: call, Func: "double_exponential_smoothing", ParamNumbers: []*float64{&sf, &tf}, Child: &logicalpkg.LeafExprPlan{Expr: call.Args[0]}}
	info := Analyze(logical).InfoFor(logical)
	if info == nil || !info.NativeLowerable {
		t.Fatalf("expected double_exponential_smoothing to be native-lowerable, got %#v", info)
	}
	if info.SubtreeShape != SubtreeShapeRangeFunction || info.NodeType != "double_exponential_smoothing" {
		t.Fatalf("expected native smoothing range-function shape, got %#v", info)
	}
}

func TestAnalyzeSubqueryAccumulatesTimeRequirements(t *testing.T) {
	subqueryExpr := mustParseExpr(t, `(up * 100)[5m:1m] offset 1m`)
	subquery, ok := subqueryExpr.(*parser.SubqueryExpr)
	if !ok {
		t.Fatalf("expected subquery expr, got %T", subqueryExpr)
	}
	innerExpr := subquery.Expr
	if paren, ok := innerExpr.(*parser.ParenExpr); ok {
		innerExpr = paren.Expr
	}
	binaryExpr, ok := innerExpr.(*parser.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary subquery child, got %T", subquery.Expr)
	}
	scalarExpr, ok := binaryExpr.RHS.(*parser.NumberLiteral)
	if !ok {
		t.Fatalf("expected scalar rhs, got %T", binaryExpr.RHS)
	}

	logical := &logicalpkg.SubqueryPlan{
		Expr:   subquery,
		Range:  subquery.Range,
		Step:   subquery.Step,
		Offset: subquery.OriginalOffset,
		Child: &logicalpkg.BinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &logicalpkg.LeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &logicalpkg.ScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}

	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if want := 10 * time.Minute; info.TimeRequirements.Lookback != want {
		t.Fatalf("expected lookback %s, got %s", want, info.TimeRequirements.Lookback)
	}
	if want := 1 * time.Minute; info.TimeRequirements.Offset != want {
		t.Fatalf("expected offset %s, got %s", want, info.TimeRequirements.Offset)
	}
	if !info.TimeRequirements.NeedsSubqueryStepGrid {
		t.Fatalf("expected subquery step-grid flag, got %#v", info.TimeRequirements)
	}
}

func TestAnalyzeLeafTracksInstantSelectorLookbackAndOffset(t *testing.T) {
	expr := mustParseExpr(t, `up offset 90s`)
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}

	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if want := 5 * time.Minute; info.TimeRequirements.Lookback != want {
		t.Fatalf("expected 5m instant-selector lookback, got %#v", info.TimeRequirements)
	}
	if want := 90 * time.Second; info.TimeRequirements.Offset != want {
		t.Fatalf("expected 90s offset, got %#v", info.TimeRequirements)
	}
}

func TestAnalyzeSubqueryAccumulatesChildAndOwnOffsetsSeparatelyFromLookback(t *testing.T) {
	subqueryExpr := mustParseExpr(t, `(up offset 90s * 100)[5m:1m] offset 1m`)
	subquery, ok := subqueryExpr.(*parser.SubqueryExpr)
	if !ok {
		t.Fatalf("expected subquery expr, got %T", subqueryExpr)
	}
	innerExpr := subquery.Expr
	if paren, ok := innerExpr.(*parser.ParenExpr); ok {
		innerExpr = paren.Expr
	}
	binaryExpr, ok := innerExpr.(*parser.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary subquery child, got %T", subquery.Expr)
	}
	scalarExpr, ok := binaryExpr.RHS.(*parser.NumberLiteral)
	if !ok {
		t.Fatalf("expected scalar rhs, got %T", binaryExpr.RHS)
	}
	logical := &logicalpkg.SubqueryPlan{
		Expr:   subquery,
		Range:  subquery.Range,
		Step:   subquery.Step,
		Offset: subquery.OriginalOffset,
		Child: &logicalpkg.BinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &logicalpkg.LeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &logicalpkg.ScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}

	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if want := 10 * time.Minute; info.TimeRequirements.Lookback != want {
		t.Fatalf("expected 10m lookback (5m selector + 5m subquery range), got %#v", info.TimeRequirements)
	}
	if want := 150 * time.Second; info.TimeRequirements.Offset != want {
		t.Fatalf("expected accumulated 150s offset, got %#v", info.TimeRequirements)
	}
}

func TestBuildBinaryTemplateForScalarExprWrapsCompositeScalarExpressions(t *testing.T) {
	template, dropsMetric, ok := buildBinaryTemplateForScalarExpr(parser.DIV, "toFloat64(toUnixTimestamp64Milli({timestamp})) / 1000.0", "{value}", false)
	if !ok {
		t.Fatal("expected vector/synthetic-scalar division template")
	}
	if !dropsMetric {
		t.Fatal("expected arithmetic template to drop metric name")
	}
	want := "({value}) / (toFloat64(toUnixTimestamp64Milli({timestamp})) / 1000.0)"
	if template != want {
		t.Fatalf("unexpected template: got %q want %q", template, want)
	}
}

func TestBuildBinaryTemplateForScalarExprSupportsModulo(t *testing.T) {
	template, dropsMetric, ok := buildBinaryTemplateForScalarExpr(parser.MOD, "1.2345", "{value}", false)
	if !ok {
		t.Fatal("expected modulo template to be native-lowerable")
	}
	if !dropsMetric {
		t.Fatal("expected modulo template to drop metric name")
	}
	for _, want := range []string{"positiveModulo(", "abs(", "isNaN(", "isInfinite(", "({value})"} {
		if !strings.Contains(template, want) {
			t.Fatalf("expected modulo template to contain %q, got %q", want, template)
		}
	}
}

func TestApplyScalarValueTransformUsesRuntimeModuloCorrection(t *testing.T) {
	result, ok := applyScalarValueTransform(parser.MOD, OutputKindInstantVector, leafLabelLineage(), 1.2345, false)
	if !ok {
		t.Fatal("expected modulo scalar transform to be native-lowerable")
	}
	if got, want := result.View.ValueExpr, "{value}"; got != want {
		t.Fatalf("expected raw child value expression for runtime modulo correction, got %q", got)
	}
	if result.Runtime == nil {
		t.Fatal("expected runtime modulo correction metadata")
	}
	if got, want := result.Runtime.Op, RuntimeValueTransformPromQLModulo; got != want {
		t.Fatalf("expected modulo runtime transform op %q, got %q", want, got)
	}
}

func TestApplySyntheticScalarChildTransformSupportsTimeScalarRoots(t *testing.T) {
	result, ok := applySyntheticScalarChildTransform(parser.ADD, false, "time", OutputKindScalar, unknownLineage(), true)
	if !ok {
		t.Fatal("expected time()+time() root to be native-lowerable")
	}
	if result.OutputKind != OutputKindScalar {
		t.Fatalf("expected scalar value-transform result, got %#v", result)
	}
}

func TestApplySyntheticScalarChildTransformSupportsTimeVectorComparisons(t *testing.T) {
	result, ok := applySyntheticScalarChildTransform(parser.GTE, false, "time", OutputKindInstantVector, leafLabelLineage(), true)
	if !ok {
		t.Fatal("expected time() >= vector filter to be native-lowerable")
	}
	if result.View.FilterExpr == "" {
		t.Fatalf("expected comparison filter in result, got %#v", result)
	}
}

func TestApplyBinarySourceTransformSupportsModulo(t *testing.T) {
	template, dropsMetric, ok := applyBinarySourceTransform(parser.MOD, "{value}", 1.2345, false)
	if !ok {
		t.Fatal("expected modulo source transform to be native-lowerable")
	}
	if !dropsMetric {
		t.Fatal("expected modulo source transform to drop metric name")
	}
	for _, want := range []string{"positiveModulo(", "abs(", "isNaN(", "isInfinite(", "1.2345"} {
		if !strings.Contains(template, want) {
			t.Fatalf("expected modulo source transform to contain %q, got %q", want, template)
		}
	}
}

func mustParseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := logicalpkg.ParseExpression(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return expr
}
