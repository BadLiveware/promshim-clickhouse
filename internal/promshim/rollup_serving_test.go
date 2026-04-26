package promshim

import (
	"testing"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
)

func TestShouldServeDenseRateRollupRequiresGateDetectionAndShape(t *testing.T) {
	query := `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`
	tests := []struct {
		name      string
		service   queryService
		query     string
		step      time.Duration
		wantServe bool
	}{
		{
			name:      "gate detected exact",
			service:   queryService{opts: Options{DenseRateRollups: "prefer"}, denseRateRollup: denseRateRollupDiscoveryForTest(true)},
			query:     query,
			step:      time.Minute,
			wantServe: true,
		},
		{
			name:      "gate off",
			service:   queryService{opts: Options{DenseRateRollups: "off"}, denseRateRollup: denseRateRollupDiscoveryForTest(true)},
			query:     query,
			step:      time.Minute,
			wantServe: false,
		},
		{
			name:      "rollup absent",
			service:   queryService{opts: Options{DenseRateRollups: "prefer"}, denseRateRollup: denseRateRollupDiscoveryForTest(false)},
			query:     query,
			step:      time.Minute,
			wantServe: false,
		},
		{
			name:      "wrong shape",
			service:   queryService{opts: Options{DenseRateRollups: "prefer"}, denseRateRollup: denseRateRollupDiscoveryForTest(true)},
			query:     `sum by (instance) (rate(demo_cpu_usage_seconds_total[5m]))`,
			step:      time.Minute,
			wantServe: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.service.shouldServeDenseRateRollup(tt.query, tt.step); got != tt.wantServe {
				t.Fatalf("shouldServeDenseRateRollup() = %v, want %v", got, tt.wantServe)
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
