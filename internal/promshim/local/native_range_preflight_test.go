package local

import (
	"testing"

	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
)

func TestNativeRangePreflightBoundsFailClosedWhenUnavailable(t *testing.T) {
	if _, _, ok := nativeRangePreflightBounds(&nativeSubtreePlan{}); ok {
		t.Fatal("expected missing optimization bounds to disable preflight")
	}
}

func TestNativeRangePreflightBoundsUsesOptimizationReport(t *testing.T) {
	start, end, ok := nativeRangePreflightBounds(&nativeSubtreePlan{OptimizationReport: &nativeplan.OptimizationReport{
		RequiredInputStartMS: 1000,
		RequiredInputEndMS:   2000,
	}})
	if !ok {
		t.Fatal("expected optimization bounds")
	}
	if start != 1000 || end != 2000 {
		t.Fatalf("bounds = %d..%d, want 1000..2000", start, end)
	}
}
