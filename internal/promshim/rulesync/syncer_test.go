package rulesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringfake "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestSyncOnceWritesSelectedPrometheusRulesToFiles(t *testing.T) {
	monitoring := monitoringfake.NewSimpleClientset(
		promRule("observability", "dashboards", "123", map[string]string{"release": "k8s-monitoring"}),
		promRule("observability", "ignored", "456", map[string]string{"release": "other"}),
	)
	outDir := t.TempDir()
	syncer, err := New(monitoring, nil, nil, Options{
		Namespaces:   []string{"observability"},
		RuleSelector: labels.SelectorFromSet(labels.Set{"release": "k8s-monitoring"}),
		OutputDir:    outDir,
		Once:         true,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := syncer.SyncOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.RuleCount != 1 || len(result.OutputFiles) != 1 {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(outDir, "promshim-observability-dashboards-123.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"groups:", "record: job:http_requests:rate5m", "expr: sum by (job) (rate(http_requests_total[5m]))"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("file content = %q, want %q", content, want)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "promshim-observability-ignored-456.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected unselected rule not written, got err=%v", err)
	}
}

func TestSyncOnceUpdatesMetricsAndHealth(t *testing.T) {
	monitoring := monitoringfake.NewSimpleClientset(promRule("observability", "dashboards", "123", nil))
	syncer, err := New(monitoring, nil, nil, Options{Namespaces: []string{"observability"}, OutputDir: t.TempDir(), Once: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRes := httptest.NewRecorder()
	syncer.MetricsHandler().ServeHTTP(metricsRes, metricsReq)
	metrics := parsePromMetrics(t, metricsRes.Body.String())
	if got := metrics["promshim_rule_syncer_selected_rules"]; got != 1 {
		t.Fatalf("promshim_rule_syncer_selected_rules = %f, want 1", got)
	}
	if got := metrics["promshim_rule_syncer_rendered_files"]; got != 1 {
		t.Fatalf("promshim_rule_syncer_rendered_files = %f, want 1", got)
	}
	if got := metrics["promshim_rule_syncer_sync_failures_total"]; got != 0 {
		t.Fatalf("promshim_rule_syncer_sync_failures_total = %f, want 0", got)
	}
	if got := metrics["promshim_rule_syncer_last_success_timestamp_seconds"]; got <= 0 {
		t.Fatalf("promshim_rule_syncer_last_success_timestamp_seconds = %f, want > 0", got)
	}
	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRes := httptest.NewRecorder()
	syncer.HealthHandler().ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("health status = %d body=%s", healthRes.Code, healthRes.Body.String())
	}
}

func TestSyncOnceDeletesStaleRuleFilesOnly(t *testing.T) {
	monitoring := monitoringfake.NewSimpleClientset(promRule("observability", "dashboards", "123", nil))
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "promshim-stale.yaml"), []byte("groups: []"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "external.yaml"), []byte("groups: []"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	syncer, err := New(monitoring, nil, nil, Options{Namespaces: []string{"observability"}, OutputDir: outDir, Once: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "promshim-stale.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected stale generated rule file deleted, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "external.yaml")); err != nil {
		t.Fatalf("expected external YAML file kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "notes.txt")); err != nil {
		t.Fatalf("expected non-rule file kept: %v", err)
	}
}

func parsePromMetrics(t *testing.T, body string) map[string]float64 {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	metrics := map[string]float64{}
	for _, line := range lines {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			t.Fatalf("failed parsing metric value from %q: %v", line, err)
		}
		metrics[parts[0]] = value
	}
	return metrics
}

func promRule(namespace, name, uid string, ruleLabels map[string]string) runtime.Object {
	return &monitoringv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID(uid), Labels: ruleLabels},
		Spec: monitoringv1.PrometheusRuleSpec{Groups: []monitoringv1.RuleGroup{{
			Name: "dashboard",
			Rules: []monitoringv1.Rule{{
				Record: "job:http_requests:rate5m",
				Expr:   intstr.FromString("sum by (job) (rate(http_requests_total[5m]))"),
			}},
		}}},
	}
}
