package promharness

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

const (
	CompareModeExact      = "exact"
	CompareModeStructural = "structural"
)

var rateFamilyFunctionNames = map[string]struct{}{
	"rate":               {},
	"irate":              {},
	"increase":           {},
	"delta":              {},
	"idelta":             {},
	"deriv":              {},
	"histogram_quantile": {},
}

// InferCompareMode returns the comparison mode for a query. Explicit
// spec.CompareMode wins; otherwise queries containing rate-family functions
// (rate/irate/increase/delta/idelta/deriv/histogram_quantile) default to
// "structural" because ClickHouse's PromQL rate() does not apply Prometheus's
// boundary extrapolation, producing different float values on identical data.
func InferCompareMode(spec QuerySpec) string {
	if mode := strings.ToLower(strings.TrimSpace(spec.CompareMode)); mode != "" {
		return mode
	}
	p := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true})
	expr, err := p.ParseExpr(spec.Query)
	if err != nil {
		return CompareModeExact
	}
	found := false
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if call, ok := node.(*parser.Call); ok {
			if _, matches := rateFamilyFunctionNames[strings.ToLower(call.Func.Name)]; matches {
				found = true
			}
		}
		return nil
	})
	if found {
		return CompareModeStructural
	}
	return CompareModeExact
}

func RunSeed(ctx context.Context, cfg SeedConfig) (Manifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if err := WaitForHTTPOK(ctx, client, "http://prometheus:9090/-/ready", "prometheus readiness"); err != nil {
		return Manifest{}, err
	}
	if err := WaitForHTTPOK(ctx, client, "http://clickhouse:8123/ping", "clickhouse readiness"); err != nil {
		return Manifest{}, err
	}

	variantNames := cfg.DatasetVariants
	if len(variantNames) == 0 {
		dataset := GenerateDataset(cfg)
		if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.PromRemoteWriteURL, dataset.Request); err != nil {
			return Manifest{}, err
		}
		if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.ClickHouseRemoteWriteURL, dataset.Request); err != nil {
			return Manifest{}, err
		}
		if cfg.PromClickRemoteWriteURL != "" {
			if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.PromClickRemoteWriteURL, dataset.Request); err != nil {
				return Manifest{}, err
			}
		}

		manifest := Manifest{
			Seed:            cfg.Seed,
			BaseUnixSeconds: cfg.BaseTime.Unix(),
			StepSeconds:     int64(cfg.Step / time.Second),
			Points:          cfg.Points,
			SeriesCount:     dataset.SeriesCount,
			SampleCount:     dataset.SampleCount,
			GeneratedAtUTC:  time.Now().UTC().Format(time.RFC3339),
		}
		if err := WriteManifest(ManifestPath(cfg.ArtifactDir), manifest); err != nil {
			return Manifest{}, err
		}
		return manifest, nil
	}

	variants := make([]Manifest, 0, len(variantNames))
	separation := datasetVariantSeparation(cfg)
	for index, variantName := range variantNames {
		variantCfg := cfg
		variantCfg.DatasetVariant = variantName
		variantCfg.BaseTime = cfg.BaseTime.Add(time.Duration(index) * separation)
		dataset := GenerateDataset(variantCfg)
		if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.PromRemoteWriteURL, dataset.Request); err != nil {
			return Manifest{}, err
		}
		if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.ClickHouseRemoteWriteURL, dataset.Request); err != nil {
			return Manifest{}, err
		}
		if cfg.PromClickRemoteWriteURL != "" {
			if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.PromClickRemoteWriteURL, dataset.Request); err != nil {
				return Manifest{}, err
			}
		}
		variants = append(variants, Manifest{
			Seed:            cfg.Seed,
			BaseUnixSeconds: variantCfg.BaseTime.Unix(),
			StepSeconds:     int64(cfg.Step / time.Second),
			Points:          cfg.Points,
			SeriesCount:     dataset.SeriesCount,
			SampleCount:     dataset.SampleCount,
			GeneratedAtUTC:  time.Now().UTC().Format(time.RFC3339),
			DatasetVariant:  variantName,
		})
	}
	manifest := Manifest{
		Seed:            cfg.Seed,
		BaseUnixSeconds: variants[0].BaseUnixSeconds,
		StepSeconds:     int64(cfg.Step / time.Second),
		Points:          cfg.Points,
		SeriesCount:     variants[0].SeriesCount,
		SampleCount:     variants[0].SampleCount,
		GeneratedAtUTC:  time.Now().UTC().Format(time.RFC3339),
		Variants:        variants,
	}
	if err := WriteManifest(ManifestPath(cfg.ArtifactDir), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

type compareSubject struct {
	Name    string
	BaseURL string
}

func configuredSubjects(cfg CompareConfig) []compareSubject {
	subjects := []compareSubject{{Name: "shim", BaseURL: cfg.PromshimBaseURL}}
	if cfg.PromClickBaseURL != "" {
		subjects = append(subjects, compareSubject{Name: "promclick", BaseURL: cfg.PromClickBaseURL})
	}
	if len(cfg.Subjects) == 0 {
		return subjects
	}
	allowed := map[string]struct{}{}
	for _, subject := range cfg.Subjects {
		allowed[strings.ToLower(strings.TrimSpace(subject))] = struct{}{}
	}
	filtered := make([]compareSubject, 0, len(subjects))
	for _, subject := range subjects {
		if _, ok := allowed[subject.Name]; ok {
			filtered = append(filtered, subject)
		}
	}
	return filtered
}

func subjectsForQuery(query QuerySpec, configured []compareSubject) ([]compareSubject, error) {
	if len(query.Subjects) == 0 {
		return configured, nil
	}
	allowed := map[string]struct{}{}
	for _, subject := range query.Subjects {
		allowed[strings.ToLower(strings.TrimSpace(subject))] = struct{}{}
	}
	filtered := make([]compareSubject, 0, len(configured))
	for _, subject := range configured {
		if _, ok := allowed[subject.Name]; ok {
			filtered = append(filtered, subject)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("query %q requested subjects %v but none are configured", query.Name, query.Subjects)
	}
	return filtered, nil
}

func manifestsForQuery(query QuerySpec, configured []Manifest) ([]Manifest, error) {
	if len(query.DatasetVariants) == 0 && len(query.ExcludeDatasetVariants) == 0 {
		return configured, nil
	}
	allowed := map[string]struct{}{}
	for _, variant := range query.DatasetVariants {
		allowed[strings.ToLower(strings.TrimSpace(variant))] = struct{}{}
	}
	excluded := map[string]struct{}{}
	for _, variant := range query.ExcludeDatasetVariants {
		excluded[strings.ToLower(strings.TrimSpace(variant))] = struct{}{}
	}
	filtered := make([]Manifest, 0, len(configured))
	for _, manifest := range configured {
		name := manifestDatasetVariantName(manifest)
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		if _, skip := excluded[name]; skip {
			continue
		}
		filtered = append(filtered, manifest)
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("query %q requested dataset variants include=%v exclude=%v but none are available", query.Name, query.DatasetVariants, query.ExcludeDatasetVariants)
	}
	return filtered, nil
}

func effectiveQuerySpec(cfg CompareConfig, spec QuerySpec) QuerySpec {
	if strings.TrimSpace(spec.NativeLoweringMode) != "" || strings.TrimSpace(cfg.DefaultNativeLoweringMode) == "" {
		return spec
	}
	spec.NativeLoweringMode = cfg.DefaultNativeLoweringMode
	return spec
}

func RunCompare(ctx context.Context, cfg CompareConfig) (CompareReport, error) {
	manifest, err := ReadManifest(ManifestPath(cfg.ArtifactDir))
	if err != nil {
		return CompareReport{}, err
	}
	manifests := manifestVariants(manifest)
	queries, err := LoadQueryCorpus(cfg.CorpusPath)
	if err != nil {
		return CompareReport{}, err
	}
	client := &http.Client{Timeout: cfg.Timeout}
	subjects := configuredSubjects(cfg)
	if len(subjects) == 0 {
		return CompareReport{}, fmt.Errorf("no compare subjects configured")
	}
	if err := waitForDatasetAvailability(ctx, client, cfg, manifests, subjects); err != nil {
		return CompareReport{}, err
	}

	report := CompareReport{CorpusPath: cfg.CorpusPath, Manifest: manifest, Results: make([]QueryComparison, 0, len(queries)*len(subjects)*len(manifests))}
	var firstErr error
	appendResult := func(query QuerySpec, variantName, datasetVariant, subject, status, detail string) {
		severity, bucket := ClassifyComparison(status, detail)
		report.Results = append(report.Results, QueryComparison{
			Name:           query.Name,
			Variant:        variantName,
			DatasetVariant: datasetVariant,
			Subject:        subject,
			Query:          query.Query,
			Status:         status,
			Severity:       severity,
			Bucket:         bucket,
			CompareMode:    InferCompareMode(query),
			Detail:         detail,
		})
	}
	for _, query := range queries {
		variants, err := expandQueryVariants(query)
		if err != nil {
			appendResult(query, "", "", "harness", "error", err.Error())
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, variant := range variants {
			variant.Spec = effectiveQuerySpec(cfg, variant.Spec)
			querySubjects, err := subjectsForQuery(variant.Spec, subjects)
			if err != nil {
				appendResult(variant.Spec, variant.Variant, "", "harness", "error", err.Error())
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			queryManifests, err := manifestsForQuery(variant.Spec, manifests)
			if err != nil {
				appendResult(variant.Spec, variant.Variant, "", "harness", "error", err.Error())
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			for _, datasetManifest := range queryManifests {
				promResult, err := QueryAndFetch(client, cfg.PrometheusBaseURL, datasetManifest, variant.Spec)
				if err != nil {
					for _, subject := range querySubjects {
						appendResult(variant.Spec, variant.Variant, datasetManifest.DatasetVariant, subject.Name, "error", err.Error())
					}
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				for _, subject := range querySubjects {
					subjectResult, err := QueryAndFetch(client, subject.BaseURL, datasetManifest, variant.Spec)
					if err != nil {
						appendResult(variant.Spec, variant.Variant, datasetManifest.DatasetVariant, subject.Name, "error", err.Error())
						if firstErr == nil {
							firstErr = err
						}
						continue
					}
					result, err := CompareQueryOutcome(variant.Spec, subject.Name, promResult, subjectResult)
					if err != nil {
						appendResult(variant.Spec, variant.Variant, datasetManifest.DatasetVariant, subject.Name, result, err.Error())
						if firstErr == nil {
							firstErr = err
						}
						continue
					}
					appendResult(variant.Spec, variant.Variant, datasetManifest.DatasetVariant, subject.Name, result, "")
				}
			}
		}
	}
	annotateCauseClusters(&report)
	if err := writeCompareReport(cfg.ArtifactDir, report); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return report, firstErr
	}
	return report, nil
}

func CompareQueryOutcome(spec QuerySpec, subject string, promResult queryResult, subjectResult queryResult) (string, error) {
	expectedStatus := strings.ToLower(strings.TrimSpace(spec.ExpectedStatus))
	if expectedStatus == "" {
		expectedStatus = "ok"
	}
	switch expectedStatus {
	case "ok":
		if promResult.Status != "success" {
			return "error", fmt.Errorf("expected success from Prometheus for %q but got %s: %s", spec.Name, promResult.Status, promResult.Error)
		}
		if subjectResult.Status != "success" {
			return "error", fmt.Errorf("expected success from subject %s for %q but got %s: %s", subject, spec.Name, subjectResult.Status, subjectResult.Error)
		}
		var compareErr error
		if InferCompareMode(spec) == CompareModeStructural {
			compareErr = CompareNormalizedResultsStructural(promResult.Data, subjectResult.Data)
		} else {
			compareErr = CompareNormalizedResults(promResult.Data, subjectResult.Data)
		}
		if compareErr != nil {
			return "diff", compareErr
		}
	case "error":
		if promResult.Status != "error" {
			return "error", fmt.Errorf("expected error from Prometheus for %q but got %s", spec.Name, promResult.Status)
		}
		if subjectResult.Status != "error" {
			return "error", fmt.Errorf("expected error from subject %s for %q but got %s", subject, spec.Name, subjectResult.Status)
		}
		if expectedType := strings.TrimSpace(spec.ExpectedErrorType); expectedType != "" {
			if promResult.ErrorType != expectedType {
				return "error", fmt.Errorf("unexpected Prometheus errorType for %q: expected %q got %q", spec.Name, expectedType, promResult.ErrorType)
			}
			if subjectResult.ErrorType != expectedType {
				return "error", fmt.Errorf("unexpected subject %s errorType for %q: expected %q got %q", subject, spec.Name, expectedType, subjectResult.ErrorType)
			}
		}
		if expectedContains := strings.TrimSpace(spec.ExpectedErrorContains); expectedContains != "" {
			if !strings.Contains(promResult.Error, expectedContains) {
				return "error", fmt.Errorf("expected Prometheus error for %q to contain %q, got %q", spec.Name, expectedContains, promResult.Error)
			}
			if !strings.Contains(subjectResult.Error, expectedContains) {
				return "error", fmt.Errorf("expected subject %s error for %q to contain %q, got %q", subject, spec.Name, expectedContains, subjectResult.Error)
			}
		}
	default:
		return "error", fmt.Errorf("unsupported expectedStatus %q for %q", spec.ExpectedStatus, spec.Name)
	}

	return "ok", nil
}

func waitForDatasetAvailability(ctx context.Context, client *http.Client, cfg CompareConfig, manifests []Manifest, subjects []compareSubject) error {
	deadline, hasDeadline := ctx.Deadline()
	for {
		allReady := true
		lastPromErr := error(nil)
		lastSubjectErr := error(nil)
		lastSubjectName := ""
		lastDatasetVariant := ""
		for _, manifest := range manifests {
			probe := QuerySpec{Name: "probe", Endpoint: "query", Query: `harness_up{job="api"}`, TimeOffsetSeconds: int64((manifest.Points - 1)) * manifest.StepSeconds}
			promResult, promErr := QueryAndNormalize(client, cfg.PrometheusBaseURL, manifest, probe)
			if promErr != nil || len(promResult.Vector) == 0 {
				allReady = false
				lastPromErr = promErr
				lastDatasetVariant = manifest.DatasetVariant
			}
			for _, subject := range subjects {
				subjectResult, subjectErr := QueryAndNormalize(client, subject.BaseURL, manifest, probe)
				if subjectErr != nil || len(subjectResult.Vector) == 0 {
					allReady = false
					lastSubjectErr = subjectErr
					lastSubjectName = subject.Name
					lastDatasetVariant = manifest.DatasetVariant
				}
			}
		}
		if allReady {
			return nil
		}
		if hasDeadline && time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for seeded dataset availability: variant=%s prom=%v %s=%v", lastDatasetVariant, lastPromErr, lastSubjectName, lastSubjectErr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for seeded dataset availability: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

type causeClusterAccumulator struct {
	id              string
	name            string
	severity        string
	bucket          string
	detail          string
	count           int
	variants        []string
	subjects        []string
	datasetVariants []string
	rows            []int
}

func annotateCauseClusters(report *CompareReport) {
	if report == nil || len(report.Results) == 0 {
		return
	}
	clusters := map[string]*causeClusterAccumulator{}
	ordered := make([]*causeClusterAccumulator, 0)
	perQueryCauseIndex := map[string]int{}
	for index := range report.Results {
		result := &report.Results[index]
		if result.Status == "ok" {
			continue
		}
		key := strings.Join([]string{result.Name, result.Severity, result.Bucket, result.Detail}, "\x00")
		cluster, ok := clusters[key]
		if !ok {
			perQueryCauseIndex[result.Name]++
			cluster = &causeClusterAccumulator{
				id:       fmt.Sprintf("%s/cause-%d", result.Name, perQueryCauseIndex[result.Name]),
				name:     result.Name,
				severity: result.Severity,
				bucket:   result.Bucket,
				detail:   result.Detail,
			}
			clusters[key] = cluster
			ordered = append(ordered, cluster)
		}
		cluster.count++
		cluster.rows = append(cluster.rows, index)
		cluster.variants = appendUniqueString(cluster.variants, result.Variant)
		cluster.subjects = appendUniqueString(cluster.subjects, result.Subject)
		cluster.datasetVariants = appendUniqueString(cluster.datasetVariants, result.DatasetVariant)
	}
	if len(ordered) == 0 {
		return
	}
	report.Clusters = make([]CompareCauseCluster, 0, len(ordered))
	for _, cluster := range ordered {
		for _, row := range cluster.rows {
			report.Results[row].CauseCluster = cluster.id
			report.Results[row].CauseClusterSize = cluster.count
		}
		report.Clusters = append(report.Clusters, CompareCauseCluster{
			ID:              cluster.id,
			Name:            cluster.name,
			Severity:        cluster.severity,
			Bucket:          cluster.bucket,
			Detail:          cluster.detail,
			Count:           cluster.count,
			Variants:        cloneStrings(cluster.variants),
			Subjects:        cloneStrings(cluster.subjects),
			DatasetVariants: cloneStrings(cluster.datasetVariants),
		})
	}
}

func appendUniqueString(values []string, candidate string) []string {
	if strings.TrimSpace(candidate) == "" {
		return values
	}
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func cloneStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

func writeCompareReport(dir string, report CompareReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(filepath.Join(dir, "compare-report.json"), payload, 0o644)
}

func CompareNormalizedResults(left, right normalizedResult) error {
	if left.ResultType != right.ResultType {
		return fmt.Errorf("resultType mismatch: %s vs %s", left.ResultType, right.ResultType)
	}
	switch left.ResultType {
	case "scalar":
		if !equalFloat(left.Scalar.Timestamp, right.Scalar.Timestamp) || !equalFloat(left.Scalar.Value, right.Scalar.Value) {
			return fmt.Errorf("scalar mismatch: %#v vs %#v", left.Scalar, right.Scalar)
		}
	case "vector":
		if len(left.Vector) != len(right.Vector) {
			return fmt.Errorf("vector length mismatch: %d vs %d", len(left.Vector), len(right.Vector))
		}
		for i := range left.Vector {
			if labelKey(left.Vector[i].Metric) != labelKey(right.Vector[i].Metric) || !equalFloat(left.Vector[i].Timestamp, right.Vector[i].Timestamp) || !equalFloat(left.Vector[i].Value, right.Vector[i].Value) {
				return fmt.Errorf("vector row mismatch at %d: %#v vs %#v", i, left.Vector[i], right.Vector[i])
			}
		}
	case "matrix":
		if len(left.Matrix) != len(right.Matrix) {
			return fmt.Errorf("matrix series length mismatch: %d vs %d", len(left.Matrix), len(right.Matrix))
		}
		for i := range left.Matrix {
			if labelKey(left.Matrix[i].Metric) != labelKey(right.Matrix[i].Metric) {
				return fmt.Errorf("matrix series labels mismatch at %d: %#v vs %#v", i, left.Matrix[i].Metric, right.Matrix[i].Metric)
			}
			if len(left.Matrix[i].Values) != len(right.Matrix[i].Values) {
				return fmt.Errorf("matrix points length mismatch at %d: %d vs %d", i, len(left.Matrix[i].Values), len(right.Matrix[i].Values))
			}
			for j := range left.Matrix[i].Values {
				if !equalFloat(left.Matrix[i].Values[j].Timestamp, right.Matrix[i].Values[j].Timestamp) || !equalFloat(left.Matrix[i].Values[j].Value, right.Matrix[i].Values[j].Value) {
					return fmt.Errorf("matrix point mismatch at series %d point %d: %#v vs %#v", i, j, left.Matrix[i].Values[j], right.Matrix[i].Values[j])
				}
			}
		}
	default:
		return fmt.Errorf("unsupported result type %q", left.ResultType)
	}
	return nil
}

func equalFloat(left, right float64) bool {
	if math.IsNaN(left) && math.IsNaN(right) {
		return true
	}
	return math.Abs(left-right) <= 1e-9
}

// CompareNormalizedResultsStructural checks result type, series set, labels,
// timestamp alignment, and NaN positions — without asserting exact float value
// equality. Used for rate-family queries where ClickHouse vs Prometheus value
// divergence is expected semantics, but structural breakage still indicates a
// real defect (missing series, label drift, dropped points).
func CompareNormalizedResultsStructural(left, right normalizedResult) error {
	if left.ResultType != right.ResultType {
		return fmt.Errorf("resultType mismatch: %s vs %s", left.ResultType, right.ResultType)
	}
	switch left.ResultType {
	case "scalar":
		if !equalFloat(left.Scalar.Timestamp, right.Scalar.Timestamp) {
			return fmt.Errorf("scalar timestamp mismatch: %v vs %v", left.Scalar.Timestamp, right.Scalar.Timestamp)
		}
		if math.IsNaN(left.Scalar.Value) != math.IsNaN(right.Scalar.Value) {
			return fmt.Errorf("scalar NaN-position mismatch: %v vs %v", left.Scalar.Value, right.Scalar.Value)
		}
	case "vector":
		if len(left.Vector) != len(right.Vector) {
			return fmt.Errorf("vector length mismatch: %d vs %d", len(left.Vector), len(right.Vector))
		}
		for i := range left.Vector {
			if labelKey(left.Vector[i].Metric) != labelKey(right.Vector[i].Metric) {
				return fmt.Errorf("vector labels mismatch at %d: %#v vs %#v", i, left.Vector[i].Metric, right.Vector[i].Metric)
			}
			if !equalFloat(left.Vector[i].Timestamp, right.Vector[i].Timestamp) {
				return fmt.Errorf("vector timestamp mismatch at %d: %v vs %v", i, left.Vector[i].Timestamp, right.Vector[i].Timestamp)
			}
			if math.IsNaN(left.Vector[i].Value) != math.IsNaN(right.Vector[i].Value) {
				return fmt.Errorf("vector NaN-position mismatch at %d", i)
			}
		}
	case "matrix":
		if len(left.Matrix) != len(right.Matrix) {
			return fmt.Errorf("matrix series length mismatch: %d vs %d", len(left.Matrix), len(right.Matrix))
		}
		for i := range left.Matrix {
			if labelKey(left.Matrix[i].Metric) != labelKey(right.Matrix[i].Metric) {
				return fmt.Errorf("matrix series labels mismatch at %d: %#v vs %#v", i, left.Matrix[i].Metric, right.Matrix[i].Metric)
			}
			if len(left.Matrix[i].Values) != len(right.Matrix[i].Values) {
				return fmt.Errorf("matrix points length mismatch at %d: %d vs %d", i, len(left.Matrix[i].Values), len(right.Matrix[i].Values))
			}
			for j := range left.Matrix[i].Values {
				if !equalFloat(left.Matrix[i].Values[j].Timestamp, right.Matrix[i].Values[j].Timestamp) {
					return fmt.Errorf("matrix timestamp mismatch at series %d point %d", i, j)
				}
				if math.IsNaN(left.Matrix[i].Values[j].Value) != math.IsNaN(right.Matrix[i].Values[j].Value) {
					return fmt.Errorf("matrix NaN-position mismatch at series %d point %d", i, j)
				}
			}
		}
	default:
		return fmt.Errorf("unsupported result type %q", left.ResultType)
	}
	return nil
}

// ClassifyComparison maps a status+detail pair onto a severity/bucket pair.
// Severities:
//
//	p0   — dashboards break (query errors, promshim unsupported/unimplemented)
//	p1   — silent data loss (series set / label / timestamp drift)
//	p2   — cosmetic value differences (only emitted in "exact" mode)
//	info — the corpus query is itself invalid (Prometheus rejected it)
//	ok   — no issue
func ClassifyComparison(status, detail string) (severity, bucket string) {
	if status == "ok" {
		return "ok", "ok"
	}
	d := detail
	switch {
	case strings.Contains(d, "got error: unsupported PromQL"):
		return "p0", "shim-unsupported"
	case strings.Contains(d, "NOT_IMPLEMENTED"), strings.Contains(d, "is not implemented"):
		return "p0", "shim-unimplemented"
	case strings.Contains(d, "expected success from Prometheus") && strings.Contains(d, "parse error"):
		return "info", "prom-parse-error"
	case strings.Contains(d, "expected success from Prometheus"):
		return "info", "prom-rejected"
	case strings.Contains(d, "expected success from subject "):
		return "p0", "subject-error"
	case strings.Contains(d, "series length mismatch"),
		strings.Contains(d, "vector length mismatch"),
		strings.Contains(d, "series labels mismatch"),
		strings.Contains(d, "labels mismatch"),
		strings.Contains(d, "points length mismatch"):
		return "p1", "series-mismatch"
	case strings.Contains(d, "timestamp mismatch"):
		return "p1", "timestamp-mismatch"
	case strings.Contains(d, "NaN-position mismatch"):
		return "p1", "nan-drift"
	case strings.Contains(d, "point mismatch"), strings.Contains(d, "row mismatch"), strings.Contains(d, "scalar mismatch"):
		return "p2", "value-diff"
	case strings.Contains(d, "resultType mismatch"):
		return "p1", "result-type-mismatch"
	default:
		return "p0", "other"
	}
}
