package local

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func TestUpdateChunkedPreflightReasonKeepsExplainReasonsConsistent(t *testing.T) {
	decision := &nativeRangeChunkDecision{Policy: "bounded_series_preflight", Chunked: true, ChunkPointsPerSeries: 10}
	plan := &chunkedRangePlan{Child: &nativeSubtreePlan{}, Reason: "generic chunk reason", Decision: decision, ChunkPointsPerSeries: 10}

	updateChunkedPreflightReason(plan, decision, "bounded series preflight exceeded threshold; keeping safe native range chunking")

	if plan.Reason != decision.Reason {
		t.Fatalf("chunk reason %q does not match decision reason %q", plan.Reason, decision.Reason)
	}
	if plan.explain().Reason != decision.Reason {
		t.Fatalf("explain reason %q does not match decision reason %q", plan.explain().Reason, decision.Reason)
	}
}

func TestNativeRangePreflightFailureReasonsAreSpecific(t *testing.T) {
	base := func(threshold int64) *chunkedRangePlan {
		return &chunkedRangePlan{
			Child: &nativeSubtreePlan{},
			Decision: &nativeRangeChunkDecision{
				Policy:               "bounded_series_preflight",
				Chunked:              true,
				ChunkPointsPerSeries: 10,
				PreflightThreshold:   threshold,
			},
			ChunkPointsPerSeries: 10,
		}
	}

	cases := []struct {
		name      string
		plan      *chunkedRangePlan
		wantError string
	}{
		{name: "selector", plan: base(1), wantError: "preflight selector unavailable"},
		{name: "threshold", plan: func() *chunkedRangePlan {
			p := base(0)
			p.Child = singleSelectorNativeSubtreeForPreflightTest(t)
			return p
		}(), wantError: "preflight series threshold disabled"},
		{name: "client", plan: func() *chunkedRangePlan {
			p := base(1)
			p.Child = singleSelectorNativeSubtreeForPreflightTest(t)
			return p
		}(), wantError: "preflight ClickHouse client unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated := ApplyNativeRangePreflight(t.Context(), nil, storage.QueryConfig{}, tc.plan)
			chunked, ok := updated.(*chunkedRangePlan)
			if !ok {
				t.Fatalf("expected chunkedRangePlan, got %T", updated)
			}
			if chunked.Decision.PreflightError != tc.wantError {
				t.Fatalf("preflight error = %q, want %q", chunked.Decision.PreflightError, tc.wantError)
			}
		})
	}
}

func singleSelectorNativeSubtreeForPreflightTest(t *testing.T) *nativeSubtreePlan {
	t.Helper()
	expr, err := logical.ParseExpression(`up[5m]`)
	if err != nil {
		t.Fatal(err)
	}
	root, err := logical.ToLogical(expr)
	if err != nil {
		t.Fatal(err)
	}
	return &nativeSubtreePlan{LogicalRoot: root}
}

func TestNativeRangePreflightSelectorRejectsMultipleSelectors(t *testing.T) {
	expr, err := logical.ParseExpression(`low_cardinality_metric + high_cardinality_metric`)
	if err != nil {
		t.Fatal(err)
	}
	root, err := logical.ToLogical(expr)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nativeRangePreflightSelector(&nativeSubtreePlan{LogicalRoot: root}); ok {
		t.Fatal("expected multi-selector native range plan to skip preflight")
	}
}

func TestNativeRangePreflightBoundsFailClosedWhenUnavailable(t *testing.T) {
	if _, _, ok := nativeRangePreflightBounds(&nativeSubtreePlan{}); ok {
		t.Fatal("expected missing optimization bounds to disable preflight")
	}
}

func TestNativeRangePreflightBoundsRejectsPartialBounds(t *testing.T) {
	if _, _, ok := nativeRangePreflightBounds(&nativeSubtreePlan{OptimizationReport: &nativeplan.OptimizationReport{RequiredInputEndMS: 2000}}); ok {
		t.Fatal("expected missing start bound to disable preflight")
	}
	if _, _, ok := nativeRangePreflightBounds(&nativeSubtreePlan{OptimizationReport: &nativeplan.OptimizationReport{RequiredInputStartMS: 1000}}); ok {
		t.Fatal("expected missing end bound to disable preflight")
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
