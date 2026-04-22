package renderer

import (
	"ch-observability/internal/promshim/native"
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/native/sqlb"
	planpkg "ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// TestAlignSubqueryStepStartMatchesPromEngine guards the epoch-aligned
// subquery step-grid behavior against drift using cases observed in the
// reference Prometheus harness.
func TestAlignSubqueryStepStartMatchesPromEngine(t *testing.T) {
	cases := []struct {
		name        string
		windowStart int64
		step        int64
		want        int64
	}{
		{name: "epoch-aligned window start keeps left edge", windowStart: 1776807220000, step: 10000, want: 1776807220000},
		{name: "non-aligned window start rounds up", windowStart: 1776807222000, step: 10000, want: 1776807230000},
		{name: "negative epoch-aligned window start keeps left edge", windowStart: -360000, step: 60000, want: -360000},
		{name: "non-aligned window with 1m step", windowStart: -350000, step: 60000, want: -300000},
		{name: "zero step is a no-op", windowStart: 12345, step: 0, want: 12345},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := alignSubqueryStepStart(tc.windowStart, tc.step); got != tc.want {
				t.Fatalf("alignSubqueryStepStart(%d, %d) = %d, want %d", tc.windowStart, tc.step, got, tc.want)
			}
		})
	}
}

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

	fragment, err := native.BuildFragment(logical, nil)
	if err != nil {
		t.Fatalf("expected native fragment, got error: %v", err)
	}
	if fragment.Kind != native.FragmentKindAggregation {
		t.Fatalf("expected aggregation fragment, got %#v", fragment)
	}
	if fragment.Aggregation == nil || fragment.Aggregation.Source == nil {
		t.Fatalf("expected aggregation source fragment, got %#v", fragment)
	}
	if fragment.Aggregation.Source.Kind != native.FragmentKindBinaryScalarSourceExpr {
		t.Fatalf("expected binary scalar source fragment, got %#v", fragment.Aggregation.Source)
	}
	if fragment.Aggregation.Source.Selector == nil || fragment.Aggregation.Source.Selector.Kind != native.SelectorKindInstantVector {
		t.Fatalf("expected selector-backed source fragment, got %#v", fragment.Aggregation.Source)
	}
	if !strings.Contains(fragment.Aggregation.Source.ValueExpr, "100") || !strings.Contains(fragment.Aggregation.Source.ValueExpr, "*") {
		t.Fatalf("expected transformed source value expression, got %#v", fragment.Aggregation.Source)
	}
}

func TestRenderFragmentBuildsInstantRateSQLForSubquery(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "rate",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindAggregation,
						OutputKind: native.OutputKindInstantVector,
						Aggregation: &native.AggregationFragment{
							Op: parser.SUM,
							Source: &native.NativeFragment{
								Kind:       native.FragmentKindLeafSource,
								OutputKind: native.OutputKindInstantVector,
								Selector: &native.SelectorSource{
									Kind:       native.SelectorKindInstantVector,
									MetricName: "up",
									Lookback:   native.DefaultInstantSelectorLookback,
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
		Mode:             native.RenderModeInstant,
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
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "increase",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindRangeMatrix,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindRangeVector,
					MetricName: "up",
					Lookback:   5 * time.Minute,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:             native.RenderModeInstant,
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
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindBinaryVectorJoin,
		OutputKind: native.OutputKindInstantVector,
		BinaryJoin: &native.BinaryJoinFragment{
			Op:        parser.ADD,
			JoinShape: native.JoinShapeOneToOne,
			VectorMatching: &parser.VectorMatching{
				Card: parser.CardOneToOne,
			},
			LHS: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindInstantVector,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   native.DefaultInstantSelectorLookback,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
			RHS: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindInstantVector,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   native.DefaultInstantSelectorLookback,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:             native.RenderModeInstant,
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
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "deriv",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindAggregation,
						OutputKind: native.OutputKindInstantVector,
						Aggregation: &native.AggregationFragment{
							Op: parser.SUM,
							Source: &native.NativeFragment{
								Kind:       native.FragmentKindLeafSource,
								OutputKind: native.OutputKindInstantVector,
								Selector: &native.SelectorSource{
									Kind:       native.SelectorKindInstantVector,
									MetricName: "up",
									Lookback:   native.DefaultInstantSelectorLookback,
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
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "arrayReduce('simpleLinearRegression'") || !strings.Contains(rendered.SQL, "toUnixTimestamp64Milli(ts)") {
		t.Fatalf("expected deriv regression expression in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "WHERE length(time_series) > 1") {
		t.Fatalf("expected minimum deriv sample filter in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeSumOverTimeSQLForDirectSelector(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindRangeMatrix,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindRangeVector,
					MetricName: "up",
					Lookback:   5 * time.Minute,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
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
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindAggregation,
						OutputKind: native.OutputKindInstantVector,
						Aggregation: &native.AggregationFragment{
							Op: parser.SUM,
							Source: &native.NativeFragment{
								Kind:       native.FragmentKindLeafSource,
								OutputKind: native.OutputKindInstantVector,
								Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
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
		Mode:            native.RenderModeRange,
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

func TestRangeRequiredBoundsForChildIncludesSelectorOffset(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindLeafSource,
		OutputKind: native.OutputKindRangeMatrix,
		Selector:   &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute, Offset: time.Minute},
		ValueExpr:  "{value}",
		TagsExpr:   "{tags}",
	}
	startMS, endMS := rangeRequiredBoundsForChild(fragment, 0, 300000)
	if got, want := startMS, int64(-360000); got != want {
		t.Fatalf("expected start bound %d, got %d", want, got)
	}
	if got, want := endMS, int64(240000); got != want {
		t.Fatalf("expected end bound %d, got %d", want, got)
	}
}

func TestRenderFragmentBuildsRangeRateSQLForSubquery(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "rate",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindAggregation,
						OutputKind: native.OutputKindInstantVector,
						Aggregation: &native.AggregationFragment{
							Op: parser.SUM,
							Source: &native.NativeFragment{
								Kind:       native.FragmentKindLeafSource,
								OutputKind: native.OutputKindInstantVector,
								Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
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
		Mode:            native.RenderModeRange,
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

func TestRenderFragmentBuildsRangeFunctionSQLForOffsetSelectorUsesShiftedRequiredBounds(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindRangeMatrix,
				Selector:   &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute, Offset: time.Minute},
				ValueExpr:  "{value}",
				TagsExpr:   "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -360000,
		RequiredEndMS:   240000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64})") {
		t.Fatalf("expected shifted required start bound in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "grid.eval_ts - toIntervalMillisecond(60000)") {
		t.Fatalf("expected offset-adjusted eval window in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "grid.eval_ts - toIntervalMillisecond(360000)") {
		t.Fatalf("expected shifted required start bound in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeSubqueryUsingInnerStepAndExpandedEnvelope(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range:  5 * time.Minute,
					Step:   time.Minute,
					Offset: time.Minute,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindLeafSource,
						OutputKind: native.OutputKindInstantVector,
						Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
						ValueExpr:  "{value}",
						TagsExpr:   "{tags}",
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -660000,
		RequiredEndMS:   240000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if got, want := rendered.QueryParams["param_step_ms"], "60000"; got != want {
		t.Fatalf("expected inner subquery step %q, got %#v", want, rendered.QueryParams)
	}
	// Subquery step grid is epoch-anchored and keeps an exact left-edge step when
	// the expanded window start already lands on the step grid.
	if got, want := rendered.QueryParams["param_start_ms"], "-360000"; got != want {
		t.Fatalf("expected epoch-aligned subquery start %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_end_ms"], "240000"; got != want {
		t.Fatalf("expected expanded subquery end %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_required_start_ms"], "-660000"; got != want {
		t.Fatalf("expected child required start %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_required_end_ms"], "240000"; got != want {
		t.Fatalf("expected child required end %q, got %#v", want, rendered.QueryParams)
	}
	if !strings.Contains(rendered.SQL, "grid.eval_ts - toIntervalMillisecond(360000)") {
		t.Fatalf("expected 5m subquery window in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "grid.eval_ts - toIntervalMillisecond(60000)") {
		t.Fatalf("expected 1m subquery offset in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeSubquerySourceSQL(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindSubquery,
		OutputKind: native.OutputKindRangeMatrix,
		Subquery: &native.SubqueryFragment{
			Range:  5 * time.Minute,
			Step:   time.Minute,
			Offset: time.Minute,
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindBinaryScalarSourceExpr,
				OutputKind: native.OutputKindInstantVector,
				Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
				ValueExpr:  "({value}) * 100",
				TagsExpr:   "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -660000,
		RequiredEndMS:   240000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if got, want := rendered.QueryParams["param_step_ms"], "60000"; got != want {
		t.Fatalf("expected subquery inner step %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_start_ms"], "-360000"; got != want {
		t.Fatalf("expected epoch-aligned subquery start %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_end_ms"], "240000"; got != want {
		t.Fatalf("expected expanded subquery end %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_required_start_ms"], "-660000"; got != want {
		t.Fatalf("expected child required start %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_required_end_ms"], "240000"; got != want {
		t.Fatalf("expected child required end %q, got %#v", want, rendered.QueryParams)
	}
	if strings.Count(rendered.SQL, "SETTINGS allow_experimental_time_series_table = 1") != 1 {
		t.Fatalf("expected a single SETTINGS clause in rendered subquery SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arrayMap(point -> (point.1, (point.2) * 100), time_series)") {
		t.Fatalf("expected wrapped child source expression in subquery SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsAnchoredRangeBroadcastSQL(t *testing.T) {
	anchorMS := int64(300000)
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindUnarySourceExpr,
		OutputKind: native.OutputKindInstantVector,
		Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, Timestamp: &anchorMS},
		ValueExpr:  "abs({value})",
		TagsExpr:   "{tags}",
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          60000,
		RequiredStartMS: 0,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected anchored rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "CROSS JOIN") {
		t.Fatalf("expected anchored range broadcast SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "anchored_child.value") {
		t.Fatalf("expected anchored child value broadcast in SQL, got %q", rendered.SQL)
	}
	if got, want := rendered.QueryParams["param_anchored_range_start_ms"], "0"; got != want {
		t.Fatalf("expected anchored range start %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_anchored_range_end_ms"], "300000"; got != want {
		t.Fatalf("expected anchored range end %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_anchored_range_step_ms"], "60000"; got != want {
		t.Fatalf("expected anchored range step %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_anchored_child_required_start_ms"], "0"; got != want {
		t.Fatalf("expected namespaced anchored child required start %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_anchored_child_required_end_ms"], "300000"; got != want {
		t.Fatalf("expected namespaced anchored child required end %q, got %#v", want, rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsRangeBinarySQLWithDifferentlyAnchoredChildren(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindBinaryVectorJoin,
		OutputKind: native.OutputKindRangeMatrix,
		BinaryJoin: &native.BinaryJoinFragment{
			Op: parser.ADD,
			VectorMatching: &parser.VectorMatching{
				Card: parser.CardOneToOne,
			},
			JoinShape: native.JoinShapeOneToOne,
			LHS: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindInstantVector,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   native.DefaultInstantSelectorLookback,
					StartOrEnd: parser.START,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
			RHS: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindInstantVector,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   native.DefaultInstantSelectorLookback,
					StartOrEnd: parser.END,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          60000,
		RequiredStartMS: 0,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered range binary SQL, got error: %v", err)
	}
	for _, check := range []string{"lhs.join_group = rhs.join_group AND lhs.timestamp = rhs.timestamp", "lhs_anchored_range_start_ms", "rhs_anchored_range_start_ms", "anchored_child.value"} {
		if !strings.Contains(rendered.SQL, check) {
			t.Fatalf("expected range binary SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
	if _, ok := rendered.QueryParams["param_anchored_range_start_ms"]; ok {
		t.Fatalf("did not expect root anchored broadcast params, got %#v", rendered.QueryParams)
	}
	for _, key := range []string{"param_lhs_anchored_range_start_ms", "param_rhs_anchored_range_start_ms", "param_lhs_anchored_child_required_end_ms", "param_rhs_anchored_child_required_end_ms"} {
		if _, ok := rendered.QueryParams[key]; !ok {
			t.Fatalf("expected namespaced anchored child param %q, got %#v", key, rendered.QueryParams)
		}
	}
}

func TestRenderFragmentBuildsRangeBinarySQLWithOffsetChildSpecificBounds(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindBinaryVectorJoin,
		OutputKind: native.OutputKindRangeMatrix,
		BinaryJoin: &native.BinaryJoinFragment{
			Op: parser.SUB,
			VectorMatching: &parser.VectorMatching{
				Card: parser.CardOneToOne,
			},
			JoinShape: native.JoinShapeOneToOne,
			LHS: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindInstantVector,
				Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
				ValueExpr:  "{value}",
				TagsExpr:   "{tags}",
			},
			RHS: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindInstantVector,
				Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, Offset: time.Minute},
				ValueExpr:  "{value}",
				TagsExpr:   "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          60000,
		RequiredStartMS: -360000,
		RequiredEndMS:   240000,
	})
	if err != nil {
		t.Fatalf("expected rendered range binary SQL with offset child, got error: %v", err)
	}
	if got, want := rendered.QueryParams["param_lhs_required_end_ms"], "300000"; got != want {
		t.Fatalf("expected lhs to use its own required end %q, got %#v", want, rendered.QueryParams)
	}
	if got, want := rendered.QueryParams["param_rhs_required_end_ms"], "240000"; got != want {
		t.Fatalf("expected rhs to use offset-adjusted required end %q, got %#v", want, rendered.QueryParams)
	}
}

func TestRenderFragmentDefaultsMissingSubqueryStepToOneMinute(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  0,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindLeafSource,
						OutputKind: native.OutputKindInstantVector,
						Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
						ValueExpr:  "{value}",
						TagsExpr:   "{tags}",
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -600000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if got, want := rendered.QueryParams["param_step_ms"], "60000"; got != want {
		t.Fatalf("expected default subquery step %q, got %#v", want, rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsRangeSumOverTimeSQLForSubqueryWithWrappedLocalChild(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "sum_over_time",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindSubquery,
				OutputKind: native.OutputKindRangeMatrix,
				Subquery: &native.SubqueryFragment{
					Range: 5 * time.Minute,
					Step:  time.Minute,
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindBinaryScalarSourceExpr,
						OutputKind: native.OutputKindInstantVector,
						Selector:   &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
						ValueExpr:  "({value}) * 100",
						TagsExpr:   "{tags}",
					},
				},
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
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
		{name: "tanh", want: "(exp(2 * value) - 1) / (exp(2 * value) + 1) AS value"},
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
			template, ok := native.NativePointwiseSourceTemplate(tc.name, tc.params)
			if !ok {
				t.Fatalf("expected native template for %s", tc.name)
			}
			fragment := &native.NativeFragment{
				Kind:        native.FragmentKindUnarySourceExpr,
				OutputKind:  native.OutputKindInstantVector,
				Selector:    &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback},
				ValueExpr:   template,
				TagsExpr:    "arrayFilter(tag -> tag.1 != '__name__', {tags})",
				DropsMetric: true,
			}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
			if err != nil {
				t.Fatalf("expected rendered SQL, got error: %v", err)
			}
			if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(tc.want)) {
				t.Fatalf("expected SQL to contain %q, got %q", tc.want, rendered.SQL)
			}
		})
	}
}

func TestRenderFragmentBuildsClampTransformSQLWithScalarBoundChildren(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindClampTransform, OutputKind: native.OutputKindInstantVector, ClampTransform: &native.ClampTransformFragment{Func: "clamp_min", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}, Min: &native.NativeFragment{Kind: native.FragmentKindScalarConvert, OutputKind: native.OutputKindScalar, ScalarConvert: &native.ScalarConvertFragment{Child: &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Source: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected rendered clamp transform SQL, got error: %v", err)
	}
	checks := []string{"LEFT JOIN (SELECT point.1 AS timestamp", "clamp_min.timestamp = base.timestamp", "greatest(base.value, clamp_min.value)", "arrayFilter(tag -> tag.1 != '__name__', base.tags) AS tags"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected clamp transform SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsSortTransformSQL(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindSortTransform, OutputKind: native.OutputKindInstantVector, SortTransform: &native.SortTransformFragment{Func: "sort_by_label_desc", Labels: []string{"instance", "job"}, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, RequireFullTags: true}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected rendered sort SQL, got error: %v", err)
	}
	checks := []string{"ORDER BY naturalSortKey(tupleElement(arrayFirst(tag -> tag.1 = 'instance'", "sort_child.tags DESC", "sort_child.timestamp DESC"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected sort transform SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsLabelJoinTransformSQL(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindLabelTransform, OutputKind: native.OutputKindInstantVector, LabelTransform: &native.LabelTransformFragment{Func: "label_join", Dst: "joined", Separator: "/", SrcLabels: []string{"instance", "job"}, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, RequireFullTags: true}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected rendered label_join SQL, got error: %v", err)
	}
	checks := []string{"arrayStringConcat([tupleElement(arrayFirst(tag -> tag.1 = 'instance'", "tuple('joined', arrayStringConcat", "throwIf(count() > 1, 'vector cannot contain metrics with the same labelset')"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected label_join SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsLabelReplaceTransformRangeSQL(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindLabelTransform, OutputKind: native.OutputKindInstantVector, LabelTransform: &native.LabelTransformFragment{Func: "label_replace", Dst: "job_copy", Repl: "$1", Regex: "^(?s:(.*))$", RegexSubexpNames: []string{"", ""}, Src: "job", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, RequireFullTags: true}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000})
	if err != nil {
		t.Fatalf("expected rendered label_replace range SQL, got error: %v", err)
	}
	checks := []string{"replaceRegexpOne(tupleElement(arrayFirst(tag -> tag.1 = 'job'", "'\\\\1'", "arrayFlatten(groupArray(time_series))", "throwIf(arrayExists((idx, point) -> if(idx = 1, 0"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected label_replace range SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestGoReplacementToClickHouseDropsOutOfRangeNumericCaptureReferences(t *testing.T) {
	got := goReplacementToClickHouse("value-$1", []string{""})
	if got != "value-" {
		t.Fatalf("expected out-of-range capture reference to be omitted, got %q", got)
	}
}

func TestRenderFragmentBuildsRangeValueTransformFilterSQLThatDropsEmptySeries(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindValueTransform, OutputKind: native.OutputKindInstantVector, ValueTransform: &native.ValueTransformFragment{Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, RequireFullTags: true}, ValueExpr: "{value}", TagsExpr: "{tags}"}, ValueExpr: "{value}", FilterExpr: "({value}) = 1.2345"}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
	if err != nil {
		t.Fatalf("expected rendered range value-transform SQL, got error: %v", err)
	}
	normalized := sqlb.NormalizeSQL(rendered.SQL)
	if !strings.Contains(normalized, sqlb.NormalizeSQL("arrayFilter(point -> (point.2) = 1.2345, time_series)")) {
		t.Fatalf("expected point-level range filter in SQL, got %q", rendered.SQL)
	}
	if !strings.Contains(normalized, sqlb.NormalizeSQL("WHERE length(arrayFilter(point -> (point.2) = 1.2345, time_series)) > 0")) {
		t.Fatalf("expected filtered-out empty series in SQL, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsLabelReplaceSQLWithoutInvalidBackrefsForNoCaptureRegex(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindLabelTransform, OutputKind: native.OutputKindInstantVector, LabelTransform: &native.LabelTransformFragment{Func: "label_replace", Dst: "job", Repl: "value-$1", Regex: "^(?s:non-matching-regex)$", RegexSubexpNames: []string{""}, Src: "instance", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback, RequireFullTags: true}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected rendered label_replace instant SQL, got error: %v", err)
	}
	normalized := sqlb.NormalizeSQL(rendered.SQL)
	if strings.Contains(normalized, sqlb.NormalizeSQL("'value-\\\\1'")) {
		t.Fatalf("expected non-capturing regex replacement to avoid invalid ClickHouse backrefs, got %q", rendered.SQL)
	}
	if !strings.Contains(normalized, sqlb.NormalizeSQL("'value-'")) {
		t.Fatalf("expected rendered SQL to preserve the literal replacement prefix, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsSyntheticRangeSeriesSQL(t *testing.T) {
	testCases := []string{"minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year"}
	for _, name := range testCases {
		t.Run(name, func(t *testing.T) {
			fragment := &native.NativeFragment{Kind: native.FragmentKindSyntheticSeries, OutputKind: native.OutputKindInstantVector, Synthetic: &native.SyntheticSeriesFragment{Func: name}}
			expectedValueSQL, err := syntheticSeriesValueSQL(fragment.Synthetic, "ts_ms")
			if err != nil {
				t.Fatalf("expected synthetic value SQL, got error: %v", err)
			}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000})
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

func TestRenderFragmentBuildsSyntheticLiteralScalarSQL(t *testing.T) {
	value := 1.25
	fragment := &native.NativeFragment{Kind: native.FragmentKindSyntheticSeries, OutputKind: native.OutputKindScalar, Synthetic: &native.SyntheticSeriesFragment{Func: "literal", Value: &value}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456})
	if err != nil {
		t.Fatalf("expected rendered literal scalar SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL("1.25 AS value")) {
		t.Fatalf("expected literal scalar SQL, got %q", rendered.SQL)
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
			fragment := &native.NativeFragment{Kind: native.FragmentKindSyntheticSeries, OutputKind: native.OutputKindScalar, Synthetic: &native.SyntheticSeriesFragment{Func: tc.name}}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456})
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
	fragment := &native.NativeFragment{Kind: native.FragmentKindInfoJoin, OutputKind: native.OutputKindInstantVector, InfoJoin: &native.InfoJoinFragment{Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}, InfoMetricName: "target_info", SelectorMatchers: nil, CopyLabelNames: []string{"k8s_cluster_name"}, DropUnmatched: false}}

	instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
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

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
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

func TestRenderFragmentBuildsInfoJoinSQLForRegexMetricMatcher(t *testing.T) {
	metricMatcher := labels.MustNewMatcher(labels.MatchRegexp, labels.MetricName, ".+_info")
	fragment := &native.NativeFragment{Kind: native.FragmentKindInfoJoin, OutputKind: native.OutputKindInstantVector, InfoJoin: &native.InfoJoinFragment{Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}, SelectorMatchers: []*labels.Matcher{metricMatcher}, DropUnmatched: false}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected instant regex info join SQL, got error: %v", err)
	}
	if got := rendered.QueryParams["param_instant_matcher_0_value"]; got != "^(?:.+_info)$" {
		t.Fatalf("expected regex info selector param, got %#v", rendered.QueryParams)
	}
	for _, check := range []string{"throwIf(count() > 1, 'found duplicate series for info metric on identifying labels')", "groupArrayIf(rhs.original_group", "lhs_is_info_metric"} {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected regex info join SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsScalarConvertSQL(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindScalarConvert, OutputKind: native.OutputKindScalar, ScalarConvert: &native.ScalarConvertFragment{Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}

	instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected instant scalar convert SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(instant.SQL), sqlb.NormalizeSQL("if(count() = 1, any(value), nan) AS value")) {
		t.Fatalf("expected scalar convert instant SQL, got %q", instant.SQL)
	}

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
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

func TestRenderClassicHistogramGroupsQueryBuildsInstantMaterializationSQL(t *testing.T) {
	fragment, err := renderClassicHistogramGroupsQuery(storage.QueryConfig{Database: "observability", Table: "prometheus"}, &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le", "job"}, Source: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "http_request_duration_seconds_bucket", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456}, "hist")
	if err != nil {
		t.Fatalf("expected classic histogram instant materialization SQL, got error: %v", err)
	}
	rendered, err := finalizeRenderedFragment(fragment)
	if err != nil {
		t.Fatalf("expected finalized classic histogram instant SQL, got error: %v", err)
	}
	checks := []string{
		"arrayFilter(tag -> tag.1 != 'le' AND tag.1 != '__name__'",
		"tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2) AS le_raw",
		"multiIf(le_raw IN ['+Inf', 'Inf', '+inf', 'inf'], inf",
		"sum(cumulative_count) AS cumulative_count",
		"arraySort(item -> item.1, groupArray((upper_bound, cumulative_count))) AS buckets",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected classic histogram instant SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
	if got := rendered.QueryParams["param_hist_instant_matcher_0_value"]; got != "http_request_duration_seconds_bucket" {
		t.Fatalf("expected namespaced histogram selector param, got %#v", rendered.QueryParams)
	}
}

func TestRenderClassicHistogramGroupsQueryBuildsRangeMaterializationSQL(t *testing.T) {
	fragment, err := renderClassicHistogramGroupsQuery(storage.QueryConfig{Database: "observability", Table: "prometheus"}, &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le", "job"}, Source: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "http_request_duration_seconds_bucket", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000}, "hist")
	if err != nil {
		t.Fatalf("expected classic histogram range materialization SQL, got error: %v", err)
	}
	rendered, err := finalizeRenderedFragment(fragment)
	if err != nil {
		t.Fatalf("expected finalized classic histogram range SQL, got error: %v", err)
	}
	checks := []string{
		"ARRAY JOIN histogram_series.time_series AS point",
		"point.1 AS timestamp",
		"ifNull(toFloat64(point.2), nan) AS cumulative_count",
		"GROUP BY histogram_tags, timestamp, upper_bound",
		"arraySort(item -> item.1, groupArray((upper_bound, cumulative_count))) AS buckets",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected classic histogram range SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
	if got := rendered.QueryParams["param_hist_range_instant_matcher_0_value"]; got != "http_request_duration_seconds_bucket" {
		t.Fatalf("expected namespaced histogram range selector param, got %#v", rendered.QueryParams)
	}
}

func TestRenderFragmentBuildsHistogramProjectionSQL(t *testing.T) {
	testCases := []struct {
		name        string
		wantInstant string
	}{
		{name: "histogram_count", wantInstant: "arrayMax(arrayFilter(v -> NOT isNaN(v)"},
		{name: "histogram_sum", wantInstant: "arraySum(arrayMap((delta, midpoint) -> delta * midpoint"},
		{name: "histogram_avg", wantInstant: ") / (if(length(buckets) < 2"},
		{name: "histogram_stdvar", wantInstant: "toFloat64(ifNull(delta * pow(midpoint - ("},
		{name: "histogram_stddev", wantInstant: "sqrt(if(isNaN("},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fragment := &native.NativeFragment{Kind: native.FragmentKindHistogramProjection, OutputKind: native.OutputKindInstantVector, HistogramProjection: &native.HistogramProjectionFragment{Func: tc.name, Child: &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le", "job"}, Source: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "http_request_duration_seconds_bucket", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}

			instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
			if err != nil {
				t.Fatalf("expected histogram projection instant SQL, got error: %v", err)
			}
			instantChecks := []string{tc.wantInstant, "NOT isInfinite(tupleElement(arrayElement(buckets, length(buckets)), 1))", "AS value"}
			for _, check := range instantChecks {
				if !strings.Contains(sqlb.NormalizeSQL(instant.SQL), sqlb.NormalizeSQL(check)) {
					t.Fatalf("expected histogram projection instant SQL to contain %q, got %q", check, instant.SQL)
				}
			}

			rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
			if err != nil {
				t.Fatalf("expected histogram projection range SQL, got error: %v", err)
			}
			rangeChecks := []string{"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series", "GROUP BY tags ORDER BY tags"}
			for _, check := range rangeChecks {
				if !strings.Contains(sqlb.NormalizeSQL(rangeRendered.SQL), sqlb.NormalizeSQL(check)) {
					t.Fatalf("expected histogram projection range SQL to contain %q, got %q", check, rangeRendered.SQL)
				}
			}
		})
	}
}

func TestRenderFragmentBuildsHistogramQuantilesSQL(t *testing.T) {
	q1, q2 := 0.5, 0.9
	fragment := &native.NativeFragment{Kind: native.FragmentKindHistogramFunction, OutputKind: native.OutputKindInstantVector, HistogramFunction: &native.HistogramFunctionFragment{Func: "histogram_quantiles", Label: "quantile", Quantiles: []*native.NativeFragment{{Kind: native.FragmentKindSyntheticSeries, OutputKind: native.OutputKindScalar, Synthetic: &native.SyntheticSeriesFragment{Func: "literal", Value: &q1}}, {Kind: native.FragmentKindSyntheticSeries, OutputKind: native.OutputKindScalar, Synthetic: &native.SyntheticSeriesFragment{Func: "literal", Value: &q2}}}, Child: &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le", "job"}, Source: &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "rate", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "http_request_duration_seconds_bucket", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}}}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected histogram quantiles SQL, got error: %v", err)
	}
	checks := []string{"UNION ALL", "tuple('quantile'", "0.5", "0.9", "groupArray((timestamp, value))"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected histogram_quantiles SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsHistogramQuantileSQL(t *testing.T) {
	quantile := 0.9
	fragment := &native.NativeFragment{Kind: native.FragmentKindHistogramFunction, OutputKind: native.OutputKindInstantVector, HistogramFunction: &native.HistogramFunctionFragment{Func: "histogram_quantile", Quantile: &quantile, Child: &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le", "job"}, Source: &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "rate", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "http_request_duration_seconds_bucket", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}}}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected histogram quantile SQL, got error: %v", err)
	}
	checks := []string{"arrayCumSum", "arrayFirstIndex", "tupleElement(arrayElement(buckets, length(buckets)), 1)", "histogram_bucket_materialization_boundary"}
	for _, check := range checks[:3] {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected histogram_quantile SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsRangeTopKOverHistogramQuantileWithTaggedHistogramSource(t *testing.T) {
	quantile := 0.9
	fragment := &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.TOPK, ParamNumber: func() *float64 { v := 2.0; return &v }(), Source: &native.NativeFragment{Kind: native.FragmentKindHistogramFunction, OutputKind: native.OutputKindInstantVector, HistogramFunction: &native.HistogramFunctionFragment{Func: "histogram_quantile", Quantile: &quantile, Child: &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le"}, Source: &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "rate", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "http_request_duration_seconds_bucket", Lookback: time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}}}}}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -60000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected topk-over-histogram range SQL, got error: %v", err)
	}
	if strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL("CAST([], 'Array(Tuple(String, String))') AS tags")) {
		t.Fatalf("expected histogram source tags to be preserved for topk wrapper, got %q", rendered.SQL)
	}
	for _, check := range []string{"arrayConcat([tuple('__name__', src.metric_name)]", "series.tags AS tags", "tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2)", "row_number() OVER (PARTITION BY grouping_tags, timestamp ORDER BY isNaN(value) ASC, value DESC, tags ASC)"} {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected topk-over-histogram SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentWrapsZeroFillAggregationOverOrVectorZero(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, EmitZeroOnEmpty: true, Source: &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "rate", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}

	instantRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected zero-fill instant aggregation SQL, got error: %v", err)
	}
	for _, check := range []string{"UNION ALL", "CAST([], 'Array(Tuple(String, String))') AS tags", "fromUnixTimestamp64Milli({zero_fill_evaluation_ms:Int64}) AS timestamp", "WHERE NOT EXISTS (SELECT 1 FROM ("} {
		if !strings.Contains(sqlb.NormalizeSQL(instantRendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected zero-fill instant SQL to contain %q, got %q", check, instantRendered.SQL)
		}
	}

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected zero-fill range aggregation SQL, got error: %v", err)
	}
	for _, check := range []string{"UNION ALL", "CAST([], 'Array(Tuple(String, String))') AS tags", "LEFT JOIN (SELECT point.1 AS timestamp, count() AS sample_count", "buildTimestampGridSQL"} {
		needle := check
		if check == "buildTimestampGridSQL" {
			needle = "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS timestamp"
		}
		if !strings.Contains(sqlb.NormalizeSQL(rangeRendered.SQL), sqlb.NormalizeSQL(needle)) {
			t.Fatalf("expected zero-fill range SQL to contain %q, got %q", needle, rangeRendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsHistogramFractionSQL(t *testing.T) {
	lower, upper := 0.0, 1.0
	fragment := &native.NativeFragment{Kind: native.FragmentKindHistogramFunction, OutputKind: native.OutputKindInstantVector, HistogramFunction: &native.HistogramFunctionFragment{Func: "histogram_fraction", Lower: &lower, Upper: &upper, Child: &native.NativeFragment{Kind: native.FragmentKindAggregation, OutputKind: native.OutputKindInstantVector, Aggregation: &native.AggregationFragment{Op: parser.SUM, Grouping: []string{"le", "job"}, Source: &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "rate", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "http_request_duration_seconds_bucket", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}}}}}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected histogram fraction SQL, got error: %v", err)
	}
	checks := []string{"arrayCumSum", "arrayFirstIndex", "/ (arrayElement(arrayCumSum", "multiIf(length(buckets) < 2"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected histogram_fraction SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsAbsentSQL(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindAbsent, OutputKind: native.OutputKindInstantVector, Absent: &native.AbsentFragment{Func: "absent", OutputMetric: map[string]string{"job": "api"}, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindInstantVector, Selector: &native.SelectorSource{Kind: native.SelectorKindInstantVector, MetricName: "up", Lookback: native.DefaultInstantSelectorLookback}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}

	instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: 0, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected absent instant SQL, got error: %v", err)
	}
	instantChecks := []string{"count() AS sample_count", "WHERE probe.sample_count = 0", "fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp", "tuple('job', 'api')"}
	for _, check := range instantChecks {
		if !strings.Contains(sqlb.NormalizeSQL(instant.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected absent instant SQL to contain %q, got %q", check, instant.SQL)
		}
	}

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 120000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 120000})
	if err != nil {
		t.Fatalf("expected absent range SQL, got error: %v", err)
	}
	rangeChecks := []string{"ARRAY JOIN absent_child.time_series AS point", "ifNull(present.sample_count, 0) = 0", "groupArray((timestamp, value))", "tuple('job', 'api')"}
	for _, check := range rangeChecks {
		if !strings.Contains(sqlb.NormalizeSQL(rangeRendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected absent range SQL to contain %q, got %q", check, rangeRendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsAbsentOverTimeSQL(t *testing.T) {
	fragment := &native.NativeFragment{Kind: native.FragmentKindAbsent, OutputKind: native.OutputKindInstantVector, Absent: &native.AbsentFragment{Func: "absent_over_time", OutputMetric: map[string]string{"job": "api"}, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}

	instant, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 123456, RequiredStartMS: -300000, RequiredEndMS: 123456})
	if err != nil {
		t.Fatalf("expected absent_over_time instant SQL, got error: %v", err)
	}
	instantChecks := []string{"ARRAY JOIN absent_child.time_series AS point", "count() AS sample_count", "WHERE probe.sample_count = 0"}
	for _, check := range instantChecks {
		if !strings.Contains(sqlb.NormalizeSQL(instant.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected absent_over_time instant SQL to contain %q, got %q", check, instant.SQL)
		}
	}

	rangeRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected absent_over_time range SQL, got error: %v", err)
	}
	rangeChecks := []string{"sum(length(window_series)) AS sample_count", "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts", "ifNull(present.sample_count, 0) = 0", "tuple('job', 'api')"}
	for _, check := range rangeChecks {
		if !strings.Contains(sqlb.NormalizeSQL(rangeRendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected absent_over_time range SQL to contain %q, got %q", check, rangeRendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsTier1RangeFunctionSQL(t *testing.T) {
	testCases := []struct {
		name string
		want string
	}{
		{name: "first_over_time", want: "arrayElement(window_series, 1)"},
		{name: "stddev_over_time", want: "arrayReduce('stddevPop'"},
		{name: "stdvar_over_time", want: "arrayReduce('varPop'"},
		{name: "present_over_time", want: "toFloat64(1)"},
		{name: "resets", want: "arrayPopFront"},
		{name: "ts_of_first_over_time", want: "toUnixTimestamp64Milli(arrayElement(window_timestamps, 1))"},
		{name: "ts_of_last_over_time", want: "toUnixTimestamp64Milli(arrayElement(window_timestamps, length(window_series)))"},
		{name: "ts_of_max_over_time", want: "arrayFold((acc, ts, v) -> if((v >= tupleElement(acc, 1)) OR isNaN(tupleElement(acc, 1))"},
		{name: "ts_of_min_over_time", want: "arrayFold((acc, ts, v) -> if((v <= tupleElement(acc, 1)) OR isNaN(tupleElement(acc, 1))"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fragment := &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: tc.name, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: 0, RequiredEndMS: 300000})
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
	fragment := &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "mad_over_time", Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	checks := []string{"arrayMap(x -> abs(x - (multiIf(", "arrayConcat(arrayFilter(v -> isNaN(v)", "floor((0.5) * (toFloat64(length(", "window_values"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected mad_over_time SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsQuantileOverTimeSQL(t *testing.T) {
	quantile := 0.95
	fragment := &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "quantile_over_time", ParamNumber: &quantile, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	checks := []string{"arrayConcat(arrayFilter(v -> isNaN(v)", "arraySort(arrayFilter(v -> NOT isNaN(v)", "floor((0.95)", "multiIf"}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(rendered.SQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected quantile_over_time SQL to contain %q, got %q", check, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsPredictLinearSQL(t *testing.T) {
	duration := 60.0
	fragment := &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "predict_linear", ParamNumber: &duration, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
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
	fragment := &native.NativeFragment{Kind: native.FragmentKindRangeFunction, OutputKind: native.OutputKindInstantVector, RangeFunction: &native.RangeFunctionFragment{Func: "double_exponential_smoothing", ParamNumbers: []*float64{&sf, &tf}, Child: &native.NativeFragment{Kind: native.FragmentKindLeafSource, OutputKind: native.OutputKindRangeMatrix, Selector: &native.SelectorSource{Kind: native.SelectorKindRangeVector, MetricName: "up", Lookback: 5 * time.Minute}, ValueExpr: "{value}", TagsExpr: "{tags}"}}}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000, RequiredStartMS: -300000, RequiredEndMS: 300000})
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
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindAggregation,
		OutputKind: native.OutputKindInstantVector,
		Aggregation: &native.AggregationFragment{
			Op:       parser.SUM,
			Grouping: []string{"job"},
			Source: &native.NativeFragment{
				Kind:       native.FragmentKindBinaryScalarSourceExpr,
				OutputKind: native.OutputKindInstantVector,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindInstantVector,
					MetricName: "up",
					Lookback:   native.DefaultInstantSelectorLookback,
				},
				ValueExpr:   "({value}) * 100",
				TagsExpr:    "arrayFilter(tag -> tag.1 != '__name__', {tags})",
				DropsMetric: true,
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
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

func TestRenderFragmentBuildsCountValuesAggregationSQLWithFullTags(t *testing.T) {
	cases := []struct {
		name         string
		mode         native.RenderMode
		outputKind   native.OutputKind
		selectorKind native.SelectorKind
		params       RenderParams
	}{
		{
			name:         "instant",
			mode:         native.RenderModeInstant,
			outputKind:   native.OutputKindInstantVector,
			selectorKind: native.SelectorKindInstantVector,
			params: RenderParams{
				Mode:             native.RenderModeInstant,
				EvaluationTimeMS: 123456,
				RequiredStartMS:  0,
				RequiredEndMS:    123456,
			},
		},
		{
			name:         "range",
			mode:         native.RenderModeRange,
			outputKind:   native.OutputKindInstantVector,
			selectorKind: native.SelectorKindInstantVector,
			params: RenderParams{
				Mode:            native.RenderModeRange,
				StartMS:         0,
				EndMS:           300000,
				StepMS:          60000,
				RequiredStartMS: 0,
				RequiredEndMS:   300000,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fragment := &native.NativeFragment{
				Kind:       native.FragmentKindAggregation,
				OutputKind: tc.outputKind,
				Aggregation: &native.AggregationFragment{
					Op:          parser.COUNT_VALUES,
					ParamString: "sample_value",
					Source: &native.NativeFragment{
						Kind:       native.FragmentKindLeafSource,
						OutputKind: tc.outputKind,
						Selector: &native.SelectorSource{
							Kind:       tc.selectorKind,
							MetricName: "up",
							Lookback:   native.DefaultInstantSelectorLookback,
						},
						ValueExpr: "{value}",
						TagsExpr:  "{tags}",
					},
				},
			}

			rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, tc.params)
			if err != nil {
				t.Fatalf("expected rendered SQL, got error: %v", err)
			}
			normalized := sqlb.NormalizeSQL(rendered.SQL)
			if !strings.Contains(normalized, sqlb.NormalizeSQL("tuple('sample_value'")) {
				t.Fatalf("expected count_values label synthesis in SQL, got %q", rendered.SQL)
			}
			if strings.Contains(normalized, sqlb.NormalizeSQL("CAST([], 'Array(Tuple(String, String))') AS tags")) {
				t.Fatalf("expected count_values renderer to force full tags instead of the empty-tags fast path, got %q", rendered.SQL)
			}
		})
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
