package native

import (
	"strings"
	"testing"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
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
