package native

import (
	"strings"
	"testing"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// optimizeLogical drives the optimizer pass pipeline against a logical
// plan node via an Analyze + InfoFor lookup into OptimizeFromInfo.
func optimizeLogical(t *testing.T, node logicalpkg.Node, ctx OptimizationContext) (*OptimizedFragment, error) {
	t.Helper()
	analysis := Analyze(node)
	info := analysis.InfoFor(node)
	if info == nil {
		t.Fatalf("expected lowering info for %T", node)
	}
	return OptimizeFromInfo(info, node, analysis, ctx)
}

func TestOptimizeFromInfoRecordsMandatoryPassOutputs(t *testing.T) {
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

	optimized, err := optimizeLogical(t, logical, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RulesApplied, optimizerPassNames(FixedPassOrder); !equalStrings(got, want) {
		t.Fatalf("expected fixed pass order %v, got %v", want, got)
	}
	assertContainsAll(t, optimized.Report.InferredPredicates, []string{`__name__="up"`})
	assertContainsAll(t, optimized.Report.PushedPredicates, []string{`__name__="up"`, `namespace="prod"`, `job=~"api|worker"`})
	assertContainsAll(t, optimized.Report.RequiredColumns, []string{"value", "tags"})
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

func TestOptimizeFromInfoUsesSignedNegativeSelectorOffsetBounds(t *testing.T) {
	expr := mustParseExpr(t, `up offset -1m`)
	logical := &logicalpkg.LeafExprPlan{Expr: expr}

	optimized, err := optimizeLogical(t, logical, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	if got, want := optimized.Report.RequiredInputStartMS, int64(60000); got != want {
		t.Fatalf("expected required input start %d, got %d", want, got)
	}
	if got, want := optimized.Report.RequiredInputEndMS, int64(360000); got != want {
		t.Fatalf("expected required input end %d, got %d", want, got)
	}
}

func TestOptimizeFromInfoUsesRangeLookbackEnvelopeForLeafSelector(t *testing.T) {
	logical := &logicalpkg.LeafExprPlan{Expr: mustParseExpr(t, `up`)}

	optimized, err := optimizeLogical(t, logical, OptimizationContext{Mode: RenderModeRange, StartMS: 0, EndMS: 300000, StepMS: 30000})
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

func TestOptimizeFromInfoMarksSubqueryStepGridSemanticBarrier(t *testing.T) {
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

	optimized, err := optimizeLogical(t, logical, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
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

func TestOptimizeFromInfoDeduplicatesInferredMetricMatcherInPushdown(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum(up{job="api"})`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Child: &logicalpkg.LeafExprPlan{Expr: agg.Expr}}
	optimized, err := optimizeLogical(t, logical, OptimizationContext{})
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

func TestOptimizeFromInfoInternsEquivalentMatchersAcrossSelectorFields(t *testing.T) {
	// Analyze interns Matchers/InferredMatchers/PushedMatchers via the
	// per-leaf interner in populateSelectorInferredAndPushedMatchers.
	// The analyzed selector is looked up via baseSelectorFromInfo on the
	// root LoweringInfo (the info side-map carries the intern-shared
	// pointers).
	aggExpr := mustParseExpr(t, `sum(up{job="api"})`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Child: &logicalpkg.LeafExprPlan{Expr: agg.Expr}}
	analysis := Analyze(logical)
	info := analysis.InfoFor(logical)
	if info == nil {
		t.Fatalf("expected lowering info for %T", logical)
	}
	if _, err := OptimizeFromInfo(info, logical, analysis, OptimizationContext{}); err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	selector := baseSelectorFromInfo(info)
	if selector == nil {
		t.Fatalf("expected selector source, got info=%#v", info)
	}
	selectorMetric := findMatcher(selector.Matchers, labels.MatchEqual, labels.MetricName, "up")
	inferredMetric := findMatcher(selector.InferredMatchers, labels.MatchEqual, labels.MetricName, "up")
	pushedMetric := findMatcher(selector.PushedMatchers, labels.MatchEqual, labels.MetricName, "up")
	if selectorMetric == nil || inferredMetric == nil || pushedMetric == nil {
		t.Fatalf("expected selector/inferred/pushed metric matchers, got selector=%#v inferred=%#v pushed=%#v", selector.Matchers, selector.InferredMatchers, selector.PushedMatchers)
	}
	if selectorMetric != inferredMetric || inferredMetric != pushedMetric {
		t.Fatalf("expected Analyze-time matcher interning to share identical pointers, got selector=%p inferred=%p pushed=%p", selectorMetric, inferredMetric, pushedMetric)
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

var _ = time.Minute // reserved for future temporal-bound tests
