package rulesync

import (
	"context"
	"os"
	"path/filepath"
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
	content, err := os.ReadFile(filepath.Join(outDir, "observability-dashboards-123.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"groups:", "record: job:http_requests:rate5m", "expr: sum by (job) (rate(http_requests_total[5m]))"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("file content = %q, want %q", content, want)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "observability-ignored-456.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected unselected rule not written, got err=%v", err)
	}
}

func TestSyncOnceDeletesStaleRuleFilesOnly(t *testing.T) {
	monitoring := monitoringfake.NewSimpleClientset(promRule("observability", "dashboards", "123", nil))
	outDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outDir, "stale.yaml"), []byte("groups: []"), 0o644); err != nil {
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
	if _, err := os.Stat(filepath.Join(outDir, "stale.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected stale rule file deleted, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "notes.txt")); err != nil {
		t.Fatalf("expected non-rule file kept: %v", err)
	}
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
