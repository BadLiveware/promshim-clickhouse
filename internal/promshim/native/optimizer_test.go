package native

import (
	"strings"
	"testing"

	planpkg "ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
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

func TestOptimizeFragmentPushesProjectionIntoUngroupedAggregationSelector(t *testing.T) {
	aggExpr := mustParseExpr(t, `sum(up)`)
	agg, ok := aggExpr.(*parser.AggregateExpr)
	if !ok {
		t.Fatalf("expected aggregate expr, got %T", aggExpr)
	}
	logical := &planpkg.LogicalAggregationPlan{
		Expr:  agg,
		Op:    agg.Op,
		Child: &planpkg.LogicalLeafExprPlan{Expr: agg.Expr},
	}

	optimized, err := BuildOptimizedFragmentWithContext(logical, nil, OptimizationContext{Mode: RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	selector := baseSelectorSource(optimized.Fragment)
	if selector == nil {
		t.Fatalf("expected selector source, got %#v", optimized.Fragment)
	}
	if selector.RequireFullTags || len(selector.RequiredTagLabels) != 0 {
		t.Fatalf("expected ungrouped aggregation to avoid tag materialization, got %#v", selector)
	}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, optimized.Fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if strings.Contains(rendered.SQL, "arrayConcat([tuple('__name__', metric_name)]") || strings.Contains(rendered.SQL, "series.tags AS tags") {
		t.Fatalf("expected projection pushdown to avoid full tag materialization, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "CAST([], 'Array(Tuple(String, String))') AS tags") {
		t.Fatalf("expected ungrouped aggregation to synthesize empty tags, got %q", rendered.SQL)
	}
}

func TestOptimizeFragmentFlattensIdentityWrapperInRenderedSQL(t *testing.T) {
	fragment := &NativeFragment{
		Kind:       FragmentKindUnarySourceExpr,
		OutputKind: OutputKindInstantVector,
		Selector: &SelectorSource{
			Kind:            SelectorKindInstantVector,
			MetricName:      "up",
			RequireFullTags: true,
			Lookback:        defaultInstantSelectorLookback,
		},
		ValueExpr: "{value}",
		TagsExpr:  "{tags}",
	}
	raw, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected raw rendered SQL, got error: %v", err)
	}
	optimized, err := OptimizeFragment(fragment, nil, OptimizationContext{})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	optimizedRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, optimized.Fragment, RenderParams{Mode: RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized rendered SQL, got error: %v", err)
	}
	if raw.SQL != optimizedRendered.SQL {
		t.Fatalf("expected normalized identity wrapper to render identically, got raw=%q optimized=%q", raw.SQL, optimizedRendered.SQL)
	}
	if strings.Contains(optimizedRendered.SQL, "FROM (\n    SELECT\n        series.tags AS tags") {
		t.Fatalf("expected redundant wrapper to disappear, got %q", optimizedRendered.SQL)
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
