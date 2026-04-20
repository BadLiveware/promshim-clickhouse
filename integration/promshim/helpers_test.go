package promshim_test

import (
	"fmt"
	"testing"
)

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertUnsupportedContains(t *testing.T, payload map[string]any, parts ...string) {
	t.Helper()
	assertEqual(t, payload["status"], "error")
	errorText, _ := payload["error"].(string)
	if errorText == "" {
		t.Fatalf("expected error text, got %#v", payload)
	}
	for _, part := range parts {
		if !contains(errorText, part) {
			t.Fatalf("expected %q to contain %q", errorText, part)
		}
	}
}

func contains(value, substring string) bool {
	return len(substring) == 0 || (len(value) >= len(substring) && fmt.Sprintf("%s", value) != "" && (func() bool { return stringIndexFold(value, substring) >= 0 })())
}

func stringIndexFold(s, substr string) int {
	lowerS := []rune(s)
	lowerSub := []rune(substr)
	for i := 0; i+len(lowerSub) <= len(lowerS); i++ {
		matched := true
		for j := range lowerSub {
			if toLower(lowerS[i+j]) != toLower(lowerSub[j]) {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

func toLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

func toStringSet(items []any) map[string]struct{} {
	result := make(map[string]struct{}, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			result[value] = struct{}{}
		}
	}
	return result
}
