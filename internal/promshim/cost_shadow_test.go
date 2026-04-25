package promshim

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestCompareRuntimeValues(t *testing.T) {
	left := model.ScalarValue{Timestamp: 1, Value: 2}
	right := model.ScalarValue{Timestamp: 1, Value: 2}
	if got := compareRuntimeValues(false, left, right); got != "match" {
		t.Fatalf("compare equal = %q", got)
	}
	different := model.ScalarValue{Timestamp: 1, Value: 3}
	if got := compareRuntimeValues(false, left, different); got != "diff" {
		t.Fatalf("compare diff = %q", got)
	}
}
