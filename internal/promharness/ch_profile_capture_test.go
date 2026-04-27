package promharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildCHProfileRowsPreservesTotalsAndSamples(t *testing.T) {
	ratio := 2.0
	meta := map[string]CHProfileComment{
		"promshim-bench query=dense mode=prefer policy=strict": {
			QueryName:     "dense",
			Query:         "avg_over_time(x[1h])",
			Endpoint:      "query_range",
			Mode:          "prefer",
			RoutingPolicy: "strict",
			Strategy:      "native_sql",
			ShimP50MS:     40,
			ShimPromRatio: &ratio,
		},
	}
	rows := BuildCHProfileRows([]map[string]any{{
		"logComment":                     "promshim-bench query=dense mode=prefer policy=strict",
		"queryCount":                     10,
		"queryDurationP50Ms":             750,
		"readRowsTotal":                  9_000_000,
		"readRowsP50":                    300_000,
		"joinResultRowCountTotal":        5_000_000_000,
		"joinResultRowCountP50":          4_000,
		"filterTransformPassedRowsTotal": 2_000_000_000,
		"filterTransformPassedRowsP50":   2_000,
		"sampleQueryId":                  "abc",
		"sampleNativeSQL":                "SELECT 1",
	}}, meta)
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	row := rows[0]
	if row.ReadRowsTotal != 9_000_000 || row.ReadRowsP50 != 300_000 || row.JoinResultRowCountTotal != 5_000_000_000 || row.JoinResultRowCountP50 != 4_000 {
		t.Fatalf("row counters not preserved: %#v", row)
	}
	if row.SampleQueryID != "abc" || row.SampleNativeSQL != "SELECT 1" {
		t.Fatalf("sample fields not preserved: %#v", row)
	}
}

func TestWriteMemoryDetailManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "heap-before.pb.gz"), []byte("heap"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteMemoryDetailManifest(dir, "bench-report.json", "http://shim"); err != nil {
		t.Fatalf("WriteMemoryDetailManifest: %v", err)
	}
	var manifest MemoryDetailManifest
	content, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.SourceReport != "bench-report.json" || manifest.PromshimURL != "http://shim" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if len(manifest.Files) != 1 || manifest.Files[0] != "heap-before.pb.gz" {
		t.Fatalf("files = %#v", manifest.Files)
	}
}
