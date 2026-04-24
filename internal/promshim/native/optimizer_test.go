package native

import (
	"strings"
	"testing"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildOptimizedFragmentRecordsMandatoryPassOutputs(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum by (job) (up{namespace="prod",job=~"api|worker"} offset 90s)`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &logicalpkg.AggregationPlan{
		Expr:     agg,
		Op:       agg.Op,
		Grouping: append([]string(nil), agg.Grouping...),
		Child:    &logicalpkg.LeafExprPlan{Expr: agg.Expr},
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

func TestBuildOptimizedFragmentUsesSignedNegativeSelectorOffsetBounds(t *testing.T) {
	expr := mustParseExpr(t, `up offset -1m`)
	logical := &logicalpkg.LeafExprPlan{Expr: expr}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	selector := BaseSelectorSource(optimized.Fragment)
	if selector == nil {
		t.Fatal("expected base selector source")
	}
	if got, want := selector.Offset, -1*time.Minute; got != want {
		t.Fatalf("expected signed selector offset %s, got %s", want, got)
	}
	if got, want := optimized.Report.RequiredInputStartMS, int64(60000); got != want {
		t.Fatalf("expected required input start %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(360000); got != want {
		t.Fatalf("expected required input end %d, got %d", want, got)
	}
}

func TestBuildOptimizedFragmentPreservesNestedAggregationGroupingProjection(t *testing.T) {
	expr := mustParseExpr(t, `count(count by(instance) (up{job="api"}))`)
	outerAgg, ok := expr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected outer aggregate expr, got %T", expr)
	}
	innerAgg, ok := outerAgg.Expr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected inner aggregate expr, got %T", outerAgg.Expr)
	}
	logical := &logicalpkg.AggregationPlan{
		Expr: outerAgg,
		Op:   outerAgg.Op,
		Child: &logicalpkg.AggregationPlan{
			Expr:     innerAgg,
			Op:       innerAgg.Op,
			Grouping: append([]string(nil), innerAgg.Grouping...),
			Child:    &logicalpkg.LeafExprPlan{Expr: innerAgg.Expr},
		},
	}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 60000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	selector := BaseSelectorSource(optimized.Fragment)
	if selector == nil {
		t.Fatal("expected base selector source")
	}
	if selector.RequireFullTags {
		t.Fatalf("did not expect full-tag requirement, got %#v", selector)
	}
	if got := selector.RequiredTagLabels; len(got) != 1 || got[0] != "instance" {
		t.Fatalf("expected nested aggregation to preserve instance projection, got %#v", selector.RequiredTagLabels)
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
	logical := &logicalpkg.LeafExprPlan{Expr: leaf}

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
	logical := &logicalpkg.SubqueryPlan{
		Expr:       subquery,
		Range:      subquery.Range,
		Step:       subquery.Step,
		Offset:     subquery.OriginalOffset,
		Timestamp:  cloneInt64Pointer(subquery.Timestamp),
		StartOrEnd: subquery.StartOrEnd,
		Child: &logicalpkg.BinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &logicalpkg.LeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &logicalpkg.ScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
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
			logical := &logicalpkg.SubqueryPlan{
				Expr:       subquery,
				Range:      subquery.Range,
				Step:       subquery.Step,
				Offset:     subquery.OriginalOffset,
				Timestamp:  cloneInt64Pointer(subquery.Timestamp),
				StartOrEnd: subquery.StartOrEnd,
				Child: &logicalpkg.BinaryPlan{
					Expr: binaryExpr,
					Op:   binaryExpr.Op,
					LHS:  &logicalpkg.LeafExprPlan{Expr: binaryExpr.LHS},
					RHS:  &logicalpkg.ScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
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
	child := &logicalpkg.SubqueryPlan{
		Expr:       subquery,
		Range:      subquery.Range,
		Step:       subquery.Step,
		Offset:     subquery.OriginalOffset,
		Timestamp:  cloneInt64Pointer(subquery.Timestamp),
		StartOrEnd: subquery.StartOrEnd,
		Child: &logicalpkg.BinaryPlan{
			Expr: binaryExpr,
			Op:   binaryExpr.Op,
			LHS:  &logicalpkg.LeafExprPlan{Expr: binaryExpr.LHS},
			RHS:  &logicalpkg.ScalarLiteralPlan{Expr: scalarExpr, Value: scalarExpr.Val},
		},
	}
	callExpr := mustParseExpr(t, `sum_over_time((up * 100)[5m:1m] @ 300 offset 1m)`)
	call, ok := callExpr.(*parser.Call)
	if !ok {
		t.Fatalf("expected call expr, got %T", callExpr)
	}
	logical := &logicalpkg.RangeFunctionPlan{Expr: call, Func: "sum_over_time", Child: child}

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
	logical := &logicalpkg.LeafExprPlan{Expr: mustParseExpr(t, `up`)}

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

func TestBuildOptimizedFragmentUsesFixedSelectorTimestampForRangeBounds(t *testing.T) {
	logical := &logicalpkg.LeafExprPlan{Expr: mustParseExpr(t, `up @ 300`)}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeRange, StartMS: 0, EndMS: 600000, StepMS: 30000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(300000); got != want {
		t.Fatalf("expected anchored range end %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputStartMS, int64(0); got != want {
		t.Fatalf("expected anchored range start %d, got %d", want, got)
	}
}

func TestBuildOptimizedFragmentUsesRangeStartEndAnchorForRangeBounds(t *testing.T) {
	testCases := []struct {
		name      string
		query     string
		wantStart int64
		wantEnd   int64
	}{
		{name: "start", query: `up @ start()`, wantStart: -300000, wantEnd: 0},
		{name: "end", query: `up @ end()`, wantStart: 300000, wantEnd: 600000},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logical := &logicalpkg.LeafExprPlan{Expr: mustParseExpr(t, tc.query)}
			optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeRange, StartMS: 0, EndMS: 600000, StepMS: 30000})
			if err != nil {
				t.Fatalf("expected optimized fragment, got error: %v", err)
			}
			if got := optimized.Report.RequiredInputStartMS; got != tc.wantStart {
				t.Fatalf("expected anchored range start %d, got %d", tc.wantStart, got)
			}
			if got := optimized.Report.RequiredInputEndMS; got != tc.wantEnd {
				t.Fatalf("expected anchored range end %d, got %d", tc.wantEnd, got)
			}
		})
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

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	assertContainsAll(t, optimized.Report.SemanticBarriers, []string{"subquery_step_grid", "evaluation_range"})
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
	logical := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Child: &logicalpkg.LeafExprPlan{Expr: agg.Expr}}
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

func TestOptimizeFragmentInternsEquivalentMatchersAcrossSelectorFields(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum(up{job="api"})`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Child: &logicalpkg.LeafExprPlan{Expr: agg.Expr}}
	optimized, err := BuildOptimizedFragment(logical, nil)
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	selector := BaseSelectorSource(optimized.Fragment)
	if selector == nil {
		t.Fatalf("expected selector source, got %#v", optimized.Fragment)
	}
	selectorMetric := findMatcher(selector.Matchers, labels.MatchEqual, labels.MetricName, "up")
	inferredMetric := findMatcher(selector.InferredMatchers, labels.MatchEqual, labels.MetricName, "up")
	pushedMetric := findMatcher(selector.PushedMatchers, labels.MatchEqual, labels.MetricName, "up")
	if selectorMetric == nil || inferredMetric == nil || pushedMetric == nil {
		t.Fatalf("expected selector/inferred/pushed metric matchers, got selector=%#v inferred=%#v pushed=%#v", selector.Matchers, selector.InferredMatchers, selector.PushedMatchers)
	}
	if selectorMetric != inferredMetric || inferredMetric != pushedMetric {
		t.Fatalf("expected optimizer matcher interning to share identical pointers, got selector=%p inferred=%p pushed=%p", selectorMetric, inferredMetric, pushedMetric)
	}
}

func TestOptimizeFragmentMatcherInternerDoesNotLeakAcrossRuns(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindLeafSource,
		OutputKind: OutputKindInstantVector,
		Selector: &SelectorSource{
			Kind:       SelectorKindInstantVector,
			MetricName: "up",
			Matchers: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, "up"),
			},
			RequireFullTags: true,
			Lookback:        DefaultInstantSelectorLookback,
		},
		ValueExpr: "{value}",
		TagsExpr:  "{tags}",
	}

	optimizedA, err := OptimizeFragment(fragment, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected first optimize to succeed, got error: %v", err)
	}
	optimizedB, err := OptimizeFragment(fragment, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected second optimize to succeed, got error: %v", err)
	}
	matcherA := findMatcher(BaseSelectorSource(optimizedA.Fragment).Matchers, labels.MatchEqual, labels.MetricName, "up")
	matcherB := findMatcher(BaseSelectorSource(optimizedB.Fragment).Matchers, labels.MatchEqual, labels.MetricName, "up")
	if matcherA == nil || matcherB == nil {
		t.Fatalf("expected metric matchers in both optimize runs, got a=%#v b=%#v", BaseSelectorSource(optimizedA.Fragment), BaseSelectorSource(optimizedB.Fragment))
	}
	if matcherA == matcherB {
		t.Fatalf("expected per-run matcher interning, but both runs shared matcher pointer %p", matcherA)
	}
}

func BenchmarkOptimizeFragmentWide(b *testing.B) {
	fragment := &NativeFragment{
		Kind:       FragmentKindAggregation,
		OutputKind: OutputKindInstantVector,
		Aggregation: &AggregationFragment{
			Op:       parser.SUM,
			Grouping: []string{"job", "namespace"},
			Source: &NativeFragment{
				Kind:       FragmentKindLabelTransform,
				OutputKind: OutputKindInstantVector,
				LabelTransform: &LabelTransformFragment{
					Func:      "label_replace",
					Dst:       "service",
					Repl:      "$1",
					Regex:     "(.*)",
					Src:       "job",
					SrcLabels: []string{"job"},
					Child: &NativeFragment{
						Kind:       FragmentKindSortTransform,
						OutputKind: OutputKindInstantVector,
						SortTransform: &SortTransformFragment{
							Func: "sort_desc",
							Child: &NativeFragment{
								Kind:       FragmentKindRangeFunction,
								OutputKind: OutputKindInstantVector,
								RangeFunction: &RangeFunctionFragment{
									Func: "rate",
									Child: &NativeFragment{
										Kind:       FragmentKindLeafSource,
										OutputKind: OutputKindRangeMatrix,
										Selector: &SelectorSource{
											Kind:       SelectorKindRangeVector,
											MetricName: "http_requests_total",
											Matchers: []*labels.Matcher{
												labels.MustNewMatcher(labels.MatchEqual, "namespace", "prod"),
												labels.MustNewMatcher(labels.MatchRegexp, "job", "api|worker"),
											},
											RequireFullTags: true,
											Lookback:        5 * time.Minute,
										},
										ValueExpr: "{value}",
										TagsExpr:  "{tags}",
									},
								},
							},
						},
					},
				},
			},
		},
	}
	ctx := OptimizationContext{Mode: RenderModeRange, StartMS: 0, EndMS: 15 * 60 * 1000, StepMS: 30 * 1000}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		optimized, err := OptimizeFragment(fragment, nil, ctx)
		if err != nil {
			b.Fatalf("expected optimizer to succeed, got error: %v", err)
		}
		if optimized == nil || optimized.Fragment == nil {
			b.Fatal("expected optimized fragment result")
		}
	}
}

func findMatcher(matchers []*labels.Matcher, typ labels.MatchType, name, value string) *labels.Matcher {
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		if matcher.Type == typ && matcher.Name == name && matcher.Value == value {
			return matcher
		}
	}
	return nil
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
