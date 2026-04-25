package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCalibrationFromSweepManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifactDir := filepath.Join(root, "harness", "artifacts", "sweeps", "unit")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reportRel := "harness/artifacts/sweeps/unit/bench-report.json"
	memoryRel := "harness/artifacts/sweeps/unit/memory-summary-bench-report.json"
	writeFixtureJSON(t, filepath.Join(root, reportRel), map[string]any{
		"schemaVersion": 2,
		"corpusPath":    "harness/corpus/unit.json",
		"runLabels": map[string]string{
			"profile":   "7d",
			"density":   "sparse",
			"transport": "native",
		},
		"rows": []any{
			map[string]any{
				"name":     "plain_selector",
				"query":    "up",
				"endpoint": "query",
				"category": "selector_plain",
				"prom":     map[string]any{"p50Ms": 1.0},
				"shim": map[string]any{
					"prefer": map[string]any{"p50Ms": 10.0, "strategy": "native_sql", "routingPolicy": "strict", "costFamily": "selector"},
					"off":    map[string]any{"p50Ms": 5.0, "strategy": "local", "routingPolicy": "strict", "costFamily": "selector"},
				},
			},
		},
	})
	writeFixtureJSON(t, filepath.Join(root, memoryRel), map[string]any{
		"schemaVersion": 1,
		"sourceReport":  filepath.Join(root, reportRel),
		"clickHouseQueryLog": []any{
			map[string]any{"logComment": "promshim-bench query=plain_selector mode=prefer", "selectedRows": 42, "readCompressedBytes": 100, "functionExecute": 7, "memoryP95Bytes": 2048},
		},
	})
	manifestPath := filepath.Join(artifactDir, "manifest.json")
	writeFixtureJSON(t, manifestPath, map[string]any{
		"schemaVersion": 1,
		"runName":       "unit",
		"artifactDir":   "harness/artifacts/sweeps/unit",
		"axes":          map[string]any{"profile": "7d", "density": "sparse", "transport": "native", "shimModes": []string{"prefer", "off"}, "memoryMode": "summary", "corpusSet": "native"},
		"endpoints":     map[string]string{"promshim": "http://localhost:29191"},
		"compliance":    map[string]any{"status": "skipped"},
		"bench":         map[string]any{"reports": []any{map[string]any{"path": reportRel, "profile": "7d", "density": "sparse", "transport": "native"}}, "memoryReports": []string{memoryRel}},
	})

	out, err := buildCalibration([]string{manifestPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Classes) != 1 {
		t.Fatalf("classes = %d, want 1: %+v", len(out.Classes), out.Classes)
	}
	class := out.Classes[0]
	if class.Family != "selector" || class.Recommendation != "local_candidate" {
		t.Fatalf("unexpected class recommendation: %+v", class)
	}
	if class.LocalNativeRatioMedian != 0.5 {
		t.Fatalf("local/native = %v, want 0.5", class.LocalNativeRatioMedian)
	}
	if class.Memory == nil || class.Memory.SelectedRowsMedian != 42 {
		t.Fatalf("memory join missing: %+v", class.Memory)
	}
	md := renderMarkdown(out)
	if !containsAll(md, []string{"sweep `unit`", "local_candidate", "selector", "Strict cand."}) {
		t.Fatalf("markdown missing expected content:\n%s", md)
	}
}

func TestBuildCalibrationMergesMultipleSweeps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestA := writeSimpleSweep(t, root, "run-a", "selector", "prefer", 10.0, 5.0)
	manifestB := writeSimpleSweep(t, root, "run-b", "rate", "prefer@cost_prefer", 20.0, 0)

	out, err := buildCalibration([]string{manifestA, manifestB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(out.Sources))
	}
	if len(out.Classes) < 2 {
		t.Fatalf("classes = %d, want at least 2", len(out.Classes))
	}
}

func TestBuildCalibrationMarksInsufficientData(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := writeSimpleSweep(t, root, "run-insufficient", "rate", "prefer", 11.0, 0)
	out, err := buildCalibration([]string{manifest}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Classes) == 0 {
		t.Fatalf("expected classes")
	}
	found := false
	for _, class := range out.Classes {
		if class.Family == "rate" {
			found = true
			if class.Recommendation != "insufficient_data" {
				t.Fatalf("recommendation=%q, want insufficient_data", class.Recommendation)
			}
			if !containsAll(strings.Join(class.Reasons, " "), []string{"insufficient", "candidate"}) {
				t.Fatalf("reasons=%v, want insufficient candidate data", class.Reasons)
			}
		}
	}
	if !found {
		t.Fatalf("rate class not found: %+v", out.Classes)
	}
}

func writeSimpleSweep(t *testing.T, root, runName, family, mode string, preferP50, offP50 float64) string {
	t.Helper()
	artifactDir := filepath.Join(root, "harness", "artifacts", "sweeps", runName)
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reportRel := filepath.Join("harness", "artifacts", "sweeps", runName, "bench-report.json")
	shim := map[string]any{}
	shim[mode] = map[string]any{"p50Ms": preferP50, "strategy": "native_sql", "routingPolicy": "cost_prefer", "costFamily": family, "strictCandidate": "native_sql", "selectedCandidate": "full_local", "servedCandidate": "full_local"}
	if offP50 > 0 {
		shim["off"] = map[string]any{"p50Ms": offP50, "strategy": "local", "routingPolicy": "strict", "costFamily": family}
	}
	writeFixtureJSON(t, filepath.Join(root, reportRel), map[string]any{
		"schemaVersion": 2,
		"corpusPath":    "harness/corpus/unit.json",
		"runLabels": map[string]string{
			"profile":   "7d",
			"density":   "sparse",
			"transport": "native",
		},
		"rows": []any{map[string]any{"name": "q_" + family, "endpoint": "query", "category": family, "prom": map[string]any{"p50Ms": 1.0}, "shim": shim}},
	})
	memoryRel := filepath.Join("harness", "artifacts", "sweeps", runName, "memory-summary-bench-report.json")
	writeFixtureJSON(t, filepath.Join(root, memoryRel), map[string]any{"schemaVersion": 1, "sourceReport": filepath.Join(root, reportRel), "clickHouseQueryLog": []any{map[string]any{"logComment": "promshim-bench query=q_" + family + " mode=prefer", "selectedRows": 1}}})
	manifestPath := filepath.Join(artifactDir, "manifest.json")
	writeFixtureJSON(t, manifestPath, map[string]any{
		"schemaVersion": 1,
		"runName":       runName,
		"artifactDir":   filepath.Join("harness", "artifacts", "sweeps", runName),
		"axes":          map[string]any{"profile": "7d", "density": "sparse", "transport": "native"},
		"compliance":    map[string]any{"status": "skipped"},
		"bench":         map[string]any{"reports": []any{map[string]any{"path": reportRel, "profile": "7d", "density": "sparse", "transport": "native"},}, "memoryReports": []string{memoryRel}},
	})
	return manifestPath
}

func writeFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsAll(value string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
