package storage

import (
	"strings"
	"testing"
)

func TestDecodeSelectorStats(t *testing.T) {
	stats, err := decodeSelectorStats(strings.NewReader("{\"matched_series\":12}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if stats.MatchedSeries != 12 {
		t.Fatalf("MatchedSeries = %d, want 12", stats.MatchedSeries)
	}
}
