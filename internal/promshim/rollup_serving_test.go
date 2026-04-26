package promshim

import (
	"context"
	"testing"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
)

func TestShouldServeDenseRateRollupRejectsBeforeCoverageProbe(t *testing.T) {
	query := `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`
	now := time.Unix(100, 0)
	tests := []struct {
		name    string
		service queryService
		query   string
		step    time.Duration
	}{
		{
			name:    "gate off",
			service: queryService{opts: Options{DenseRateRollups: "off"}, denseRateRollup: denseRateRollupDiscoveryForTest(true)},
			query:   query,
			step:    time.Minute,
		},
		{
			name:    "rollup absent",
			service: queryService{opts: Options{DenseRateRollups: "prefer"}, denseRateRollup: denseRateRollupDiscoveryForTest(false)},
			query:   query,
			step:    time.Minute,
		},
		{
			name:    "wrong shape",
			service: queryService{opts: Options{DenseRateRollups: "prefer"}, denseRateRollup: denseRateRollupDiscoveryForTest(true)},
			query:   `sum by (instance) (rate(demo_cpu_usage_seconds_total[5m]))`,
			step:    time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.service.shouldServeDenseRateRollup(context.Background(), tt.query, now, now, tt.step); got {
				t.Fatalf("shouldServeDenseRateRollup() = true, want false")
			}
		})
	}
}

func TestMarkDenseRateRollupServed(t *testing.T) {
	routing := httpapi.RoutingInfo{
		CandidateDecision: &httpapi.CandidateDecision{StrictCandidate: "native_sql", SelectedCandidate: "native_sql", ServedCandidate: "native_sql"},
		Candidates: []httpapi.ExecutionCandidate{{
			ID:            denseRateRollupCandidateID,
			RejectReasons: []string{"gate_disabled"},
		}},
	}
	markDenseRateRollupServed(&routing)
	if routing.Decision != "rollup_override" || routing.Reason != "optional_dense_rate_rollup_gate" {
		t.Fatalf("unexpected routing decision: %#v", routing)
	}
	if routing.CandidateDecision.SelectedCandidate != denseRateRollupCandidateID || routing.CandidateDecision.ServedCandidate != denseRateRollupCandidateID {
		t.Fatalf("unexpected candidate decision: %#v", routing.CandidateDecision)
	}
	candidate := routing.Candidates[0]
	if !candidate.Eligible || !candidate.Selected || !candidate.Served || len(candidate.RejectReasons) != 0 {
		t.Fatalf("candidate not marked served cleanly: %#v", candidate)
	}
}
