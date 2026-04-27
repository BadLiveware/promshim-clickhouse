package promharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderBenchMatrixFromSweep(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "harness", "artifacts", "bench", "sweeps", "run", "bench-report.json")
	writeReport(t, reportPath, BenchReportV2{
		SchemaVersion: 2,
		CorpusPath:    "harness/corpus/bench-native-lowering-7d.json",
		RunLabels:     map[string]string{"profile": "7d", "density": "sparse", "transport": "native"},
		Rows: []BenchRowV2{{
			Name:     "rate_5m",
			Category: "instant_rate_short",
			PromBand: "fast",
			Prom:     &BenchTiming{P50MS: 10},
			Shim: map[string]BenchShimModeResult{
				"prefer": {BenchTiming: BenchTiming{P50MS: 15}, RoutingPolicy: "strict", Strategy: "native_sql", StrictCandidate: "native", SelectedCandidate: "local", ServedCandidate: "local"},
			},
		}},
		Summary: BenchSummary{StrategyHistogram: map[string]int{"native_sql": 1}},
	})
	manifest := SweepManifest{RunName: "run", Bench: SweepBench{Reports: []SweepBenchReport{{Path: "harness/artifacts/bench/sweeps/run/bench-report.json"}}}}
	manifestPath := filepath.Join(root, "harness", "artifacts", "bench", "sweeps", "run", "manifest.json")
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifestPath, string(content))
	out, err := RenderBenchMatrix(BenchMatrixOptions{RepoRoot: root, SweepPath: manifestPath, PerQuery: true})
	if err != nil {
		t.Fatalf("RenderBenchMatrix: %v", err)
	}
	if !strings.Contains(out, "| instant_rate_short | rate_5m | 7d | sparse | native | bench-native-lowering-7d | prefer | strict | native_sql | native | local | local | yes | fast | 10.00 | 15.00 | 1.5× |") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestRenderBenchMatrixRejectsUnknownSort(t *testing.T) {
	_, err := RenderBenchMatrix(BenchMatrixOptions{SortBy: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown --sort-by") {
		t.Fatalf("expected unknown sort error, got %v", err)
	}
}

func TestRenderBenchMatrixFromLegacyReports(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "bench-report-7d.json")
	report := BenchReport{Rows: []BenchRow{{Name: "rate", Category: "instant_rate", PromP50MS: 10, NativeP50MS: 5, NativePromRatio: 0.5, FallbackNativeRatio: 2}}}
	content, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RenderBenchMatrix(BenchMatrixOptions{Profiles: []BenchMatrixProfileInput{{Profile: "7d", Path: reportPath}}})
	if err != nil {
		t.Fatalf("RenderBenchMatrix: %v", err)
	}
	if !strings.Contains(out, "| instant_rate | 10.00 | 5.00 | 0.5× | 2.0× |") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
