package renderer

import (
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

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

	optimized, err := native.BuildOptimizedFragmentWithContext(logical, nil, native.OptimizationContext{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	selector := native.BaseSelectorSource(optimized.Fragment)
	if selector == nil {
		t.Fatalf("expected selector source, got %#v", optimized.Fragment)
	}
	if selector.RequireFullTags || len(selector.RequiredTagLabels) != 0 {
		t.Fatalf("expected ungrouped aggregation to avoid tag materialization, got %#v", selector)
	}
	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, optimized.Fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS})
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
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindUnarySourceExpr,
		OutputKind: native.OutputKindInstantVector,
		Selector: &native.SelectorSource{
			Kind:            native.SelectorKindInstantVector,
			MetricName:      "up",
			RequireFullTags: true,
			Lookback:        native.DefaultInstantSelectorLookback,
		},
		ValueExpr: "{value}",
		TagsExpr:  "{tags}",
	}
	raw, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
	if err != nil {
		t.Fatalf("expected raw rendered SQL, got error: %v", err)
	}
	optimized, err := native.OptimizeFragment(fragment, nil, native.OptimizationContext{})
	if err != nil {
		t.Fatalf("expected optimized fragment, got error: %v", err)
	}
	optimizedRendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, optimized.Fragment, RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 300000, RequiredStartMS: 0, RequiredEndMS: 300000})
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
