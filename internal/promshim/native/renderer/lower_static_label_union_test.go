package renderer

import (
	"strings"
	"testing"

	native "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/prometheus/prometheus/promql/parser"
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

func TestLowerStaticLabelUnionSharesSelectorVariantChildren(t *testing.T) {
	query := `label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="DaemonSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "daemonset", "", ".*") or label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="StatefulSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "statefulset", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesTags(`observability`.`prometheus`)"); got != 1 {
		t.Fatalf("timeSeriesTags count = %d, want one shared selector child for same-shape variants; SQL:\n%s", got, rq.SQL)
	}
	if !strings.Contains(rq.SQL, "static_label_union_rows") {
		t.Fatalf("expected static-label union wrapper for selector variants in SQL:\n%s", rq.SQL)
	}
}

func TestLowerStaticLabelUnionSharesSelectorVariantChildrenNestedOr(t *testing.T) {
	query := `label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="DaemonSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "daemonset", "", ".*") or (label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="StatefulSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "statefulset", "", ".*") or label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="Node", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "staticpod", "", ".*"))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesTags(`observability`.`prometheus`)"); got != 1 {
		t.Fatalf("timeSeriesTags count = %d, want one shared selector child for nested same-shape variants; SQL:\n%s", got, rq.SQL)
	}
	if got := strings.Count(rq.SQL, "UNION ALL"); got != 0 {
		t.Fatalf("unexpected UNION ALL in nested variant shared child SQL:\n%s", rq.SQL)
	}
}

func TestLowerStaticLabelUnionReportsSelectorVariantOptimization(t *testing.T) {
	query := `label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="DaemonSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "daemonset", "", ".*") or label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="StatefulSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "statefulset", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	_, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 1 {
		t.Fatalf("static label union decisions = %d, want 1", got)
	}
	if got := report.StaticLabelUnionDecisions[0]; !got.Applied || got.CandidateBranches != 2 || got.CollapsedRows != 1 || got.RemainingGroups != 2 || got.Mode != "shared_selector_child" {
		t.Fatalf("unexpected static label union decision: %#v", got)
	}
}

func TestLowerStaticLabelUnionReportsSelectorVariantOptimizationNestedOr(t *testing.T) {
	query := `label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="DaemonSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "daemonset", "", ".*") or (label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="StatefulSet", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "statefulset", "", ".*") or label_replace(rate(kube_pod_owner{job="kube-state-metrics", owner_kind="Node", namespace="default", workload_type="daemonset"}[5m]), "workload_type", "staticpod", "", ".*"))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	_, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 1 {
		t.Fatalf("static label union decisions = %d, want 1", got)
	}
	if got := report.StaticLabelUnionDecisions[0]; !got.Applied || got.CandidateBranches != 3 || got.CollapsedRows != 2 || got.RemainingGroups != 3 || got.Mode != "shared_selector_child" {
		t.Fatalf("unexpected static label union decision: %#v", got)
	}
}

func TestLowerStaticLabelUnionRejectsUnsafeSelectorOverlapNestedOr(t *testing.T) {
	query := `(label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(rate(up[5m]), "__name__", "rule_b", "", ".*")) or label_replace(rate(up[5m]), "__name__", "rule_b", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	_, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 2 {
		t.Fatalf("static label union decisions = %d, want 2 (outer and inner OR), sql=%s", got, query)
	}
	foundUnsafe := false
	for _, decision := range report.StaticLabelUnionDecisions {
		if decision.Applied {
			continue
		}
		if decision.SkipReason == staticLabelUnionSkipReasonUnsafeSelectorOverlap {
			foundUnsafe = true
		}
	}
	if !foundUnsafe {
		t.Fatalf("expected unsafe-overlap skip decision in nested OR, got %#v", report.StaticLabelUnionDecisions)
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

func TestLowerStaticLabelUnionReportsSharedSelectorOptimization(t *testing.T) {
	query := `label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(rate(up[5m]), "__name__", "rule_b", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 1 {
		t.Fatalf("static label union decisions = %d, want 1; sql=%s", got, rq.SQL)
	}
	if got := report.StaticLabelUnionDecisions[0]; !got.Applied || got.CandidateBranches != 2 || got.CollapsedRows != 1 || got.RemainingGroups != 2 || got.Mode != "shared_selector_child" {
		t.Fatalf("unexpected static label union decision: %#v", got)
	}
}

func TestLowerStaticLabelUnionReportsRejectedIncompatibleStaticLabels(t *testing.T) {
	query := `label_replace(rate(up[5m]), "team", "alpha", "", ".*") or label_replace(rate(up[5m]), "namespace", "default", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	_, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 1 {
		t.Fatalf("static label union decisions = %d, want 1", got)
	}
	if got := report.StaticLabelUnionDecisions[0]; got.Applied || got.SkipReason != staticLabelUnionSkipReasonIncompatibleStaticLabels || got.CandidateBranches != 2 {
		t.Fatalf("unexpected static label union decision: %#v", got)
	}
}

func TestLowerStaticLabelUnionReportsRejectedUnsafeSelectorOverlap(t *testing.T) {
	query := `label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	_, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 1 {
		t.Fatalf("static label union decisions = %d, want 1", got)
	}
	if got := report.StaticLabelUnionDecisions[0]; got.Applied || got.SkipReason != staticLabelUnionSkipReasonUnsafeSelectorOverlap || got.CandidateBranches != 2 {
		t.Fatalf("unexpected static label union decision: %#v", got)
	}
}

func TestLowerStaticLabelUnionReportsDisjointOptimization(t *testing.T) {
	query := `label_replace(rate(up[5m]), "__name__", "rule_a", "", ".*") or label_replace(sum(rate(up[5m])), "__name__", "rule_b", "", ".*")`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	report := &native.OptimizationReport{}
	_, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant(), OptimizationReport: report}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := len(report.StaticLabelUnionDecisions); got != 1 {
		t.Fatalf("static label union decisions = %d, want 1", got)
	}
	if got := report.StaticLabelUnionDecisions[0]; !got.Applied || got.CollapsedRows != 0 || got.Mode != "disjoint_children" {
		t.Fatalf("unexpected static label union decision: %#v", got)
	}
}

func TestStaticLabelUnionCanonicalBaseKeyMatchesExactSelectorVariants(t *testing.T) {
	left, _, _ := buildLowerInputs(t, `rate(kube_pod_owner{job="kube-state-metrics", owner_kind="DaemonSet", workload="api"}[5m])`)
	right, _, _ := buildLowerInputs(t, `rate(kube_pod_owner{job="kube-state-metrics", owner_kind="StatefulSet", workload="api"}[5m])`)
	if got, want := staticLabelUnionCanonicalBaseKey(left), staticLabelUnionCanonicalBaseKey(right); got != want {
		t.Fatalf("expected canonical base keys to match, got:\nleft=%q\nright=%q", got, want)
	}
	if got, want := staticLabelUnionBaseKey(left), staticLabelUnionBaseKey(right); got == want {
		t.Fatalf("expected raw base keys to differ for owner_kind variants, got shared value=%q", got)
	}
}

func TestStaticLabelUnionCanonicalBaseKeyRejectsRegexShapeMismatch(t *testing.T) {
	left, _, _ := buildLowerInputs(t, `rate(kube_pod_owner{job="kube-state-metrics", owner_kind=~"DaemonSet|DaemonSetSet"}[5m])`)
	right, _, _ := buildLowerInputs(t, `rate(kube_pod_owner{job="kube-state-metrics", owner_kind=~"StatefulSet|DaemonSet"}[5m])`)
	if got, want := staticLabelUnionCanonicalBaseKey(left), staticLabelUnionCanonicalBaseKey(right); got == want {
		t.Fatalf("expected canonical base keys to differ for regexp selector variants, got %q", got)
	}
}

func TestStaticLabelUnionCanonicalBaseKeySortsMatcherOrder(t *testing.T) {
	left, _, _ := buildLowerInputs(t, `rate(kube_pod_owner{b="two", a="one", namespace="default"}[5m])`)
	right, _, _ := buildLowerInputs(t, `rate(kube_pod_owner{namespace="default", a="changed", b="value"}[5m])`)
	if got, want := staticLabelUnionCanonicalBaseKey(left), staticLabelUnionCanonicalBaseKey(right); got != want {
		t.Fatalf("expected canonical matcher-order invariance, got:\nleft=%q\nright=%q", got, want)
	}
}

func TestStaticLabelUnionCanonicalExprRoundTrip(t *testing.T) {
	e1, err := parser.NewParser(parser.Options{}).ParseExpr(`sum by (cluster, namespace) (rate(kube_pod_owner{owner_kind="DaemonSet", workload_type="daemonset"}[5m]))`)
	if err != nil {
		t.Fatalf("ParseExpr(left): %v", err)
	}
	e2, err := parser.NewParser(parser.Options{}).ParseExpr(`sum by (namespace, cluster) (rate(kube_pod_owner{workload_type="StatefulSet", owner_kind="StatefulSet"}[5m]))`)
	if err != nil {
		t.Fatalf("ParseExpr(right): %v", err)
	}
	got := staticLabelUnionCanonicalExpr(e1).String()
	want := staticLabelUnionCanonicalExpr(e2).String()
	if got != want {
		t.Fatalf("expected canonical AST shape equivalence, got:\nleft=%s\nright=%s", got, want)
	}
}
