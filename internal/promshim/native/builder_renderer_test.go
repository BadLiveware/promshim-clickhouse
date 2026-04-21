package native

import (
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/native/sqlb"
	planpkg "ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildFragmentReturnsAggregationTree(t *testing.T) {
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

	logical := &planpkg.LogicalAggregationPlan{
		Expr:     agg,
		Op:       agg.Op,
		Grouping: append([]string(nil), agg.Grouping...),
		Child: &planpkg.LogicalBinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &planpkg.LogicalLeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &planpkg.LogicalScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}

	fragment, err := BuildFragment(logical, nil)
	if err != nil {
		t.Fatalf("expected native fragment, got error: %v", err)
	}
	if fragment.Kind != FragmentKindAggregation {
		t.Fatalf("expected aggregation fragment, got %#v", fragment)
	}
	if fragment.Aggregation == nil || fragment.Aggregation.Source == nil {
		t.Fatalf("expected aggregation source fragment, got %#v", fragment)
	}
	if fragment.Aggregation.Source.Kind != FragmentKindBinaryScalarSourceExpr {
		t.Fatalf("expected binary scalar source fragment, got %#v", fragment.Aggregation.Source)
	}
	if fragment.Aggregation.Source.Selector == nil || fragment.Aggregation.Source.Selector.Kind != SelectorKindInstantVector {
		t.Fatalf("expected selector-backed source fragment, got %#v", fragment.Aggregation.Source)
	}
	if !strings.Contains(fragment.Aggregation.Source.ValueExpr, "100") || !strings.Contains(fragment.Aggregation.Source.ValueExpr, "*") {
		t.Fatalf("expected transformed source value expression, got %#v", fragment.Aggregation.Source)
	}
}

func TestRenderFragmentBuildsInstantRateSQLForSubquery(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "rate",
			Child: &NativeFragment{
				Kind:       FragmentKindSubquery,
				OutputKind: OutputKindRangeMatrix,
				Subquery: &SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &NativeFragment{
						Kind:       FragmentKindAggregation,
						OutputKind: OutputKindInstantVector,
						Aggregation: &AggregationFragment{
							Op: parser.SUM,
							Source: &NativeFragment{
								Kind:       FragmentKindLeafSource,
								OutputKind: OutputKindInstantVector,
								Selector: &SelectorSource{
									Kind:       SelectorKindInstantVector,
									MetricName: "up",
									Lookback:   defaultInstantSelectorLookback,
								},
								ValueExpr: "{value}",
								TagsExpr:  "{tags}",
							},
						},
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:             RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arrayMap((prev, cur) -> if(cur < prev, cur, cur - prev)") {
		t.Fatalf("expected rate delta expression in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "WHERE length(time_series) > 1") {
		t.Fatalf("expected minimum rate sample filter in SQL, got %q", rendered.SQL)
	}
	if got := rendered.QueryParams["param_range_instant_matcher_0_value"]; got != "up" {
		t.Fatalf("expected metric-name selector param, got %q with params=%#v", got, rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsInstantIncreaseSQLForDirectRangeSelector(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "increase",
			Child: &NativeFragment{
				Kind:       FragmentKindLeafSource,
				OutputKind: OutputKindRangeMatrix,
				Selector: &SelectorSource{
					Kind:       SelectorKindRangeVector,
					MetricName: "up",
					Lookback:   5 * time.Minute,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:             RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arraySum(arrayMap((prev, cur) -> if(cur < prev, cur, cur - prev)") {
		t.Fatalf("expected increase delta expression in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "WHERE length(time_series) > 1") {
		t.Fatalf("expected minimum sample filter in SQL, got %q", rendered.SQL)
	}
	if got := rendered.QueryParams["param_range_matrix_matcher_0_value"]; got != "up" {
		t.Fatalf("expected metric-name selector param, got %q with params=%#v", got, rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsInstantBinaryJoinSQLNamespacesSelectorParams(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindBinaryVectorJoin,
		OutputKind: OutputKindInstantVector,
		BinaryJoin: &BinaryJoinFragment{
			Op:        parser.ADD,
			JoinShape: JoinShapeOneToOne,
			VectorMatching: &parser.VectorMatching{
				Card: parser.CardOneToOne,
			},
			LHS: &NativeFragment{
				Kind:       FragmentKindLeafSource,
				OutputKind: OutputKindInstantVector,
				Selector: &SelectorSource{
					Kind:       SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   defaultInstantSelectorLookback,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
			RHS: &NativeFragment{
				Kind:       FragmentKindLeafSource,
				OutputKind: OutputKindInstantVector,
				Selector: &SelectorSource{
					Kind:       SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   defaultInstantSelectorLookback,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:             RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	for _, expected := range []string{"{lhs_instant_matcher_0_value:String}", "{rhs_instant_matcher_0_value:String}"} {
		if !strings.Contains(rendered.SQL, expected) {
			t.Fatalf("expected namespaced placeholder %q in SQL, got %q", expected, rendered.SQL)
		}
	}
	if got := rendered.QueryParams["param_lhs_instant_matcher_0_value"]; got != "up" {
		t.Fatalf("expected namespaced lhs selector param, got %q with params=%#v", got, rendered.QueryParams)
	}
	if got := rendered.QueryParams["param_rhs_instant_matcher_0_value"]; got != "up" {
		t.Fatalf("expected namespaced rhs selector param, got %q with params=%#v", got, rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsInstantDerivSQLForSubquery(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "deriv",
			Child: &NativeFragment{
				Kind:       FragmentKindSubquery,
				OutputKind: OutputKindRangeMatrix,
				Subquery: &SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &NativeFragment{
						Kind:       FragmentKindAggregation,
						OutputKind: OutputKindInstantVector,
						Aggregation: &AggregationFragment{
							Op: parser.SUM,
							Source: &NativeFragment{
								Kind:       FragmentKindLeafSource,
								OutputKind: OutputKindInstantVector,
								Selector: &SelectorSource{
									Kind:       SelectorKindInstantVector,
									MetricName: "up",
									Lookback:   defaultInstantSelectorLookback,
								},
								ValueExpr: "{value}",
								TagsExpr:  "{tags}",
							},
						},
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:             RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arraySum(arrayMap((x, y) -> x * y") || !strings.Contains(rendered.SQL, "/ ((toFloat64(length(time_series))") {
		t.Fatalf("expected deriv regression expression in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "WHERE length(time_series) > 1") {
		t.Fatalf("expected minimum deriv sample filter in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeSumOverTimeSQLForDirectSelector(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &NativeFragment{
				Kind:       FragmentKindLeafSource,
				OutputKind: OutputKindRangeMatrix,
				Selector: &SelectorSource{
					Kind:       SelectorKindRangeVector,
					MetricName: "up",
					Lookback:   5 * time.Minute,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -300000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series") {
		t.Fatalf("expected range result shaping in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts") || !strings.Contains(rendered.SQL, "AS window_series") {
		t.Fatalf("expected shared window-series materialization in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayMap(point -> tupleElement(point, 1), window_series) AS window_timestamps") {
		t.Fatalf("expected shared window timestamps array in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series) AS window_values") {
		t.Fatalf("expected shared window values array in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arraySum(arrayFilter(v -> NOT isNaN(v), arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series)))") {
		t.Fatalf("expected sum_over_time expression in SQL, got %q", rendered.SQL)
	}
	if got := rendered.QueryParams["param_range_matrix_matcher_0_value"]; got != "up" {
		t.Fatalf("expected metric-name selector param, got %q with params=%#v", got, rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsRangeSumOverTimeSQLForSubquery(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &NativeFragment{
				Kind:       FragmentKindSubquery,
				OutputKind: OutputKindRangeMatrix,
				Subquery: &SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &NativeFragment{
						Kind:       FragmentKindAggregation,
						OutputKind: OutputKindInstantVector,
						Aggregation: &AggregationFragment{
							Op: parser.SUM,
							Source: &NativeFragment{
								Kind:       FragmentKindLeafSource,
								OutputKind: OutputKindInstantVector,
								Selector:   &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", Lookback: defaultInstantSelectorLookback},
								ValueExpr:  "{value}",
								TagsExpr:   "{tags}",
							},
						},
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -300000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts") {
		t.Fatalf("expected subquery window filtering in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "CROSS JOIN") {
		t.Fatalf("expected eval grid cross join in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayMap(point -> tupleElement(point, 1), window_series) AS window_timestamps") {
		t.Fatalf("expected shared window timestamps array in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series) AS window_values") {
		t.Fatalf("expected shared window values array in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arraySum(arrayFilter(v -> NOT isNaN(v), arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series)))") {
		t.Fatalf("expected sum_over_time expression over window_series, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeRateSQLForSubquery(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "rate",
			Child: &NativeFragment{
				Kind:       FragmentKindSubquery,
				OutputKind: OutputKindRangeMatrix,
				Subquery: &SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &NativeFragment{
						Kind:       FragmentKindAggregation,
						OutputKind: OutputKindInstantVector,
						Aggregation: &AggregationFragment{
							Op: parser.SUM,
							Source: &NativeFragment{
								Kind:       FragmentKindLeafSource,
								OutputKind: OutputKindInstantVector,
								Selector:   &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", Lookback: defaultInstantSelectorLookback},
								ValueExpr:  "{value}",
								TagsExpr:   "{tags}",
							},
						},
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -300000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arrayMap((prev, cur) -> if(cur < prev, cur, cur - prev)") {
		t.Fatalf("expected rate expression over window_series, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts") {
		t.Fatalf("expected subquery window filtering in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeSumOverTimeSQLForSubqueryWithWrappedLocalChild(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindRangeFunction,
		OutputKind: OutputKindInstantVector,
		RangeFunction: &RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &NativeFragment{
				Kind:       FragmentKindSubquery,
				OutputKind: OutputKindRangeMatrix,
				Subquery: &SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &NativeFragment{
						Kind:       FragmentKindBinaryScalarSourceExpr,
						OutputKind: OutputKindInstantVector,
						Selector:   &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", Lookback: defaultInstantSelectorLookback},
						ValueExpr:  "({value}) * 100",
						TagsExpr:   "{tags}",
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -300000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if strings.Count(rendered.SQL, "SETTINGS allow_experimental_time_series_table = 1") != 1 {
		t.Fatalf("expected only outer SETTINGS clause in wrapped subquery SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayMap(point -> (point.1, (point.2) * 100), time_series)") {
		t.Fatalf("expected wrapped local child source expression in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsInstantPointwiseTransformSQL(t *testing.T) {
	testCases := []struct {
		name   string
		params []*float64
		want   string
	}{
		{name: "abs", want: "abs(value) AS value"},
		{name: "ceil", want: "ceil(value) AS value"},
		{name: "floor", want: "floor(value) AS value"},
		{name: "sgn", want: "sign(value) AS value"},
		{name: "exp", want: "exp(value) AS value"},
		{name: "ln", want: "log(value) AS value"},
		{name: "log2", want: "log2(value) AS value"},
		{name: "log10", want: "log10(value) AS value"},
		{name: "sqrt", want: "sqrt(value) AS value"},
		{name: "clamp", params: []*float64{floatPtr(1), floatPtr(2)}, want: "greatest(1, least(2, value)) AS value"},
		{name: "clamp_min", params: []*float64{floatPtr(1)}, want: "greatest(value, 1) AS value"},
		{name: "clamp_max", params: []*float64{floatPtr(2)}, want: "least(value, 2) AS value"},
		{name: "sin", want: "sin(value) AS value"},
		{name: "cos", want: "cos(value) AS value"},
		{name: "tan", want: "tan(value) AS value"},
		{name: "asin", want: "asin(value) AS value"},
		{name: "acos", want: "acos(value) AS value"},
		{name: "atan", want: "atan(value) AS value"},
		{name: "sinh", want: "sinh(value) AS value"},
		{name: "cosh", want: "cosh(value) AS value"},
		{name: "tanh", want: "tanh(value) AS value"},
		{name: "asinh", want: "asinh(value) AS value"},
		{name: "acosh", want: "acosh(value) AS value"},
		{name: "atanh", want: "atanh(value) AS value"},
		{name: "deg", want: "degrees(value) AS value"},
		{name: "rad", want: "radians(value) AS value"},
		{name: "timestamp", want: "toFloat64(toUnixTimestamp64Milli(timestamp)) / 1000.0 AS value"},
		{name: "minute", want: "toFloat64(toMinute(toDateTime(toInt64(value), 'UTC'))) AS value"},
		{name: "hour", want: "toFloat64(toHour(toDateTime(toInt64(value), 'UTC'))) AS value"},
		{name: "day_of_week", want: "toFloat64(modulo(toDayOfWeek(toDateTime(toInt64(value), 'UTC')), 7)) AS value"},
		{name: "day_of_month", want: "toFloat64(toDayOfMonth(toDateTime(toInt64(value), 'UTC'))) AS value"},
		{name: "day_of_year", want: "toFloat64(toDayOfYear(toDateTime(toInt64(value), 'UTC'))) AS value"},
		{name: "days_in_month", want: "toFloat64(toDaysInMonth(toDateTime(toInt64(value), 'UTC'))) AS value"},
		{name: "month", want: "toFloat64(toMonth(toDateTime(toInt64(value), 'UTC'))) AS value"},
		{name: "year", want: "toFloat64(toYear(toDateTime(toInt64(value), 'UTC'))) AS value"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			template, ok := nativePointwiseSourceTemplate(tc.name, tc.params)
			if !ok {
				t.Fatalf("expected native template for %s", tc.name)
			}
			fragment := &NativeFragment{
				Kind:        FragmentKindUnarySourceExpr,
				OutputKind:  OutputKindInstantVector,
				Selector:    &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", Lookback: defaultInstantSelectorLookback},
				ValueExpr:   template,
				TagsExpr:    "arrayFilter(tag -> tag.1 != '__name__', {tags})",
				DropsMetric: true,
			}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
			if err != nil {
				t.Fatalf("expected rendered SQL, got error: %v", err)
			}
			if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(tc.want)) {
				t.Fatalf("expected SQL to contain %q, got %q", tc.want, rendered.SQL)
			}
		})
	}
}

func TestRenderFragmentBuildsSyntheticRangeSeriesSQL(t *testing.T) {
	testCases := []string{"minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year"}
	for _, name := range testCases {
		t.Run(name, func(t *testing.T) {
			fragment := &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindInstantVector, Synthetic: &SyntheticSeriesFragment{Func: name}}
			expectedValueSQL, err := syntheticSeriesValueSQL(name, "ts_ms")
			if err != nil {
				t.Fatalf("expected synthetic value SQL, got error: %v", err)
			}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000})
			if err != nil {
				t.Fatalf("expected rendered SQL, got error: %v", err)
			}
			if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(expectedValueSQL)) {
				t.Fatalf("expected synthetic %s range SQL to contain %q, got %q", name, expectedValueSQL, rendered.SQL)
			}
			if rendered.QueryParams["param_step_ms"] != "60000" {
				t.Fatalf("expected step param, got %#v", rendered.QueryParams)
			}
		})
	}
}

func TestRenderFragmentBuildsSyntheticInstantScalarSQL(t *testing.T) {
	testCases := []struct {
		name string
		want string
	}{
		{name: "time", want: "toFloat64({evaluation_ms:Int64}) / 1000.0 AS value"},
		{name: "pi", want: "toFloat64(3.141592653589793) AS value"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fragment := &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindScalar, Synthetic: &SyntheticSeriesFragment{Func: tc.name}}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 123456})
			if err != nil {
				t.Fatalf("expected rendered SQL, got error: %v", err)
			}
			if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(tc.want)) {
				t.Fatalf("expected synthetic %s instant SQL to contain %q, got %q", tc.name, tc.want, rendered.SQL)
			}
			if tc.name == "time" && rendered.QueryParams["param_evaluation_ms"] != "123456" {
				t.Fatalf("expected evaluation param, got %#v", rendered.QueryParams)
			}
		})
	}
}

func TestRenderFragmentBuildsInfoJoinSQL(t *testing.T) {
	fragment := &NativeFragment{Kind: FragmentKindInfoJoin, OutputKind: OutputKindInstantVector, InfoJoin: &InfoJoinFragment{Child: &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindInstantVector, Selector: &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", Lookback: defaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}, InfoMetricName: "target_info", SelectorMatchers: nil, CopyLabelNames: []string{"k8s_cluster_name"}, DropUnmatched: false}}

	instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected instant info join SQL, got error: %v", err)
	}
	instantChecks := []string{"LEFT JOIN", "lhs.join_group = rhs.join_group", "k8s_cluster_name", "lhs.value AS value", "series.tags AS tags"}
	for _, check := range instantChecks {
		if !strings.Contains(sqlb.NormalizeSQL(instant.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected instant info join SQL to contain %q, got %q", check, instant.SQL)
		}
	}
	if instant.QueryParams["param_instant_matcher_0_value"] != "target_info" {
		t.Fatalf("expected target_info selector param, got %#v", instant.QueryParams)
	}

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
	if err != nil {
		t.Fatalf("expected range info join SQL, got error: %v", err)
	}
	rangeChecks := []string{"lhs.join_group = rhs.join_group AND lhs.timestamp = rhs.timestamp", "ARRAY JOIN", "groupArray((timestamp, value))"}
	for _, check := range rangeChecks {
		if !strings.Contains(sqlb.NormalizeSQL(rangeRendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected range info join SQL to contain %q, got %q", check, rangeRendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsScalarConvertSQL(t *testing.T) {
	fragment := &NativeFragment{Kind: FragmentKindScalarConvert, OutputKind: OutputKindScalar, ScalarConvert: &ScalarConvertFragment{Child: &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindInstantVector, Selector: &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", Lookback: defaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}

	instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected instant scalar convert SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(instant.SQL), sqlb.NormalizeSQL("if(count() = 1, any(value), nan) AS value")) {
		t.Fatalf("expected scalar convert instant SQL, got %q", instant.SQL)
	}

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
	if err != nil {
		t.Fatalf("expected range scalar convert SQL, got error: %v", err)
	}
	checks := []string{"groupArray((timestamp, value))", "if(ifNull(scalar_values.sample_count, 0) = 1, scalar_values.any_value, nan)", "ARRAY JOIN scalar_child.time_series AS point"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rangeRendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected scalar convert range SQL to contain %q, got %q", check, rangeRendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsTier1RangeFunctionSQL(t *testing.T) {
	testCases := []struct {
		name string
		want string
	}{
		{name: "stddev_over_time", want: "arrayReduce('stddevPop'"},
		{name: "stdvar_over_time", want: "arrayReduce('varPop'"},
		{name: "present_over_time", want: "toFloat64(1)"},
		{name: "resets", want: "arrayPopFront"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fragment := &NativeFragment{Kind: FragmentKindRangeFunction, OutputKind: OutputKindInstantVector, RangeFunction: &RangeFunctionFragment{Func: tc.name, Child: &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindRangeMatrix, Selector: &SelectorSource{Kind: SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 300000})
			if err != nil {
				t.Fatalf("expected rendered SQL, got error: %v", err)
			}
			if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(tc.want)) {
				t.Fatalf("expected %s SQL to contain %q, got %q", tc.name, tc.want, rendered.SQL)
			}
		})
	}
}

func TestRenderFragmentBuildsMadOverTimeSQL(t *testing.T) {
	fragment := &NativeFragment{Kind: FragmentKindRangeFunction, OutputKind: OutputKindInstantVector, RangeFunction: &RangeFunctionFragment{Func: "mad_over_time", Child: &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindRangeMatrix, Selector: &SelectorSource{Kind: SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	checks := []string{"arrayReduce('quantileExact(0.5)'", "arrayMap(x -> abs(x - arrayReduce('quantileExact(0.5)'", "window_values"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected mad_over_time SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsPredictLinearSQL(t *testing.T) {
	duration := 60.0
	fragment := &NativeFragment{Kind: FragmentKindRangeFunction, OutputKind: OutputKindInstantVector, RangeFunction: &RangeFunctionFragment{Func: "predict_linear", ParamNumber: &duration, Child: &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindRangeMatrix, Selector: &SelectorSource{Kind: SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	checks := []string{"simpleLinearRegression", "toUnixTimestamp64Milli(eval_ts)", "window_timestamps", "window_values", "multiIf"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected predict_linear SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsDoubleExponentialSmoothingSQL(t *testing.T) {
	sf, tf := 0.5, 0.3
	fragment := &NativeFragment{Kind: FragmentKindRangeFunction, OutputKind: OutputKindInstantVector, RangeFunction: &RangeFunctionFragment{Func: "double_exponential_smoothing", ParamNumbers: []*float64{&sf, &tf}, Child: &NativeFragment{Kind: FragmentKindLeafSource, OutputKind: OutputKindRangeMatrix, Selector: &SelectorSource{Kind: SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	checks := []string{"arrayFold", "arraySlice", "tupleElement(acc, 1)", "window_values", "multiIf"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected smoothing SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsRangeAggregationSQL(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindAggregation,
		OutputKind: OutputKindInstantVector,
		Aggregation: &AggregationFragment{
			Op:       parser.SUM,
			Grouping: []string{"job"},
			Source: &NativeFragment{
				Kind:       FragmentKindBinaryScalarSourceExpr,
				OutputKind: OutputKindInstantVector,
				Selector: &SelectorSource{
					Kind:       SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   defaultInstantSelectorLookback,
				},
				ValueExpr:   "({value}) * 100",
				TagsExpr:    "arrayFilter(tag -> tag.1 != '__name__', {tags})",
				DropsMetric: true,
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: 0,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "timeSeriesData") || !strings.Contains(rendered.SQL, "timeSeriesTags") {
		t.Fatalf("expected repo-owned selector SQL source, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "({value}) * 100") && !strings.Contains(rendered.SQL, "(point.2) * 100") {
		t.Fatalf("expected transformed value expression in SQL, got %q", rendered.SQL)
	}
	if got := rendered.QueryParams["param_range_instant_matcher_0_value"]; got != "up" {
		t.Fatalf("expected metric-name selector param, got %q with params=%#v", got, rendered.QueryParams)
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
