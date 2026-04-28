package promharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadQueryCorpusRejectsInvalidRangeOffsets(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "corpus.json")
	payload := `[
		{"name":"bad_range","endpoint":"query_range","query":"up","startOffsetSeconds":3600,"endOffsetSeconds":0,"stepSeconds":30}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	_, err := LoadQueryCorpus(path)
	if err == nil {
		t.Fatal("expected invalid range offset error")
	}
	if !strings.Contains(err.Error(), "endOffsetSeconds >= startOffsetSeconds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadQueryCorpusRejectsNonPositiveRangeStep(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "corpus.json")
	payload := `[
		{"name":"bad_step","endpoint":"query_range","query":"up","startOffsetSeconds":0,"endOffsetSeconds":3600,"stepSeconds":0}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	_, err := LoadQueryCorpus(path)
	if err == nil {
		t.Fatal("expected invalid range step error")
	}
	if !strings.Contains(err.Error(), "stepSeconds > 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadQueryCorpusRejectsUnsupportedEndpoint(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "corpus.json")
	payload := `[
		{"name":"bad_endpoint","endpoint":"query_instantish","query":"up"}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	_, err := LoadQueryCorpus(path)
	if err == nil {
		t.Fatal("expected unsupported endpoint error")
	}
	if !strings.Contains(err.Error(), "unsupported endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}
