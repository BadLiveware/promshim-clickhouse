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
