package promharness

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEffectiveQuerySpecAppliesDefaultNativeLoweringMode(t *testing.T) {
	spec := effectiveQuerySpec(CompareConfig{DefaultNativeLoweringMode: "force_supported"}, QuerySpec{Name: "native-row", Endpoint: "query", Query: "harness_up"})
	if spec.NativeLoweringMode != "force_supported" {
		t.Fatalf("expected default native lowering mode to be applied, got %#v", spec)
	}

	explicit := effectiveQuerySpec(CompareConfig{DefaultNativeLoweringMode: "force_supported"}, QuerySpec{Name: "explicit-row", Endpoint: "query", Query: "harness_up", NativeLoweringMode: "shadow"})
	if explicit.NativeLoweringMode != "shadow" {
		t.Fatalf("expected explicit native lowering mode to win, got %#v", explicit)
	}
}

func TestCompareQueryOutcomeSupportsExpectedErrorMatching(t *testing.T) {
	status, err := CompareQueryOutcome(QuerySpec{
		Name:                  "matrix-root-range-query",
		ExpectedStatus:        "error",
		ExpectedErrorType:     "bad_data",
		ExpectedErrorContains: "invalid expression type",
	},
		"shim",
		queryResult{Status: "error", ErrorType: "bad_data", Error: "query execution failed: invalid expression type in query_range"},
		queryResult{Status: "error", ErrorType: "bad_data", Error: "query execution failed: invalid expression type"},
	)
	if err != nil {
		t.Fatalf("expected expected-error outcome to pass, got err: %v", err)
	}
	if status != "ok" {
		t.Fatalf("expected query outcome status ok, got %q", status)
	}
}

func TestQueryAndFetchCapturesRecordingRuleMetadata(t *testing.T) {
	manifest := Manifest{BaseUnixSeconds: 1700000000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[1700000000,"1"]}],"recordingRules":[{"record":"job:http_requests:rate5m","mode":"instant_virtual"},{"record":"job:http_requests:rate5m","mode":"range_virtual","virtualRange":"5m","virtualStep":"30s"}]}}`)
	}))
	defer server.Close()

	result, err := QueryAndFetch(server.Client(), server.URL, manifest, QuerySpec{Endpoint: "query", Query: "job:http_requests:rate5m", Explain: true})
	if err != nil {
		t.Fatalf("expected query fetch success, got %v", err)
	}
	if !result.RecordingRuleExpanded {
		t.Fatalf("expected recording rule expansion metadata, got %#v", result)
	}
	if result.RecordingRuleMode != "instant_virtual,range_virtual" {
		t.Fatalf("expected ordered modes, got %q", result.RecordingRuleMode)
	}
	if !result.RecordingRuleRangeExpansion {
		t.Fatalf("expected range expansion metadata to be true")
	}
}

func TestQueryAndFetchCapturesRecordingRuleRejectionReason(t *testing.T) {
	manifest := Manifest{BaseUnixSeconds: 1700000000}
	sentinel := "recording rule is ambiguous across multiple matches"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"error","errorType":"bad_data","error":%q}`, sentinel)
	}))
	defer server.Close()

	result, err := QueryAndFetch(server.Client(), server.URL, manifest, QuerySpec{Endpoint: "query", Query: "ambiguous:rule", Explain: true})
	if err != nil {
		t.Fatalf("expected query fetch transport success, got %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %#v", result.Status)
	}
	if result.RecordingRuleRejectionReason != sentinel {
		t.Fatalf("expected rejection reason %q, got %q", sentinel, result.RecordingRuleRejectionReason)
	}
}

func TestCompareQueryOutcomeRejectsUnexpectedErrorTypeForExpectedErrorQuery(t *testing.T) {
	_, err := CompareQueryOutcome(QuerySpec{
		Name:              "unsupported-error",
		ExpectedStatus:    "error",
		ExpectedErrorType: "unsupported",
	},
		"shim",
		queryResult{Status: "error", ErrorType: "bad_data", Error: "bad_data error"},
		queryResult{Status: "error", ErrorType: "unsupported", Error: "unsupported function"},
	)
	if err == nil {
		t.Fatal("expected error-type mismatch to fail")
	}
}

func TestCompareNormalizedResultsTreatsNaNAsEqual(t *testing.T) {
	left := normalizedResult{ResultType: "scalar", Scalar: &normalizedScalar{Timestamp: 1, Value: math.NaN()}}
	right := normalizedResult{ResultType: "scalar", Scalar: &normalizedScalar{Timestamp: 1, Value: math.NaN()}}
	if err := CompareNormalizedResults(left, right); err != nil {
		t.Fatalf("expected NaN scalars to compare equal, got %v", err)
	}
}

func TestCompareNormalizedResultsRejectsDifferentVectorValues(t *testing.T) {
	left := normalizedResult{ResultType: "vector", Vector: []normalizedVectorSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 1}}}
	right := normalizedResult{ResultType: "vector", Vector: []normalizedVectorSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 2}}}
	if err := CompareNormalizedResults(left, right); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestConfiguredSubjectsHonorsGlobalFilter(t *testing.T) {
	subjects := configuredSubjects(CompareConfig{PromshimBaseURL: "http://shim", PromClickBaseURL: "http://promclick", Subjects: []string{"shim"}})
	if len(subjects) != 1 || subjects[0].Name != "shim" {
		t.Fatalf("expected only shim subject, got %#v", subjects)
	}
}

func TestSubjectsForQueryHonorsPerQueryFilter(t *testing.T) {
	configured := []compareSubject{{Name: "shim", BaseURL: "http://shim"}, {Name: "promclick", BaseURL: "http://promclick"}}
	subjects, err := subjectsForQuery(QuerySpec{Name: "phase6-row", Subjects: []string{"shim"}}, configured)
	if err != nil {
		t.Fatalf("expected per-query subject filter to resolve, got %v", err)
	}
	if len(subjects) != 1 || subjects[0].Name != "shim" {
		t.Fatalf("expected only shim subject, got %#v", subjects)
	}
}

func TestSubjectsForQueryRejectsUnavailableSubjects(t *testing.T) {
	configured := []compareSubject{{Name: "shim", BaseURL: "http://shim"}}
	if _, err := subjectsForQuery(QuerySpec{Name: "phase6-row", Subjects: []string{"promclick"}}, configured); err == nil {
		t.Fatal("expected unavailable subject filter to fail")
	}
}

func TestManifestsForQueryHonorsDatasetVariantFilter(t *testing.T) {
	configured := []Manifest{{DatasetVariant: "baseline"}, {DatasetVariant: "resets_gaps"}, {DatasetVariant: "histogram_burst"}}
	manifests, err := manifestsForQuery(QuerySpec{Name: "phase12-row", DatasetVariants: []string{"baseline", "histogram_burst"}, ExcludeDatasetVariants: []string{"baseline"}}, configured)
	if err != nil {
		t.Fatalf("expected dataset variant filter to resolve, got %v", err)
	}
	if len(manifests) != 1 || manifests[0].DatasetVariant != "histogram_burst" {
		t.Fatalf("expected only histogram_burst manifest, got %#v", manifests)
	}
}

func TestManifestsForQueryRejectsUnavailableDatasetVariants(t *testing.T) {
	configured := []Manifest{{DatasetVariant: "baseline"}}
	if _, err := manifestsForQuery(QuerySpec{Name: "phase12-row", DatasetVariants: []string{"resets_gaps"}}, configured); err == nil {
		t.Fatal("expected unavailable dataset variant filter to fail")
	}
}

func TestManifestsForQueryTreatsLegacySingleManifestAsBaseline(t *testing.T) {
	configured := []Manifest{{BaseUnixSeconds: 1700000000, StepSeconds: 60, Points: 10}}
	manifests, err := manifestsForQuery(QuerySpec{Name: "phase12-row", DatasetVariants: []string{"baseline"}}, configured)
	if err != nil {
		t.Fatalf("expected legacy single manifest baseline selection to resolve, got %v", err)
	}
	if len(manifests) != 1 || manifests[0].DatasetVariant != "" {
		t.Fatalf("expected legacy single manifest to match baseline selection, got %#v", manifests)
	}
}

func TestRunCompareExpandsNamedVariantsIntoReportRows(t *testing.T) {
	tempDir := t.TempDir()
	manifest := Manifest{BaseUnixSeconds: 1700000000, StepSeconds: 60, Points: 10}
	if err := WriteManifest(ManifestPath(tempDir), manifest); err != nil {
		t.Fatal(err)
	}
	corpusPath := filepath.Join(tempDir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`[
  {
    "name": "variant_query",
    "endpoint": "query",
    "query": "up",
    "timeOffsets": [
      {"name": "early", "timeOffsetSeconds": 60},
      {"name": "late", "timeOffsetSeconds": 540}
    ],
    "subjects": ["shim"]
  }
]`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[1,"1"]}]}}`)
	}))
	defer server.Close()

	report, err := RunCompare(contextWithTimeout(t), CompareConfig{
		PrometheusBaseURL: server.URL,
		PromshimBaseURL:   server.URL,
		CorpusPath:        corpusPath,
		ArtifactDir:       tempDir,
		Timeout:           2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("expected two compare results for two variants, got %#v", report.Results)
	}
	if report.Results[0].Name != "variant_query" || report.Results[0].Variant != "early" || report.Results[0].Status != "ok" {
		t.Fatalf("unexpected first variant row: %#v", report.Results[0])
	}
	if report.Results[1].Name != "variant_query" || report.Results[1].Variant != "late" || report.Results[1].Status != "ok" {
		t.Fatalf("unexpected second variant row: %#v", report.Results[1])
	}
}

func TestRunCompareExpandsManifestDatasetVariantsIntoReportRows(t *testing.T) {
	tempDir := t.TempDir()
	manifest := Manifest{Variants: []Manifest{
		{Seed: 12345, BaseUnixSeconds: 1700000000, StepSeconds: 60, Points: 10, DatasetVariant: "baseline"},
		{Seed: 12345, BaseUnixSeconds: 1700003600, StepSeconds: 60, Points: 10, DatasetVariant: "resets_gaps"},
	}}
	if err := WriteManifest(ManifestPath(tempDir), manifest); err != nil {
		t.Fatal(err)
	}
	corpusPath := filepath.Join(tempDir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`[{"name":"dataset_variant_query","endpoint":"query","query":"up","subjects":["shim"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[1,"1"]}]}}`)
	}))
	defer server.Close()

	report, err := RunCompare(contextWithTimeout(t), CompareConfig{
		PrometheusBaseURL: server.URL,
		PromshimBaseURL:   server.URL,
		CorpusPath:        corpusPath,
		ArtifactDir:       tempDir,
		Timeout:           2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("expected two compare results for two dataset variants, got %#v", report.Results)
	}
	if report.Results[0].DatasetVariant != "baseline" || report.Results[1].DatasetVariant != "resets_gaps" {
		t.Fatalf("unexpected dataset variants in report rows: %#v", report.Results)
	}
}

func TestRunCompareCapturesRecordingRuleMetadata(t *testing.T) {
	tempDir := t.TempDir()
	manifest := Manifest{BaseUnixSeconds: 1700000000, StepSeconds: 60, Points: 10}
	if err := WriteManifest(ManifestPath(tempDir), manifest); err != nil {
		t.Fatal(err)
	}
	corpusPath := filepath.Join(tempDir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`[
  {
    "name": "virtual_query",
    "endpoint": "query",
    "query": "sum_up{query=\"virtual\"}",
    "explain": true,
    "subjects": ["shim"],
    "expectedStatus": "ok"
  },
  {
    "name": "virtual_query_error",
    "endpoint": "query",
    "query": "ambiguous_virtual_query",
    "explain": true,
    "subjects": ["shim"],
    "expectedStatus": "error",
    "expectedErrorType": "bad_data",
    "expectedErrorContains": "ambiguous"
  }
]`), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := "recording rule \"ambiguous\" is ambiguous across 2 definitions"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("query")
		if query == "ambiguous_virtual_query" {
			_, _ = fmt.Fprintf(w, `{"status":"error","errorType":"bad_data","error":%q}`, sentinel)
			return
		}
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[1,"1"]}],"recordingRules":[{"record":"virtual:metric","mode":"instant_virtual"}]}}`)
	}))
	defer server.Close()

	report, err := RunCompare(contextWithTimeout(t), CompareConfig{
		PrometheusBaseURL: server.URL,
		PromshimBaseURL:   server.URL,
		CorpusPath:        corpusPath,
		ArtifactDir:       tempDir,
		Timeout:           2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatalf("expected two rows with recording-rule metadata coverage, got %#v", report.Results)
	}
	first := report.Results[0]
	if !first.RecordingRuleExpanded || first.RecordingRuleMode != "instant_virtual" || first.RecordingRuleRangeExpansion {
		t.Fatalf("unexpected recording-rule metadata on ok row: %#v", first)
	}
	second := report.Results[1]
	if second.RecordingRuleRejectionReason != sentinel {
		t.Fatalf("expected rejection reason for error row, got %#v", second)
	}
}

func TestRunCompareHonorsPerQueryDatasetVariantFilter(t *testing.T) {
	tempDir := t.TempDir()
	manifest := Manifest{Variants: []Manifest{
		{Seed: 12345, BaseUnixSeconds: 1700000000, StepSeconds: 60, Points: 10, DatasetVariant: "baseline"},
		{Seed: 12345, BaseUnixSeconds: 1700003600, StepSeconds: 60, Points: 10, DatasetVariant: "resets_gaps"},
		{Seed: 12345, BaseUnixSeconds: 1700007200, StepSeconds: 60, Points: 10, DatasetVariant: "histogram_burst"},
	}}
	if err := WriteManifest(ManifestPath(tempDir), manifest); err != nil {
		t.Fatal(err)
	}
	corpusPath := filepath.Join(tempDir, "corpus.json")
	if err := os.WriteFile(corpusPath, []byte(`[{"name":"dataset_variant_query","endpoint":"query","query":"up","datasetVariants":["baseline","histogram_burst"],"excludeDatasetVariants":["baseline"],"subjects":["shim"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[1,"1"]}]}}`)
	}))
	defer server.Close()

	report, err := RunCompare(contextWithTimeout(t), CompareConfig{
		PrometheusBaseURL: server.URL,
		PromshimBaseURL:   server.URL,
		CorpusPath:        corpusPath,
		ArtifactDir:       tempDir,
		Timeout:           2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].DatasetVariant != "histogram_burst" {
		t.Fatalf("expected only histogram_burst compare row, got %#v", report.Results)
	}
}

func TestAnnotateCauseClustersGroupsRepeatedVariantFailures(t *testing.T) {
	report := CompareReport{Results: []QueryComparison{
		{Name: "base_query", Variant: "early", DatasetVariant: "baseline", Subject: "shim", Status: "diff", Severity: "p1", Bucket: "series-mismatch", Detail: "matrix points length mismatch"},
		{Name: "base_query", Variant: "late", DatasetVariant: "baseline", Subject: "shim", Status: "diff", Severity: "p1", Bucket: "series-mismatch", Detail: "matrix points length mismatch"},
		{Name: "base_query", Variant: "late", DatasetVariant: "resets_gaps", Subject: "promclick", Status: "diff", Severity: "p1", Bucket: "series-mismatch", Detail: "matrix points length mismatch"},
		{Name: "other_query", Subject: "shim", Status: "ok", Severity: "ok", Bucket: "ok"},
	}}

	annotateCauseClusters(&report)

	if len(report.Clusters) != 1 {
		t.Fatalf("expected one cause cluster, got %#v", report.Clusters)
	}
	cluster := report.Clusters[0]
	if cluster.ID != "base_query/cause-1" || cluster.Count != 3 {
		t.Fatalf("unexpected cluster summary: %#v", cluster)
	}
	if len(cluster.Variants) != 2 || cluster.Variants[0] != "early" || cluster.Variants[1] != "late" {
		t.Fatalf("unexpected cluster variants: %#v", cluster)
	}
	if len(cluster.Subjects) != 2 {
		t.Fatalf("unexpected cluster subjects: %#v", cluster)
	}
	if len(cluster.DatasetVariants) != 2 {
		t.Fatalf("unexpected cluster dataset variants: %#v", cluster)
	}
	for _, row := range report.Results[:3] {
		if row.CauseCluster != "base_query/cause-1" || row.CauseClusterSize != 3 {
			t.Fatalf("expected clustered failing row, got %#v", row)
		}
	}
	if report.Results[3].CauseCluster != "" || report.Results[3].CauseClusterSize != 0 {
		t.Fatalf("expected ok row to stay uncluttered, got %#v", report.Results[3])
	}
}

func contextWithTimeout(t *testing.T) (ctx context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}
