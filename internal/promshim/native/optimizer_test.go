package native

import (
	"strings"
	"testing"

	planpkg "ch-observability/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildOptimizedFragmentRecordsMandatoryPassOutputs(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum by (job) (up{namespace="prod",job=~"api|worker"} offset 90s)`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &planpkg.LogicalAggregationPlan{
		Expr:     agg,
		Op:       agg.Op,
		Grouping: append([]string(nil), agg.Grouping...),
		Child:    &planpkg.LogicalLeafExprPlan{Expr: agg.Expr},
	}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RulesApplied, optimizerPassNames(FixedPassOrder); !equalStrings(got, want) {
		t.Fatalf("expected fixed pass order %v, got %v", want, got)
	}
	assertContainsAll(t, optimized.Report.InferredPredicates, []string{`__name__="up"`})
	assertContainsAll(t, optimized.Report.PushedPredicates, []string{`__name__="up"`, `namespace="prod"`, `job=~"api|worker"`})
	assertContainsAll(t, optimized.Report.RequiredColumns, []string{"tags", "value"})
	assertContainsAll(t, optimized.Report.MaterializedColumns, []string{"value"})
	assertContainsAll(t, optimized.Report.SemanticBarriers, []string{"aggregation_boundary", "evaluation_range", "late_tag_materialization"})
	assertContainsAll(t, optimized.Report.FunctionCatalog, []string{"rate", "increase", "sum(rate(...))"})
	if optimized.Report.JoinNormalization != "not_applicable" {
		t.Fatalf("expected join normalization to be a guarded no-op, got %#v", optimized.Report.JoinNormalization)
	}
	if optimized.Report.RequiredInputStartMS != -90000 || optimized.Report.RequiredInputEndMS != 210000 {
		t.Fatalf("expected evaluation-range propagation to compute [-90000,210000], got start=%d end=%d", optimized.Report.RequiredInputStartMS, optimized.Report.RequiredInputEndMS)
	}
}

func TestBuildOptimizedFragmentUsesFullSubqueryTemporalBounds(t *testing.T) {
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

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 12 * 60 * 1000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputStartMS, int64(60000); got != want {
		t.Fatalf("expected required input start %d, got %d (report=%#v)", want, got, optimized.Report)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(660000); got != want {
		t.Fatalf("expected required input end %d, got %d (report=%#v)", want, got, optimized.Report)
	}
}

func TestBuildOptimizedFragmentUsesFixedSelectorTimestampForInstantBounds(t *testing.T) {
	expr := mustParseExpr(t, `up @ 123`)
	leaf, ok := expr.(*parser.VectorSelector)
	if !ok {
		t.Fatalf("expected vector selector, got %T", expr)
	}
	if leaf.Timestamp == nil {
		t.Fatal("expected fixed selector timestamp")
	}
	logical := &planpkg.LogicalLeafExprPlan{Expr: leaf}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 999999})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputEndMS, *leaf.Timestamp; got != want {
		t.Fatalf("expected fixed selector end bound %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputStartMS, *leaf.Timestamp-DefaultInstantSelectorLookback.Milliseconds(); got != want {
		t.Fatalf("expected fixed selector start bound %d, got %d", want, got)
	}
}

func TestBuildOptimizedFragmentUsesFixedSubqueryTimestampForInstantBounds(t *testing.T) {
	subqueryExpr := mustParseExpr(t, `(up * 100)[5m:1m] @ 300 offset 1m`)
	subquery, ok := subqueryExpr.(*parser.SubqueryExpr)
	if !ok {
		t.Fatalf("expected subquery expr, got %T", subqueryExpr)
	}
	if subquery.Timestamp == nil {
		t.Fatal("expected fixed subquery timestamp")
	}
	innerExpr := subquery.Expr
	if paren, ok := innerExpr.(*parser.ParenExpr); ok {
		innerExpr = paren.Expr
	}
	binaryExpr, ok := innerExpr.(*parser.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary child, got %T", subquery.Expr)
	}
	scalarExpr, ok := binaryExpr.RHS.(*parser.NumberLiteral)
	if !ok {
		t.Fatalf("expected scalar rhs, got %T", binaryExpr.RHS)
	}
	logical := &planpkg.LogicalSubqueryPlan{
		Expr:       subquery,
		Range:      subquery.Range,
		Step:       subquery.Step,
		Offset:     subquery.OriginalOffset,
		Timestamp:  cloneInt64Pointer(subquery.Timestamp),
		StartOrEnd: subquery.StartOrEnd,
		Child: &planpkg.LogicalBinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &planpkg.LogicalLeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &planpkg.LogicalScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 999999})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(240000); got != want {
		t.Fatalf("expected fixed subquery end bound %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputStartMS, int64(-360000); got != want {
		t.Fatalf("expected fixed subquery start bound %d, got %d", want, got)
	}
}

func TestBuildOptimizedFragmentUsesSubqueryStartEndAnchorForInstantBounds(t *testing.T) {
	testCases := []struct {
		name      string
		query     string
		eval      int64
		wantStart int64
		wantEnd   int64
	}{
		{name: "start", query: `(up * 100)[5m:1m] @ start()`, eval: 420000, wantStart: -180000, wantEnd: 420000},
		{name: "end", query: `(up * 100)[5m:1m] @ end()`, eval: 420000, wantStart: -180000, wantEnd: 420000},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subqueryExpr := mustParseExpr(t, tc.query)
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
				t.Fatalf("expected binary child, got %T", subquery.Expr)
			}
			scalarExpr, ok := binaryExpr.RHS.(*parser.NumberLiteral)
			if !ok {
				t.Fatalf("expected scalar rhs, got %T", binaryExpr.RHS)
			}
			logical := &planpkg.LogicalSubqueryPlan{
				Expr:       subquery,
				Range:      subquery.Range,
				Step:       subquery.Step,
				Offset:     subquery.OriginalOffset,
				Timestamp:  cloneInt64Pointer(subquery.Timestamp),
				StartOrEnd: subquery.StartOrEnd,
				Child: &planpkg.LogicalBinaryPlan{
					Expr: binaryExpr,
					Op:   binaryExpr.Op,
					LHS:  &planpkg.LogicalLeafExprPlan{Expr: binaryExpr.LHS},
					RHS:  &planpkg.LogicalScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
				},
			}

			optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: tc.eval})
			if err != nil {
				t.Fatalf("expected optimized fragment, got error: %v", err)
			}
			if got := optimized.Report.RequiredInputEndMS; got != tc.wantEnd {
				t.Fatalf("expected anchored subquery end %d, got %d", tc.wantEnd, got)
			}
			if got := optimized.Report.RequiredInputStartMS; got != tc.wantStart {
				t.Fatalf("expected anchored subquery start %d, got %d", tc.wantStart, got)
			}
		})
	}
}

func TestBuildOptimizedFragmentUsesNestedSubqueryAnchorForInstantRangeFunctionBounds(t *testing.T) {
	subqueryExpr := mustParseExpr(t, `(up * 100)[5m:1m] @ 300 offset 1m`)
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
		t.Fatalf("expected binary child, got %T", subquery.Expr)
	}
	scalarExpr, ok := binaryExpr.RHS.(*parser.NumberLiteral)
	if !ok {
		t.Fatalf("expected scalar rhs, got %T", binaryExpr.RHS)
	}
	child := &planpkg.LogicalSubqueryPlan{
		Expr:       subquery,
		Range:      subquery.Range,
		Step:       subquery.Step,
		Offset:     subquery.OriginalOffset,
		Timestamp:  cloneInt64Pointer(subquery.Timestamp),
		StartOrEnd: subquery.StartOrEnd,
		Child: &planpkg.LogicalBinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &planpkg.LogicalLeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &planpkg.LogicalScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}
	callExpr := mustParseExpr(t, `sum_over_time((up * 100)[5m:1m] @ 300 offset 1m)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &planpkg.LogicalRangeFunctionPlan{Expr: call, Func: "sum_over_time", Child: child}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 999999})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(240000); got != want {
		t.Fatalf("expected nested anchored end %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputStartMS, int64(-360000); got != want {
		t.Fatalf("expected nested anchored start %d, got %d", want, got)
	}
}

func TestBuildOptimizedFragmentUsesRangeLookbackEnvelopeForLeafSelector(t *testing.T) {
	logical := &planpkg.LogicalLeafExprPlan{Expr: mustParseExpr(t, `up`)}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 30000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputStartMS, -DefaultInstantSelectorLookback.Milliseconds(); got != want {
		t.Fatalf("expected range lookback envelope start %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(300000); got != want {
		t.Fatalf("expected range lookback envelope end %d, got %d", want, got)
	}
}

func TestBuildOptimizedFragmentMarksSubqueryStepGridSemanticBarrier(t *testing.T) {
	subqueryExpr := mustParseExpr(t, `(up * 100)[5m:1m]`)
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
		t.Fatalf("expected binary child, got %T", subquery.Expr)
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

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	assertContainsAll(t, optimized.Report.SemanticBarriers, []string{"subquery_step_grid", "evaluation_range"})
}

func TestOptimizeFragmentFlattensTrivialUnaryWrapper(t *testing.T) {
	fragment := &NativeFragment{
		Kind:         FragmentKindUnarySourceExpr,
		OutputKind:   OutputKindInstantVector,
		SourcePromQL: mustParseExpr(t, `up`),
		ValueExpr:    "{value}",
		TagsExpr:     "{tags}",
	}

	optimized, err := OptimizeFragment(fragment, nil, OptimizationContext{})
	if err != nil {
		t.Fatalf("expected optimizer to succeed, got error: %v", err)
	}
	if optimized.Fragment.Kind != FragmentKindLeafSource {
		t.Fatalf("expected trivial unary wrapper to flatten to leaf source, got %#v", optimized.Fragment)
	}
}

func TestApplyRenderedSQLMetadataAddsRenderedColumnsAndRejectsSelectStar(t *testing.T) {
	report := &OptimizationReport{MaterializedColumns: []string{"value"}}
	if err := ApplyRenderedSQLMetadata(report, RenderModeRange, "SELECT 1"); err != nil {
		t.Fatalf("expected rendered SQL to be accepted, got error: %v", err)
	}
	if report.RenderedSQL != "SELECT 1" {
		t.Fatalf("expected rendered SQL to be recorded, got %q", report.RenderedSQL)
	}
	assertContainsAll(t, report.MaterializedColumns, []string{"tags", "value", "time_series"})

	err := ApplyRenderedSQLMetadata(&OptimizationReport{}, RenderModeInstant, "SELECT * FROM foo")
	if err == nil {
		t.Fatal("expected SELECT * to be rejected for native final SQL shaping")
	}
	if !strings.Contains(err.Error(), "SELECT *") {
		t.Fatalf("expected SELECT * rejection, got %v", err)
	}
}

func optimizerPassNames(passes []OptimizerPass) []string {
	names := make([]string, 0, len(passes))
	for _, pass := range passes {
		names = append(names, string(pass))
	}
	return names
}

func assertContainsAll(t *testing.T, got []string, want []string) {
	t.Helper()
	for _, expected := range want {
		found := false
		for _, actual := range got {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %q in %v", expected, got)
		}
	}
}

func TestOptimizeFragmentDeduplicatesInferredMetricMatcherInPushdown(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum(up{job="api"})`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &planpkg.LogicalAggregationPlan{Expr: agg, Op: agg.Op, Child: &planpkg.LogicalLeafExprPlan{Expr: agg.Expr}}
	optimized, err := BuildOptimizedFragment(logical, nil)
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	count := 0
	for _, predicate := range optimized.Report.PushedPredicates {
		if predicate == `__name__="up"` {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected deduplicated pushed metric matcher, got count=%d predicates=%#v", count, optimized.Report.PushedPredicates)
	}
}

func equalStrings(lhs, rhs []string) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	for i := range lhs {
		if lhs[i] != rhs[i] {
			return false
		}
	}
	return true
}
