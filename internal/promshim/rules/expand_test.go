package rules

import (
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestExpandExprAppliesRuleQueryOffset(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  query_offset: 2m
  rules:
  - record: job:http_requests:rate5m
    expr: rate(http_requests_total[5m])
`)
	expr := parseExpr(t, `job:http_requests:rate5m`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Expr.String(), `offset 2m`) {
		t.Fatalf("expanded expr = %s, want query_offset applied", result.Expr.String())
	}
}

func TestExpandExprReplacesRecordingRuleSelector(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  labels:
    source: rules
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
    labels:
      team: edge
`)
	expr := parseExpr(t, `job:http_requests:rate5m{job="api", source="rules"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 1 {
		t.Fatalf("result = %#v, want one expansion", result)
	}
	got := result.Expr.String()
	for _, want := range []string{`rate(http_requests_total[5m])`, `label_replace`, `__name__`, `job:http_requests:rate5m`, `team`, `edge`, `source`, `rules`, `and on (job)`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded expr = %s, want to contain %s", got, want)
		}
	}
}

func TestExpandExprStaticLabelMismatchReturnsEmptyVector(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  labels:
    source: rules
  rules:
  - record: job:http_requests:rate5m
    expr: up
`)
	expr := parseExpr(t, `job:http_requests:rate5m{source="other"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Expr.String(), "unless") {
		t.Fatalf("expanded mismatch = %s, want empty vector expression", result.Expr.String())
	}
}

func TestExpandExprRejectsConflictOnlyRegistry(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: up
  - record: same:rule
    expr: process_start_time_seconds
`)
	expr := parseExpr(t, `same:rule`)

	_, err := ExpandExpr(expr, reg)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguity error", err)
	}
}

func TestExpandExprDisambiguatesConflictByStaticRuleLabels(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: replicaset
  - record: same:rule
    expr: sum(up)
    labels:
      workload_type: deployment
`)
	expr := parseExpr(t, `same:rule{workload_type="deployment"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 1 {
		t.Fatalf("result = %#v, want one expansion", result)
	}
	got := result.Expr.String()
	if !strings.Contains(got, `sum(up)`) {
		t.Fatalf("expanded expr = %s, want deployment variant", got)
	}
	if !strings.Contains(got, `"deployment"`) {
		t.Fatalf("expanded expr = %s, want workload_type static label", got)
	}
}

func TestExpandExprDisambiguatesConflictByGroupAndRuleLabels(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: g1
  labels:
    source: left
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: replicaset
- name: g2
  labels:
    source: right
  rules:
  - record: same:rule
    expr: sum(up)
    labels:
      workload_type: replicaset
`)
	expr := parseExpr(t, `same:rule{source="right",workload_type="replicaset"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 1 {
		t.Fatalf("result = %#v, want one expansion", result)
	}
	got := result.Expr.String()
	if !strings.Contains(got, `sum(up)`) {
		t.Fatalf("expanded expr = %s, want right variant", got)
	}
	if !strings.Contains(got, `"right"`) {
		t.Fatalf("expanded expr = %s, want group label source=right applied", got)
	}
}

func TestExpandExprCachesDisambiguatedConflictingRulesBySignature(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: g1
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: replicaset
- name: g2
  rules:
  - record: same:rule
    expr: sum(up)
    labels:
      workload_type: deployment
`)
	replicaset, err := ExpandExpr(parseExpr(t, `same:rule{workload_type="replicaset"}`), reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replicaset.Expr.String(), `up`) || strings.Contains(replicaset.Expr.String(), `sum(up)`) {
		t.Fatalf("first expansion should use replicaset variant, got %s", replicaset.Expr)
	}
	deployment, err := ExpandExpr(parseExpr(t, `same:rule{workload_type="deployment"}`), reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deployment.Expr.String(), `sum(up)`) {
		t.Fatalf("second expansion should use deployment variant, got %s", deployment.Expr)
	}
	replicasetAgain, err := ExpandExpr(parseExpr(t, `same:rule{workload_type="replicaset"}`), reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replicasetAgain.Expr.String(), `up`) || strings.Contains(replicasetAgain.Expr.String(), `sum(up)`) {
		t.Fatalf("cached expansion should remain per-variant, got %s", replicasetAgain.Expr)
	}
}

func TestExpandExprConcurrentDisambiguatedConflicts(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: g1
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: replicaset
- name: g2
  rules:
  - record: same:rule
    expr: sum(up)
    labels:
      workload_type: deployment
`)
	replica := parseExpr(t, `same:rule{workload_type="replicaset"}`)
	deployment := parseExpr(t, `same:rule{workload_type="deployment"}`)
	total := 200
	errCh := make(chan error, total)
	var wg sync.WaitGroup
	wg.Add(total)
	for i := 0; i < total; i++ {
		i := i
		go func() {
			defer wg.Done()
			expr := replica
			if i%2 == 1 {
				expr = deployment
			}
			_, err := ExpandExpr(expr, reg)
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestExpandExprReturnsEmptyVectorWhenNoStaticLabelMatch(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: replicaset
  - record: same:rule
    expr: sum(up)
    labels:
      workload_type: deployment
`)
	expr := parseExpr(t, `same:rule{workload_type="daemonset"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded {
		t.Fatalf("result = %#v, want expanded", result)
	}
	if !strings.Contains(result.Expr.String(), `unless`) {
		t.Fatalf("expanded expr = %s, want empty vector expression", result.Expr)
	}
	if len(result.Expansions) != 0 {
		t.Fatalf("result expansions = %#v, want 0", result.Expansions)
	}
}

func TestExpandExprRejectsConflictingRuleWhenStaticLabelsAreNotDisambiguating(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: replicaset
  - record: same:rule
    expr: sum(up)
    labels:
      workload_type: deployment
`)
	expr := parseExpr(t, `same:rule{job="api"}`)

	_, err := ExpandExpr(expr, reg)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want ambiguity error", err)
	}
}

func TestExpandExprAppliesRegexDynamicMatcher(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
`)
	expr := parseExpr(t, `job:http_requests:rate5m{job=~"api|web"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Expr.String()
	for _, want := range []string{`label_replace`, matcherLabelName, `api|web`, `and on (job)`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded expr = %s, want %s", got, want)
		}
	}
}

func TestExpandExprAppliesNegativeDynamicMatcher(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
`)
	expr := parseExpr(t, `job:http_requests:rate5m{job!~"api|web"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Expr.String()
	for _, want := range []string{`label_replace`, matcherLabelName, `api|web`, `unless on (job)`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded expr = %s, want %s", got, want)
		}
	}
}

func TestExpandExprExpandsRecordingRuleSubquery(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: up
`)
	expr := parseExpr(t, `job:http_requests:rate5m[5m:]`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 1 {
		t.Fatalf("result = %#v, want one expansion", result)
	}
	if _, ok := result.Expr.(*parser.SubqueryExpr); !ok {
		t.Fatalf("expanded expr type = %T, want subquery", result.Expr)
	}
	if !strings.Contains(result.Expr.String(), "up") || !strings.Contains(result.Expr.String(), "[5m:") {
		t.Fatalf("expanded subquery = %s", result.Expr.String())
	}
}

func TestExpandExprExpandsRecordingRuleRangeSelectorToSubquery(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  interval: 30s
  rules:
  - record: job:http_requests:rate5m
    expr: up
`)
	expr := parseExpr(t, `avg_over_time(job:http_requests:rate5m[5m])`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 1 {
		t.Fatalf("result = %#v, want one expansion", result)
	}
	if !strings.Contains(result.Expr.String(), "up") || !strings.Contains(result.Expr.String(), "[5m:30s]") {
		t.Fatalf("expanded range selector = %s", result.Expr.String())
	}
}

func TestExpandExprRecursivelyExpandsNestedRecordingRules(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
  - record: job:http_requests:rate5m:double
    expr: job:http_requests:rate5m * 2
`)
	expr := parseExpr(t, `job:http_requests:rate5m:double{job="api"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 2 {
		t.Fatalf("result = %#v, want nested expansion chain", result)
	}
	got := result.Expr.String()
	for _, want := range []string{`http_requests_total`, `job:http_requests:rate5m`, `job:http_requests:rate5m:double`, `* 2`, `and on (job)`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded nested expr = %s, want %s", got, want)
		}
	}
}

func TestExpandExprDoesNotDoubleApplyAncestorQueryOffset(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  query_offset: 5m
  rules:
  - record: base:rule
    expr: up
  - record: double:rule
    expr: base:rule
`)
	expr := parseExpr(t, `double:rule`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 2 {
		t.Fatalf("result = %#v, want nested expansion chain with both rules", result)
	}
	got := result.Expr.String()
	if !strings.Contains(got, `offset 5m`) {
		t.Fatalf("expanded expr = %s, want base query_offset applied", got)
	}
	if strings.Contains(got, `offset 10m`) {
		t.Fatalf("expanded expr = %s, want single query_offset application, got double offset", got)
	}
}

func TestExpandExprSkipsOffsetOnAbsoluteTimestampSelectors(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  query_offset: 2m
  rules:
  - record: at:rule
    expr: up
`)
	expr := parseExpr(t, `at:rule @ 1700000000`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Expr.String()
	if !strings.Contains(got, `@ 1700000000`) {
		t.Fatalf("expanded expr = %s, want absolute timestamp to be preserved", got)
	}
	if strings.Contains(got, `offset 2m`) {
		t.Fatalf("expanded expr = %s, want no query_offset for absolute @ timestamp", got)
	}
}

func TestExpandExprAppliesOffsetAcrossNestedRuleExpansionWithoutInnerOffset(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: inner
  rules:
  - record: inner_rule
    expr: up
- name: outer
  query_offset: 5m
  rules:
  - record: outer_rule
    expr: inner_rule
`)
	expr := parseExpr(t, `outer_rule`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded {
		t.Fatalf("result = %#v, want expanded", result)
	}
	got := result.Expr.String()
	if !strings.Contains(got, `offset 5m`) {
		t.Fatalf("expanded expr = %s, want outer query_offset applied", got)
	}
	if strings.Contains(got, `offset 10m`) {
		t.Fatalf("expanded expr = %s, want no extra query_offset application", got)
	}
}

func TestExpandExprMatrixSelectorPreservesTimestampInsideButNotOnSubquery(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: at_rule
    expr: up
`)
	expr := parseExpr(t, `avg_over_time(at_rule[5m] @ 1700000000)`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	call, ok := result.Expr.(*parser.Call)
	if !ok {
		t.Fatalf("expanded expr type = %T, want call", result.Expr)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expanded call args = %#v, want one arg", call.Args)
	}
	subquery, ok := call.Args[0].(*parser.SubqueryExpr)
	if !ok {
		t.Fatalf("call arg type = %T, want subquery", call.Args[0])
	}
	if subquery.Timestamp != nil {
		t.Fatalf("subquery timestamp unexpectedly present: %v", subquery.Timestamp)
	}
	if subquery.StartOrEnd != 0 {
		t.Fatalf("subquery start/end unexpectedly present: %v", subquery.StartOrEnd)
	}
	if !strings.Contains(subquery.Expr.String(), `@ 1700000000`) {
		t.Fatalf("expanded inner expr = %s, want absolute timestamp preserved on inner selectors", subquery.Expr)
	}
}

func TestExpandExprDoesNotUseNilExprFromCachedExpansionOrderDependency(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: depends_on
    expr: base_rule
  - record: base_rule
    expr: up
`)
	expr := parseExpr(t, `depends_on`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expr == nil {
		t.Fatalf("expanded expr is nil, expected resolved expansion")
	}
	if !strings.Contains(result.Expr.String(), "up") {
		t.Fatalf("expanded expr = %s, want base metric expansion", result.Expr)
	}
}

func registryForTest(t *testing.T, content string) *Registry {
	t.Helper()
	reg, err := LoadFiles([]string{writeRulesFile(t, content)})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Errors()) > 0 {
		t.Fatalf("load errors: %v", reg.Errors())
	}
	return reg
}

func parseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(query)
	if err != nil {
		t.Fatal(err)
	}
	return expr
}
