package promharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeSweepRunner map[string]SweepCommandResult

func (r fakeSweepRunner) Run(cwd string, args ...string) SweepCommandResult {
	key := strings.Join(args, " ")
	if result, ok := r[key]; ok {
		return result
	}
	code := 127
	return SweepCommandResult{OK: false, Stderr: "unexpected command: " + key, ReturnCode: &code}
}

func TestBuildSweepArtifactsCollectsReportsAndRelativePaths(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "harness", "artifacts", "bench", "sweeps", "run-a")
	if err := os.MkdirAll(filepath.Join(outDir, "memory-detail-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(outDir, "memory-summary-a.json"), `{}`)
	writeFile(t, filepath.Join(outDir, "memory-detail-a", "manifest.json"), `{}`)
	writeFile(t, filepath.Join(outDir, "clickhouse-profile-a.json"), `{}`)
	writeReport(t, filepath.Join(outDir, "bench-report-a.json"), BenchReportV2{
		SchemaVersion: 2,
		CorpusPath:    "harness/corpus/bench-native-lowering-7d.json",
		RunLabels:     map[string]string{"profile": "7d", "density": "sparse", "transport": "native"},
		Rows: []BenchRowV2{
			{
				Name:     "slow_rate",
				Category: "rate",
				PromBand: "fast",
				Prom:     &BenchTiming{P50MS: 2},
				Shim: map[string]BenchShimModeResult{
					"prefer": {BenchTiming: BenchTiming{P50MS: 8}, RoutingPolicy: "strict", Strategy: "native_sql"},
					"off":    {BenchTiming: BenchTiming{P50MS: 4}, RoutingPolicy: "strict", Strategy: "local"},
				},
			},
		},
		Summary: BenchSummary{StrategyHistogram: map[string]int{"native_sql": 1, "local": 1}},
	})
	writeFile(t, filepath.Join(outDir, "bench-report-legacy.json"), `{"rows":[]}`)
	zero := 0
	runner := fakeSweepRunner{
		"git status --porcelain":                              {OK: true, Stdout: " M scripts/run-sweep.sh\n?? scratch\n", ReturnCode: &zero},
		"git rev-parse HEAD":                                  {OK: true, Stdout: "abcdef", ReturnCode: &zero},
		"docker compose -f docker-compose.yml ps -q promshim": {OK: true, Stdout: "container123", ReturnCode: &zero},
		"docker inspect container123":                         {OK: true, Stdout: `[{"Name":"/promshim","Created":"2026-01-02T03:04:05Z","Image":"sha256:abc","Config":{"Image":"promshim:latest"}}]`, ReturnCode: &zero},
	}
	opts := baseSweepOptions(root, "harness/artifacts/bench/sweeps/run-a")
	opts.CommandRunner = runner
	opts.Now = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := BuildSweepArtifacts(opts); err != nil {
		t.Fatalf("BuildSweepArtifacts: %v", err)
	}

	var manifest SweepManifest
	readJSON(t, filepath.Join(outDir, "manifest.json"), &manifest)
	if manifest.GeneratedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("generatedAt = %q", manifest.GeneratedAt)
	}
	if got, want := manifest.Bench.MemoryReports, []string{"harness/artifacts/bench/sweeps/run-a/memory-summary-a.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("memory reports = %#v, want %#v", got, want)
	}
	if got := manifest.Bench.Reports; len(got) != 1 || got[0].Path != "harness/artifacts/bench/sweeps/run-a/bench-report-a.json" || got[0].RowCount != 1 {
		t.Fatalf("reports = %#v", got)
	}
	if got, want := manifest.Axes.RoutingPolicies, []string{"strict"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routing policies = %#v, want %#v", got, want)
	}
	promshim := manifest.BenchmarkStack["promshim"].(map[string]interface{})
	if promshim["available"] != true || promshim["containerId"] != "container123" {
		t.Fatalf("promshim provenance = %#v", promshim)
	}

	var summary SweepSummary
	readJSON(t, filepath.Join(outDir, "summary.json"), &summary)
	if summary.ReportCount != 1 || summary.MemoryReportCount != 1 || summary.MemoryDetailCount != 1 || summary.ClickHouseProfileCount != 1 {
		t.Fatalf("bad counts: %#v", summary)
	}
	if summary.StrategyHistogram["native_sql"] != 1 || summary.RoutingPolicyHistogram["strict"] != 2 || summary.TargetBands["fast"] != 1 {
		t.Fatalf("bad histograms: %#v", summary)
	}
	if len(summary.TopSlowRows) != 2 || summary.TopSlowRows[0].Mode != "prefer" || summary.TopSlowRows[0].ShimPromRatio == nil || *summary.TopSlowRows[0].ShimPromRatio != 4 {
		t.Fatalf("bad slow rows: %#v", summary.TopSlowRows)
	}
	content, err := os.ReadFile(filepath.Join(outDir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "| slow_rate | prefer | strict | native_sql | 2.00 | 8.00 | 4.0× |") {
		t.Fatalf("summary.md missing slow row:\n%s", string(content))
	}
}

func TestBuildSweepArtifactsRecordsUnavailableProvenance(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "harness", "artifacts", "bench", "sweeps", "run-b")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	code := 1
	runner := fakeSweepRunner{
		"git status --porcelain":                              {OK: true},
		"git rev-parse HEAD":                                  {OK: true, Stdout: "abcdef"},
		"docker compose -f docker-compose.yml ps -q promshim": {OK: false, Stderr: "docker down", ReturnCode: &code},
	}
	opts := baseSweepOptions(root, "harness/artifacts/bench/sweeps/run-b")
	opts.CommandRunner = runner
	if err := BuildSweepArtifacts(opts); err != nil {
		t.Fatalf("BuildSweepArtifacts: %v", err)
	}
	var manifest SweepManifest
	readJSON(t, filepath.Join(outDir, "manifest.json"), &manifest)
	promshim := manifest.BenchmarkStack["promshim"].(map[string]interface{})
	if promshim["available"] != false || promshim["reason"] != "compose_ps_failed" || promshim["stderr"] != "docker down" {
		t.Fatalf("promshim provenance = %#v", promshim)
	}
}

func TestBuildSweepArtifactsAllowsEmptyArtifactDir(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "harness", "artifacts", "bench", "sweeps", "empty")
	opts := baseSweepOptions(root, "harness/artifacts/bench/sweeps/empty")
	opts.BenchStatus = "skipped"
	opts.ComplianceStatus = "skipped"
	opts.CommandRunner = fakeSweepRunner{
		"git status --porcelain": {OK: true},
		"git rev-parse HEAD":     {OK: true, Stdout: "abcdef"},
	}
	if err := BuildSweepArtifacts(opts); err != nil {
		t.Fatalf("BuildSweepArtifacts: %v", err)
	}
	var summary SweepSummary
	readJSON(t, filepath.Join(outDir, "summary.json"), &summary)
	if summary.ReportCount != 0 || len(summary.StrategyHistogram) != 0 || len(summary.TopSlowRows) != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	content, err := os.ReadFile(filepath.Join(outDir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "No benchmark strategy data captured") {
		t.Fatalf("summary.md = %s", string(content))
	}
}

func baseSweepOptions(root, artifactDir string) SweepArtifactOptions {
	return SweepArtifactOptions{
		RepoRoot:                   root,
		ArtifactDir:                artifactDir,
		RunName:                    filepath.Base(artifactDir),
		Profile:                    "7d",
		Density:                    "sparse",
		Transport:                  "native",
		SeedPolicy:                 "reuse",
		ShimModes:                  "prefer,force_supported,off",
		RoutingPolicies:            "",
		IncludeProm:                "true",
		CorpusSet:                  "native",
		ComplianceStatus:           "passed",
		BenchStatus:                "passed",
		PromURL:                    "http://localhost:29190",
		ShimURL:                    "http://localhost:29191",
		ClickHouseURL:              "http://localhost:28124",
		MemoryMode:                 "summary",
		ClickHouseProfileMode:      "summary",
		ClickHouseReferenceProfile: "default-benchmark-compose",
		SettingsProfile:            "default_safe",
	}
}

func writeReport(t *testing.T, path string, report BenchReportV2) {
	t.Helper()
	content, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(content))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string, dest interface{}) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, dest); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", path, err, string(content))
	}
}
