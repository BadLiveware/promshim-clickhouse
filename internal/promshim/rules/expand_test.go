package rules

import (
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

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

func TestExpandExprRejectsUnsupportedDynamicMatcher(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
`)
	expr := parseExpr(t, `job:http_requests:rate5m{job=~"api|web"}`)

	_, err := ExpandExpr(expr, reg)
	if err == nil || !strings.Contains(err.Error(), "dynamic label") {
		t.Fatalf("err = %v, want dynamic label matcher rejection", err)
	}
}

func TestExpandExprRejectsRecordingRuleSubquery(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: up
`)
	expr := parseExpr(t, `job:http_requests:rate5m[5m:]`)

	_, err := ExpandExpr(expr, reg)
	if err == nil || !strings.Contains(err.Error(), "subqueries") {
		t.Fatalf("err = %v, want subquery rejection", err)
	}
}

func TestExpandExprRejectsRecordingRuleRangeSelector(t *testing.T) {
	reg := registryForTest(t, `groups:
- name: dashboard
  rules:
  - record: job:http_requests:rate5m
    expr: up
`)
	expr := parseExpr(t, `avg_over_time(job:http_requests:rate5m[5m])`)

	_, err := ExpandExpr(expr, reg)
	if err == nil || !strings.Contains(err.Error(), "range selectors") {
		t.Fatalf("err = %v, want range selector rejection", err)
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
