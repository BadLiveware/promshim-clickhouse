package native

import (
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(rendered.SQL, "arraySort(item -> item.1, groupArray((d.timestamp, d.value))) AS window_series") {
		t.Fatalf("expected step-grid window materialization in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arraySum(arrayFilter(v -> NOT isNaN(v), arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series)))") {
		t.Fatalf("expected sum_over_time expression in SQL, got %q", rendered.SQL)
	}
	if got := rendered.QueryParams["param_range_window_matcher_0_value"]; got != "up" {
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
