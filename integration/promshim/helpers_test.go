package promshim_test

import (
	"strconv"
	"testing"
)

func assertEqual(t *testing.T, got, want any) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func contains(value, substring string) bool {
	return len(substring) == 0 || (value != "" && len(value) >= len(substring) && stringIndexFold(value, substring) >= 0)
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

func requireVectorRows(t *testing.T, payload map[string]any) []any {
	t.Helper()
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "vector")
	return data["result"].([]any)
}

func requireMatrixRows(t *testing.T, payload map[string]any) []any {
	t.Helper()
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "matrix")
	return data["result"].([]any)
}

func requireScalarValue(t *testing.T, payload map[string]any) float64 {
	t.Helper()
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "scalar")
	result := data["result"].([]any)
	parsed, err := strconv.ParseFloat(result[1].(string), 64)
	if err != nil {
		t.Fatalf("failed to parse scalar result %#v: %v", result, err)
	}
	return parsed
}

func vectorValuesByLabel(t *testing.T, rows []any, label string) map[string]float64 {
	t.Helper()
	result := make(map[string]float64, len(rows))
	for _, row := range rows {
		rowMap := row.(map[string]any)
		metric := rowMap["metric"].(map[string]any)
		labelValue, ok := metric[label].(string)
		if !ok || labelValue == "" {
			t.Fatalf("expected label %q in metric %#v", label, metric)
		}
		value := rowMap["value"].([]any)
		parsed, err := strconv.ParseFloat(value[1].(string), 64)
		if err != nil {
			t.Fatalf("failed to parse vector value %#v: %v", value, err)
		}
		result[labelValue] = parsed
	}
	return result
}
