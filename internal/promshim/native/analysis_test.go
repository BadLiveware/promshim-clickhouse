package native

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/prometheus/prometheus/model/labels"
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
	if info.Aggregation.Source == nil {
		t.Fatalf("expected aggregation source fragment, got %#v", info.Aggregation)
	}
	if !strings.Contains(info.Aggregation.Source.ValueExpr, "100") || !strings.Contains(info.Aggregation.Source.ValueExpr, "*") {
		t.Fatalf("expected transformed native source value expression, got %#v", info.Aggregation.Source)
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
		if info.Fragment == nil || info.Fragment.Aggregation == nil || info.Fragment.Aggregation.Op != tc.op {
			t.Fatalf("expected native aggregation fragment for %q, got %#v", tc.query, info)
		}
		if (tc.op == parser.TOPK || tc.op == parser.LIMITK || tc.op == parser.LIMIT_RATIO) && info.LabelLineage.MetricName != LabelLineageOriginal {
			t.Fatalf("expected selection aggregation to preserve metric-name lineage, got %#v", info.LabelLineage)
		}
		if tc.op == parser.COUNT_VALUES {
			if info.Fragment.Aggregation.ParamString != "sample_value" {
				t.Fatalf("expected count_values param string, got %#v", info.Fragment.Aggregation)
			}
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindUnarySourceExpr || !strings.Contains(info.Fragment.ValueExpr, "abs") {
		t.Fatalf("expected abs source-expression fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindInfoJoin || info.Fragment.InfoJoin == nil || info.Fragment.InfoJoin.InfoMetricName != "target_info" {
		t.Fatalf("expected info join fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.InfoJoin == nil || info.Fragment.InfoJoin.InfoMetricName != "" {
		t.Fatalf("expected matcher-driven info join fragment, got %#v", info)
	}
	foundRegex := false
	for _, matcher := range info.Fragment.InfoJoin.SelectorMatchers {
		if matcher != nil && matcher.Name == labels.MetricName && matcher.Type == labels.MatchRegexp && matcher.Value == ".+_info" {
			foundRegex = true
			break
		}
	}
	if !foundRegex {
		t.Fatalf("expected regex metric-name matcher to be preserved, got %#v", info.Fragment.InfoJoin)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindSyntheticSeries || info.Fragment.Synthetic == nil || info.Fragment.Synthetic.Value == nil || *info.Fragment.Synthetic.Value != 1 {
		t.Fatalf("expected lifted synthetic literal vector fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindSortTransform || info.Fragment.SortTransform == nil {
		t.Fatalf("expected sort transform fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindClampTransform || info.Fragment.ClampTransform == nil || info.Fragment.ClampTransform.Min == nil {
		t.Fatalf("expected clamp transform fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindScalarConvert || info.Fragment.ScalarConvert == nil {
		t.Fatalf("expected scalar convert fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindSyntheticSeries || info.Fragment.Synthetic == nil || info.Fragment.Synthetic.Func != "minute" {
		t.Fatalf("expected synthetic minute() fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindSyntheticSeries || info.Fragment.Synthetic == nil || info.Fragment.Synthetic.Func != "time" {
		t.Fatalf("expected synthetic time() fragment, got %#v", info)
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
			if info.Fragment == nil || info.Fragment.Kind != FragmentKindHistogramProjection || info.Fragment.HistogramProjection == nil || info.Fragment.HistogramProjection.Func != fn {
				t.Fatalf("expected histogram projection fragment for %s, got %#v", fn, info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindHistogramFunction || info.Fragment.HistogramFunction == nil || info.Fragment.HistogramFunction.Func != "histogram_quantile" {
		t.Fatalf("expected histogram function fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindHistogramFunction || info.Fragment.HistogramFunction == nil || info.Fragment.HistogramFunction.Func != "histogram_quantiles" || len(info.Fragment.HistogramFunction.Quantiles) != 2 {
		t.Fatalf("expected histogram_quantiles fragment, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindHistogramFunction || info.Fragment.HistogramFunction == nil || info.Fragment.HistogramFunction.Func != "histogram_fraction" {
		t.Fatalf("expected histogram function fragment for histogram_fraction, got %#v", info)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindAbsent || info.Fragment.Absent == nil || info.Fragment.Absent.Func != "absent" {
		t.Fatalf("expected absent fragment, got %#v", info)
	}
	if got := info.Fragment.Absent.OutputMetric["job"]; got != "api" {
		t.Fatalf("expected derived output metric to be preserved, got %#v", info.Fragment.Absent.OutputMetric)
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
	if info.Fragment == nil || info.Fragment.Kind != FragmentKindAbsent || info.Fragment.Absent == nil || info.Fragment.Absent.Func != "absent_over_time" {
		t.Fatalf("expected absent_over_time fragment, got %#v", info)
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
	if !info.NativeLowerable || info.Fragment == nil || info.Fragment.Kind != FragmentKindLabelTransform || info.Fragment.LabelTransform == nil {
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
	if info == nil || !info.NativeLowerable || info.Fragment == nil || info.Fragment.LabelTransform == nil {
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
	if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != "rate" {
		t.Fatalf("expected native rate fragment, got %#v", info)
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
		if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != tc.kind {
			t.Fatalf("expected native %s fragment, got %#v", tc.kind, info)
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
		if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != tc.kind {
			t.Fatalf("expected native %s fragment, got %#v", tc.kind, info)
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
		if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != fn {
			t.Fatalf("expected native %s fragment, got %#v", fn, info)
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
	if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != "quantile_over_time" {
		t.Fatalf("expected native quantile_over_time fragment, got %#v", info)
	}
	if info.Fragment.RangeFunction.ParamNumber == nil || *info.Fragment.RangeFunction.ParamNumber != 0.95 {
		t.Fatalf("expected quantile parameter, got %#v", info.Fragment.RangeFunction)
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
	if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != "predict_linear" {
		t.Fatalf("expected native predict_linear fragment, got %#v", info)
	}
	if info.Fragment.RangeFunction.ParamNumber == nil || *info.Fragment.RangeFunction.ParamNumber != 60 {
		t.Fatalf("expected predict_linear duration param, got %#v", info.Fragment.RangeFunction)
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
	if info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Func != "double_exponential_smoothing" {
		t.Fatalf("expected native smoothing fragment, got %#v", info)
	}
	if len(info.Fragment.RangeFunction.ParamNumbers) != 2 || *info.Fragment.RangeFunction.ParamNumbers[0] != 0.5 || *info.Fragment.RangeFunction.ParamNumbers[1] != 0.3 {
		t.Fatalf("expected smoothing params, got %#v", info.Fragment.RangeFunction)
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
	fragment, _, ok := applyScalarValueTransform(parser.MOD, &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindInstantVector}, leafLabelLineage(), 1.2345, false)
	if !ok {
		t.Fatal("expected modulo scalar transform to be native-lowerable")
	}
	if fragment == nil || fragment.ValueTransform == nil {
		t.Fatalf("expected value transform fragment, got %#v", fragment)
	}
	if got, want := fragment.ValueTransform.ValueExpr, "{value}"; got != want {
		t.Fatalf("expected raw child value expression for runtime modulo correction, got %q", got)
	}
	if fragment.ValueTransform.RuntimeTransform == nil {
		t.Fatal("expected runtime modulo correction metadata")
	}
	if got, want := fragment.ValueTransform.RuntimeTransform.Op, RuntimeValueTransformPromQLModulo; got != want {
		t.Fatalf("expected modulo runtime transform op %q, got %q", want, got)
	}
}

func TestApplySyntheticScalarChildTransformSupportsTimeScalarRoots(t *testing.T) {
	child := &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindScalar, Synthetic: &SyntheticSeriesFragment{Func: "time"}}
	fragment, _, ok := applySyntheticScalarChildTransform(parser.ADD, false, "time", child, unknownLineage(), true)
	if !ok {
		t.Fatal("expected time()+time() root to be native-lowerable")
	}
	if fragment == nil || fragment.Kind != FragmentKindValueTransform || fragment.OutputKind != OutputKindScalar {
		t.Fatalf("expected scalar value-transform fragment, got %#v", fragment)
	}
}

func TestApplySyntheticScalarChildTransformSupportsTimeVectorComparisons(t *testing.T) {
	child := &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindInstantVector}
	fragment, _, ok := applySyntheticScalarChildTransform(parser.GTE, false, "time", child, leafLabelLineage(), true)
	if !ok {
		t.Fatal("expected time() >= vector filter to be native-lowerable")
	}
	if fragment == nil || fragment.ValueTransform == nil || fragment.ValueTransform.FilterExpr == "" {
		t.Fatalf("expected comparison filter fragment, got %#v", fragment)
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
