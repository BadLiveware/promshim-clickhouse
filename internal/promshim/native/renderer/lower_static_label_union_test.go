package renderer

import (
	"strings"
	"testing"
)

func TestLowerStaticLabelUnionSharesInstantChild(t *testing.T) {
	query := `label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(rate(up[5m]), "__name__", "rule_b", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "ARRAY JOIN [tuple(CAST([tuple('__name__', 'rule_a')]") || !strings.Contains(rq.SQL, "tuple(CAST([tuple('__name__', 'rule_b')]") {
		t.Fatalf("expected static label definitions in SQL:\n%s", rq.SQL)
	}
	if got := strings.Count(rq.SQL, "timeSeriesTags(`observability`.`prometheus`)"); got != 1 {
		t.Fatalf("timeSeriesTags count = %d, want one shared rate child selector; SQL:\n%s", got, rq.SQL)
	}
	if strings.Contains(rq.SQL, " AS lhs INNER JOIN ") || strings.Contains(rq.SQL, " AS rhs") {
		t.Fatalf("expected label fanout instead of binary OR join SQL:\n%s", rq.SQL)
	}
}

func TestLowerStaticLabelUnionSharesRangeChild(t *testing.T) {
	query := `label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(rate(up[5m]), "__name__", "rule_b", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "static_label_union_series") {
		t.Fatalf("expected static label range union wrapper in SQL:\n%s", rq.SQL)
	}
	if got := strings.Count(rq.SQL, "timeSeriesTags(`observability`.`prometheus`)"); got != 1 {
		t.Fatalf("timeSeriesTags count = %d, want one shared rate child selector; SQL:\n%s", got, rq.SQL)
	}
}

func TestLowerStaticLabelUnionCombinesDisjointDifferentChildren(t *testing.T) {
	query := `label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(rate(http_requests_total[5m]), "__name__", "rule_b", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "static_label_union_rows") || !strings.Contains(rq.SQL, " UNION ALL ") {
		t.Fatalf("expected static label disjoint union for different children:\n%s", rq.SQL)
	}
	if got := strings.Count(rq.SQL, "timeSeriesTags(`observability`.`prometheus`)"); got != 2 {
		t.Fatalf("timeSeriesTags count = %d, want one selector per distinct child; SQL:\n%s", got, rq.SQL)
	}
}

func TestLowerStaticLabelUnionKeepsDifferentLabelSetsSeparate(t *testing.T) {
	query := `label_replace(rate(up[5m]), "team", "alpha", "", ".*") or label_replace(rate(up[5m]), "namespace", "default", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if strings.Contains(rq.SQL, "static_label_union_rows") {
		t.Fatalf("did not expect static label union for different static label keys:\n%s", rq.SQL)
	}
}
