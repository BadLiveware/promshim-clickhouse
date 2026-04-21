package native

import (
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/model"
	planpkg "ch-observability/internal/promshim/plan"
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

	child := &planpkg.LogicalBinaryPlan{
		Expr: binaryExpr,
		Op:   binaryExpr.Op,
		LHS:  &planpkg.LogicalLeafExprPlan{Expr: binaryExpr.LHS},
		RHS:  &planpkg.LogicalScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
	}
	logical := &planpkg.LogicalAggregationPlan{
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
	logical := &planpkg.LogicalLabelJoinPlan{
		Expr:   call,
		Config: cfg,
		Child:  &planpkg.LogicalLeafExprPlan{Expr: call.Args[0]},
	}

	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if info.NativeLowerable {
		t.Fatalf("expected label_join to stay local, got %#v", info)
	}
	if got := info.LabelLineage.Known["joined"]; got != LabelLineageSynthetic {
		t.Fatalf("expected synthetic destination lineage, got %#v", info.LabelLineage)
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
	logical := &planpkg.LogicalRatePlan{
		Expr: call,
		Func: "rate",
		Child: &planpkg.LogicalSubqueryPlan{
			Expr:   subquery,
			Range:  subquery.Range,
			Step:   subquery.Step,
			Offset: subquery.OriginalOffset,
			Child: &planpkg.LogicalAggregationPlan{
				Expr:     aggExpr,
				Op:       aggExpr.Op,
				Grouping: append([]string(nil), aggExpr.Grouping...),
				Child:    &planpkg.LogicalLeafExprPlan{Expr: aggExpr.Expr},
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
		child := &planpkg.LogicalSubqueryPlan{
			Expr:   subquery,
			Range:  subquery.Range,
			Step:   subquery.Step,
			Offset: subquery.OriginalOffset,
			Child: &planpkg.LogicalAggregationPlan{
				Expr:     aggExpr,
				Op:       aggExpr.Op,
				Grouping: append([]string(nil), aggExpr.Grouping...),
				Child:    &planpkg.LogicalLeafExprPlan{Expr: aggExpr.Expr},
			},
		}
		var logical planpkg.LogicalPlan
		switch tc.kind {
		case "increase":
			logical = &planpkg.LogicalIncreasePlan{Expr: call, Child: child}
		default:
			logical = &planpkg.LogicalDeltaPlan{Expr: call, Func: tc.kind, Child: child}
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
		child := &planpkg.LogicalSubqueryPlan{
			Expr:   subquery,
			Range:  subquery.Range,
			Step:   subquery.Step,
			Offset: subquery.OriginalOffset,
			Child: &planpkg.LogicalAggregationPlan{
				Expr:     aggExpr,
				Op:       aggExpr.Op,
				Grouping: append([]string(nil), aggExpr.Grouping...),
				Child:    &planpkg.LogicalLeafExprPlan{Expr: aggExpr.Expr},
			},
		}
		var logical planpkg.LogicalPlan
		switch tc.kind {
		case "changes":
			logical = &planpkg.LogicalChangesPlan{Expr: call, Child: child}
		default:
			logical = &planpkg.LogicalDerivPlan{Expr: call, Child: child}
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

	logical := &planpkg.LogicalSubqueryPlan{
		Expr:   subquery,
		Range:  subquery.Range,
		Step:   subquery.Step,
		Offset: subquery.OriginalOffset,
		Child: &planpkg.LogicalBinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &planpkg.LogicalLeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &planpkg.LogicalScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}

	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if want := 6 * time.Minute; info.TimeRequirements.Lookback != want {
		t.Fatalf("expected lookback %s, got %s", want, info.TimeRequirements.Lookback)
	}
	if want := 1 * time.Minute; info.TimeRequirements.Offset != want {
		t.Fatalf("expected offset %s, got %s", want, info.TimeRequirements.Offset)
	}
	if !info.TimeRequirements.NeedsSubqueryStepGrid {
		t.Fatalf("expected subquery step-grid flag, got %#v", info.TimeRequirements)
	}
}

func TestAnalyzeLeafTracksOffsetLookback(t *testing.T) {
	expr := mustParseExpr(t, `up offset 90s`)
	leaf := &planpkg.LogicalLeafExprPlan{Expr: expr}

	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if want := 90 * time.Second; info.TimeRequirements.Lookback != want || info.TimeRequirements.Offset != want {
		t.Fatalf("expected 90s lookback+offset, got %#v", info.TimeRequirements)
	}
}

func mustParseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := planpkg.ParseExpression(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return expr
}
