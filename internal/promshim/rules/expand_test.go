package rules

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
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

func TestExpandExprUnionsConflictingRulesByDistinctStaticLabels(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: vector(1)
    labels:
      workload_type: replicaset
  - record: same:rule
    expr: vector(2)
    labels:
      workload_type: deployment
`)
	expr := parseExpr(t, `same:rule`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 2 {
		t.Fatalf("result = %#v, want two expansions", result)
	}
	if !strings.Contains(result.Expr.String(), ` or `) {
		t.Fatalf("expanded expr = %s, want union of variants", result.Expr)
	}
	if !strings.Contains(result.Expr.String(), `vector(1)`) || !strings.Contains(result.Expr.String(), `vector(2)`) {
		t.Fatalf("expanded expr = %s, want both rule variants", result.Expr)
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

func TestExpandExprDisambiguatedRuleNoFalsePositiveCycleDetection(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: g1
  rules:
  - record: same:rule
    expr: same:rule{workload_type="b"}
    labels:
      workload_type: a
- name: g2
  rules:
  - record: same:rule
    expr: up
    labels:
      workload_type: b
`)

	result, err := ExpandExpr(parseExpr(t, `same:rule{workload_type="a"}`), reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded {
		t.Fatalf("result = %#v, want expanded", result)
	}
	if len(result.Expansions) != 2 {
		t.Fatalf("result = %#v, want two expansions (outer + inner), got %d", result, len(result.Expansions))
	}
	if !strings.Contains(result.Expr.String(), `up`) {
		t.Fatalf("expanded expr = %s, want up", result.Expr)
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
      workload_type: replicaset
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

func TestApplySelectorMatchersUsesProgressivelyBuiltExprForRegexPredicates(t *testing.T) {
	expr := parseExpr(t, `sum by (job, source) (up)`)
	matchers := make([]*labels.Matcher, 0, 2)
	if m, err := labels.NewMatcher(labels.MatchRegexp, "job", "api|web"); err == nil {
		matchers = append(matchers, m)
	}
	if m, err := labels.NewMatcher(labels.MatchNotRegexp, "job", "web"); err == nil {
		matchers = append(matchers, m)
	}
	if len(matchers) != 2 {
		t.Fatalf("failed to build label matchers")
	}

	result, err := applySelectorMatchers(expr, matchers, nil)
	if err != nil {
		t.Fatalf("applySelectorMatchers returned error: %v", err)
	}
	predicateChain, ok := result.(*parser.BinaryExpr)
	if !ok {
		t.Fatalf("expanded expr type = %T, want *parser.BinaryExpr", result)
	}
	if predicateChain.Op != parser.LUNLESS {
		t.Fatalf("unexpected top-level operator %v", predicateChain.Op)
	}
	pred, ok := predicateChain.RHS.(*parser.BinaryExpr)
	if !ok {
		t.Fatalf("expected right-hand side predicate binary expr, got %T", predicateChain.RHS)
	}
	if pred.Op != parser.LAND {
		t.Fatalf("expected right-hand predicate operator LAND, got %v", pred.Op)
	}
	call, ok := pred.LHS.(*parser.Call)
	if !ok {
		t.Fatalf("expected right-hand predicate to be call, got %T", pred.LHS)
	}
	if call.Func.Name != "label_replace" {
		t.Fatalf("expected label_replace call, got %q", call.Func.Name)
	}
	if _, ok := call.Args[0].(*parser.AggregateExpr); !ok {
		t.Fatalf("expected label_replace source to be original expr (AggregateExpr), got %T", call.Args[0])
	}
}

func TestExpandExprPushesPreservedSelectorMatchersIntoRuleLeaves(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: |
      max by (cluster, namespace, workload, pod) (
        label_replace(
          kube_pod_owner{job="kube-state-metrics", owner_kind="DaemonSet"},
          "workload", "$1", "owner_name", "(.*)"
        )
      )
    labels:
      workload_type: daemonset
`)
	expr := parseExpr(t, `namespace_workload_pod:kube_pod_owner:relabel{cluster="kind", namespace="monitoring"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Expr.String()
	if strings.Contains(got, `and on (cluster)`) || strings.Contains(got, `and on (namespace)`) {
		t.Fatalf("preserved matchers should be pushed into rule leaves, got: %s", got)
	}
	if !strings.Contains(got, `cluster="kind"`) || !strings.Contains(got, `namespace="monitoring"`) {
		t.Fatalf("expanded expr does not contain pushed matchers: %s", got)
	}
}

func TestExpandExprSkipsDynamicMatchAllRegexMatcher(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      workload_type: deployment
`)
	expr := parseExpr(t, `namespace_workload_pod:kube_pod_owner:relabel{workload=~".*"}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Expr.String()
	if strings.Contains(got, matcherLabelName) || strings.Contains(got, `and on (workload)`) {
		t.Fatalf("match-all dynamic regex should not add predicate scaffolding: %s", got)
	}
	if !strings.Contains(got, `workload_type`) {
		t.Fatalf("expected static rule labels to remain: %s", got)
	}
}

func TestExpandExprSkipsDynamicEmptyRegexMatcher(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      workload_type: deployment
`)
	expr := parseExpr(t, `namespace_workload_pod:kube_pod_owner:relabel{workload=~""}`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Expr.String()
	if strings.Contains(got, matcherLabelName) || strings.Contains(got, `and on (workload)`) {
		t.Fatalf("empty dynamic regex should not add predicate scaffolding: %s", got)
	}
	if !strings.Contains(got, `workload_type`) {
		t.Fatalf("expected static rule labels to remain: %s", got)
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

func TestExpandExprUnionsRangeSelectorForDistinctStaticLabelRules(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard-a
  interval: 30s
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      team: alpha
- name: dashboard-b
  interval: 30s
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      team: beta
`)
	expr := parseExpr(t, `avg_over_time(namespace_workload_pod:kube_pod_owner:relabel[5m])`)

	result, err := ExpandExpr(expr, reg)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Expanded || len(result.Expansions) != 2 {
		t.Fatalf("result = %#v, want two expansions", result)
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
	got := subquery.String()
	if !strings.Contains(got, `up`) {
		t.Fatalf("expanded range selector = %s", got)
	}
	if !strings.Contains(got, `or`) {
		t.Fatalf("expanded range selector = %s, want union", got)
	}
}

func TestExpandExprUnionsRangeSelectorForDistinctStaticLabelRulesAtTimestamp(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard-a
  interval: 30s
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      team: alpha
- name: dashboard-b
  interval: 30s
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      team: beta
`)
	expr := parseExpr(t, `avg_over_time(namespace_workload_pod:kube_pod_owner:relabel[5m] @ 1700000000)`)

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
	if subquery.Timestamp != nil || subquery.StartOrEnd != 0 {
		t.Fatalf("subquery timestamp unexpectedly present: %v/%v", subquery.Timestamp, subquery.StartOrEnd)
	}
	got := subquery.Expr.String()
	if !strings.Contains(got, `@ 1700000000`) {
		t.Fatalf("expanded range selector = %s, want inner timestamp preserved", got)
	}
	if !strings.Contains(got, `or`) {
		t.Fatalf("expanded range selector = %s, want union", got)
	}
}

func TestExpandExprRangeSelectorWithMultipleDistinctStaticLabelRulesIsRejectedForIncompatibleIntervals(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard-a
  interval: 30s
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      team: alpha
- name: dashboard-b
  interval: 1m
  rules:
  - record: namespace_workload_pod:kube_pod_owner:relabel
    expr: up
    labels:
      team: beta
`)
	expr := parseExpr(t, `avg_over_time(namespace_workload_pod:kube_pod_owner:relabel[5m])`)

	_, err := ExpandExpr(expr, reg)
	if err == nil || !strings.Contains(err.Error(), "incompatible interval") {
		t.Fatalf("err = %v, want incompatible interval error", err)
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

func FuzzExpandExprVirtualRecordingRuleRangeUnion(f *testing.F) {
	f.Add(`namespace_workload_pod:kube_pod_owner:relabel`, int64(1700000000), int(0))
	f.Add(`namespace_workload_pod:kube_pod_owner:relabel{team="alpha"}`, int64(1700000000), int(1))
	f.Add(`namespace_workload_pod:kube_pod_owner:relabel{team=~"alpha|beta"}`, int64(1700000000), int(1))
	f.Add(`namespace_workload_pod:kube_pod_owner:relabel{job="api"}`, int64(1700000000), int(0))
	f.Add(`ambiguous_virtual_range_rule`, int64(1700000000), int(0))
	f.Add(`interval_virtual_range_rule`, int64(1700000000), int(0))

	f.Fuzz(func(t *testing.T, selector string, at int64, includeAt int) {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			return
		}
		if at < 0 || at > 3000000000 {
			return
		}
		if !strings.Contains(selector, `namespace_workload_pod:kube_pod_owner:relabel`) &&
			!strings.Contains(selector, `ambiguous_virtual_range_rule`) &&
			!strings.Contains(selector, `interval_virtual_range_rule`) {
			return
		}

		query := "avg_over_time(" + selector + "[5m]"
		if includeAt%2 == 0 && at != 0 {
			query += " @ " + strconv.FormatInt(at, 10)
		}
		query += ")"

		parsed, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(query)
		if err != nil {
			return
		}
		result, err := ExpandExpr(parsed, virtualRangeUnionRegistry())
		if err != nil {
			if isExpectedFuzzExpandError(err) {
				return
			}
			t.Fatalf("query %q: unexpected expand error: %v", query, err)
		}
		if result.Expr == nil {
			t.Fatalf("query %q: expanded expr is nil", query)
		}

		if includeAt%2 == 0 && at != 0 && strings.Contains(selector, `namespace_workload_pod:kube_pod_owner:relabel`) {
			call, ok := result.Expr.(*parser.Call)
			if !ok || len(call.Args) != 1 {
				t.Fatalf("query %q: expected avg_over_time(subquery), got %T", query, result.Expr)
			}
			subquery, ok := call.Args[0].(*parser.SubqueryExpr)
			if !ok {
				t.Fatalf("query %q: expected subquery arg, got %T", query, call.Args[0])
			}
			if subquery.Timestamp != nil || subquery.StartOrEnd != 0 {
				t.Fatalf("query %q: subquery timestamp unexpectedly present: %v/%v", query, subquery.Timestamp, subquery.StartOrEnd)
			}
			if !strings.Contains(subquery.Expr.String(), "@ "+strconv.FormatInt(at, 10)) {
				t.Fatalf("query %q: expected inner @ timestamp preserved, got %s", query, subquery.Expr.String())
			}
		}
	})
}

var (
	virtualRangeUnionRegistryOnce  sync.Once
	virtualRangeUnionRegistryValue *Registry
)

func virtualRangeUnionRegistry() *Registry {
	virtualRangeUnionRegistryOnce.Do(func() {
		virtualRangeUnionRegistryValue = EmptyRegistry()
		parseExprString := func(expr string) parser.Expr {
			parsed, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(expr)
			if err != nil {
				panic(err)
			}
			return parsed
		}
		virtualRangeUnionRegistryValue.add(RecordingRule{
			Name:       `namespace_workload_pod:kube_pod_owner:relabel`,
			Expr:       parseExprString(`up`),
			ExprString: `up`,
			Labels: map[string]string{
				"team": "alpha",
			},
			Interval: 30 * time.Second,
			Source:   `fuzz/rules.yaml`,
		})
		virtualRangeUnionRegistryValue.add(RecordingRule{
			Name:       `namespace_workload_pod:kube_pod_owner:relabel`,
			Expr:       parseExprString(`up`),
			ExprString: `up`,
			Labels: map[string]string{
				"team": "beta",
			},
			Interval: 30 * time.Second,
			Source:   `fuzz/rules.yaml`,
		})
		virtualRangeUnionRegistryValue.add(RecordingRule{
			Name:       `ambiguous_virtual_range_rule`,
			Expr:       parseExprString(`vector(1)`),
			ExprString: `vector(1)`,
			Labels: map[string]string{
				"source": "same",
			},
			Interval: 30 * time.Second,
			Source:   `fuzz/rules.yaml`,
		})
		virtualRangeUnionRegistryValue.add(RecordingRule{
			Name:       `ambiguous_virtual_range_rule`,
			Expr:       parseExprString(`vector(2)`),
			ExprString: `vector(2)`,
			Labels: map[string]string{
				"source": "same",
			},
			Interval: 30 * time.Second,
			Source:   `fuzz/rules.yaml`,
		})
		virtualRangeUnionRegistryValue.add(RecordingRule{
			Name:       `interval_virtual_range_rule`,
			Expr:       parseExprString(`vector(3)`),
			ExprString: `vector(3)`,
			Labels: map[string]string{
				"region": "a",
			},
			Interval: 30 * time.Second,
			Source:   `fuzz/rules.yaml`,
		})
		virtualRangeUnionRegistryValue.add(RecordingRule{
			Name:       `interval_virtual_range_rule`,
			Expr:       parseExprString(`vector(4)`),
			ExprString: `vector(4)`,
			Labels: map[string]string{
				"region": "b",
			},
			Interval: time.Minute,
			Source:   `fuzz/rules.yaml`,
		})
		virtualRangeUnionRegistryValue.validateExpansions()
	})
	return virtualRangeUnionRegistryValue
}

func isExpectedFuzzExpandError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, `ambiguous across`) ||
		strings.Contains(msg, `incompatible interval settings`) ||
		strings.Contains(msg, `recording rule selector matcher`)
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
