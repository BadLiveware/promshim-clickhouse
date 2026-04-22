package promshim

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestSQLBuilderAudit_NoLegacySourcePlaceholderReplacement(t *testing.T) {
	root := repoRootFromThisFile(t)
	var hits []string
	legacyReplacePattern := regexp.MustCompile(`strings\.ReplaceAll\([^\n]*,\s*"\{(?:value|tags)\}",`)
	err := filepath.WalkDir(filepath.Join(root, "internal", "promshim"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if legacyReplacePattern.Find(content) != nil {
			hits = append(hits, relToRoot(root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking promshim sources: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("found legacy source placeholder substitution in: %v", hits)
	}
}

func TestSQLBuilderAudit_MigratedFilesAvoidFormattedQueryTemplates(t *testing.T) {
	root := repoRootFromThisFile(t)
	files := []string{
		"internal/promshim/storage/join_sql.go",
		"internal/promshim/native/renderer/renderer.go",
		"internal/promshim/storage/selector_sql.go",
		"internal/promshim/storage/sql.go",
	}
	queryTemplatePattern := regexp.MustCompile("(?s)fmt\\.Sprintf\\(\\s*`[^`]*\\bSELECT\\b[^`]*`")

	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if queryTemplatePattern.Find(content) != nil {
			t.Fatalf("found formatted SQL query template in migrated file %s", rel)
		}
	}
}

func TestSQLBuilderAudit_MigratedFilesAvoidFmtSprintfEntirely(t *testing.T) {
	root := repoRootFromThisFile(t)
	files := []string{
		"internal/promshim/storage/join_sql.go",
		"internal/promshim/native/renderer/renderer.go",
		"internal/promshim/storage/selector_sql.go",
		"internal/promshim/storage/sql.go",
	}

	fmtPattern := regexp.MustCompile(`fmt\.Sprintf\(`)
	for _, rel := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("opening %s: %v", rel, err)
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if fmtPattern.MatchString(line) {
				t.Fatalf("found fmt.Sprintf in migrated file %s:%d: %s", rel, lineNo, strings.TrimSpace(line))
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scanning %s: %v", rel, err)
		}
	}
}

func repoRootFromThisFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func relToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
