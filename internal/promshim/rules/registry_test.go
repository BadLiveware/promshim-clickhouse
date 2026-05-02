package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFilesLoadsRecordingRulesAndIgnoresAlerts(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  interval: 30s
  labels:
    source: rules
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
    labels:
      team: edge
  - alert: HighErrorRate
    expr: up == 0
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Errors()) != 0 {
		t.Fatalf("unexpected load errors: %v", reg.Errors())
	}
	if reg.Len() != 1 {
		t.Fatalf("registry size = %d, want 1", reg.Len())
	}
	rule, ok := reg.Lookup("job:http_requests:rate5m")
	if !ok {
		t.Fatal("recording rule not found")
	}
	if rule.Expr == nil || rule.ExprString != "sum by (job) (rate(http_requests_total[5m]))" {
		t.Fatalf("unexpected rule expression: %#v", rule)
	}
	if rule.GroupName != "dashboard" || rule.Labels["team"] != "edge" || rule.GroupLabels["source"] != "rules" {
		t.Fatalf("unexpected rule metadata: %#v", rule)
	}
}

func TestLoadFilesCachesNestedRuleExpansions(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: inner:rule
    expr: up
  - record: outer:rule
    expr: inner:rule * 2
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Errors()) != 0 {
		t.Fatalf("unexpected load errors: %v", reg.Errors())
	}
	rule, ok := reg.Lookup("outer:rule")
	if !ok {
		t.Fatal("recording rule not found")
	}
	expr, expansions, ok := reg.CachedExpansion(recordingRuleCacheKey(rule))
	if !ok {
		t.Fatal("expected cached outer expansion")
	}
	if !strings.Contains(expr.String(), "up") || len(expansions) != 1 || expansions[0].Record != "inner:rule" {
		t.Fatalf("cached expansion expr=%s expansions=%#v", expr.String(), expansions)
	}
}

func TestLoadFilesDetectsExpansionCycles(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: a:rule
    expr: b:rule
  - record: b:rule
    expr: a:rule
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Errors()) == 0 || !strings.Contains(reg.Errors()[0].Error(), "cycle") {
		t.Fatalf("load errors = %v, want cycle error", reg.Errors())
	}
}

func TestLoadFilesTreatsDifferentQueryOffsetsAsConflicts(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  query_offset: 1m
  rules:
  - record: same:rule
    expr: up
- name: dashboard-2
  query_offset: 2m
  rules:
  - record: same:rule
    expr: up
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("same:rule"); ok {
		t.Fatal("query_offset conflict should not be directly selectable")
	}
	if conflicts, ok := reg.Conflict("same:rule"); !ok || len(conflicts) != 2 {
		t.Fatalf("conflicts = %#v, want two", conflicts)
	}
}

func TestLoadFilesDetectsConflictingRecords(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: up
  - record: same:rule
    expr: process_start_time_seconds
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("same:rule"); ok {
		t.Fatal("conflicting rule should not be directly selectable")
	}
	if conflicts, ok := reg.Conflict("same:rule"); !ok || len(conflicts) != 2 {
		t.Fatalf("conflicts = %#v, want two", conflicts)
	}
}

func TestAllRulesReturnsAllVariants(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: same:rule
    expr: up
  - record: same:rule
    expr: process_start_time_seconds
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Errors()) != 0 {
		t.Fatalf("unexpected load errors: %v", reg.Errors())
	}
	rules := reg.AllRules()
	if len(rules) != 2 {
		t.Fatalf("AllRules() = %d, want 2", len(rules))
	}
}

func TestLoadFilesReportsInvalidRules(t *testing.T) {
	path := writeRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: broken:rule
    expr: sum(
`)

	reg, err := LoadFiles([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Errors()) == 0 || !strings.Contains(reg.Errors()[0].Error(), "broken:rule") {
		t.Fatalf("load errors = %v, want broken rule parse error", reg.Errors())
	}
}

func writeRulesFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
