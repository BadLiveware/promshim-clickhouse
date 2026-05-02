package rulesync

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringclient "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	commonmodel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

const (
	DefaultOutputDir     = "/etc/promshim/rules"
	DefaultPrometheusVer = "3.0.0"
)

type Options struct {
	Namespaces    []string
	RuleSelector  labels.Selector
	OutputDir     string
	PrometheusVer string
	SyncInterval  time.Duration
	Once          bool
}

type Result struct {
	RuleCount   int
	OutputFiles []string
}

type Syncer struct {
	monitoring monitoringclient.Interface
	logger     *slog.Logger
	opts       Options
}

func New(monitoring monitoringclient.Interface, _ kubernetes.Interface, logger *slog.Logger, opts Options) (*Syncer, error) {
	if monitoring == nil {
		return nil, fmt.Errorf("monitoring client is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	opts = normalizeOptions(opts)
	if opts.OutputDir == "" {
		return nil, fmt.Errorf("output directory is required")
	}
	return &Syncer{monitoring: monitoring, logger: logger, opts: opts}, nil
}

func (s *Syncer) Run(ctx context.Context) error {
	if s.opts.Once || s.opts.SyncInterval <= 0 {
		_, err := s.SyncOnce(ctx)
		return err
	}
	ticker := time.NewTicker(s.opts.SyncInterval)
	defer ticker.Stop()
	for {
		if _, err := s.SyncOnce(ctx); err != nil {
			s.logger.Error("sync PrometheusRule files", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Syncer) SyncOnce(ctx context.Context) (Result, error) {
	ruleFiles, err := s.RuleFiles(ctx)
	if err != nil {
		return Result{}, err
	}
	files, err := s.syncFiles(ruleFiles)
	if err != nil {
		return Result{}, err
	}
	s.logger.Info("synced PrometheusRule files", "rules", len(ruleFiles), "files", files)
	return Result{RuleCount: len(ruleFiles), OutputFiles: files}, nil
}

func (s *Syncer) RuleFiles(ctx context.Context) (map[string]string, error) {
	namespaces := s.opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}
	out := map[string]string{}
	for _, namespace := range namespaces {
		items, err := s.monitoring.MonitoringV1().PrometheusRules(namespace).List(ctx, metav1.ListOptions{LabelSelector: s.opts.RuleSelector.String()})
		if err != nil {
			return nil, fmt.Errorf("list PrometheusRule in namespace %q: %w", namespace, err)
		}
		for i := range items.Items {
			rule := &items.Items[i]
			content, err := renderRule(rule, s.opts.PrometheusVer)
			if err != nil {
				return nil, fmt.Errorf("render PrometheusRule %s/%s: %w", rule.Namespace, rule.Name, err)
			}
			out[ruleFilename(rule)] = content
		}
	}
	return out, nil
}

func renderRule(rule *monitoringv1.PrometheusRule, prometheusVersion string) (string, error) {
	if rule == nil {
		return "", fmt.Errorf("nil PrometheusRule")
	}
	validation := commonmodel.UTF8Validation
	if prometheusVersion != "" && strings.HasPrefix(prometheusVersion, "2.") {
		validation = commonmodel.LegacyValidation
	}
	content, err := yaml.Marshal(rule.Spec)
	if err != nil {
		return "", fmt.Errorf("marshal rule spec: %w", err)
	}
	if _, errs := rulefmt.Parse(content, false, validation, parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true})); len(errs) > 0 {
		return "", fmt.Errorf("invalid rule: %v", errs)
	}
	return string(content), nil
}

func (s *Syncer) syncFiles(ruleFiles map[string]string) ([]string, error) {
	if err := os.MkdirAll(s.opts.OutputDir, 0o755); err != nil {
		return nil, err
	}
	desired := map[string]struct{}{}
	files := make([]string, 0, len(ruleFiles))
	for _, filename := range sortedKeys(ruleFiles) {
		desired[filename] = struct{}{}
		path := filepath.Join(s.opts.OutputDir, filename)
		if err := writeFileAtomic(path, []byte(ruleFiles[filename]), 0o644); err != nil {
			return nil, err
		}
		files = append(files, path)
	}
	entries, err := os.ReadDir(s.opts.OutputDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		if _, ok := desired[entry.Name()]; !ok {
			if err := os.Remove(filepath.Join(s.opts.OutputDir, entry.Name())); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
		}
	}
	return files, nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func normalizeOptions(opts Options) Options {
	if opts.RuleSelector == nil {
		opts.RuleSelector = labels.Everything()
	}
	if opts.OutputDir == "" {
		opts.OutputDir = DefaultOutputDir
	}
	if opts.PrometheusVer == "" {
		opts.PrometheusVer = DefaultPrometheusVer
	}
	return opts
}

func ruleFilename(rule *monitoringv1.PrometheusRule) string {
	parts := []string{rule.Namespace, rule.Name}
	if rule.UID != "" {
		parts = append(parts, string(rule.UID))
	}
	for i := range parts {
		parts[i] = sanitizeFilenamePart(parts[i])
	}
	return strings.Join(parts, "-") + ".yaml"
}

func sanitizeFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, value)
}

func sortedKeys(in map[string]string) []string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func SplitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}
