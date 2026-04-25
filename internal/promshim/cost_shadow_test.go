package promshim

import (
	"testing"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
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

func TestRankShadowCandidatesSkipsRejectedAndSortsBySelectedThenCost(t *testing.T) {
	candidates := []httpapi.ExecutionCandidate{
		{ID: "native_sql", Tier: "2", Supported: true, KnownCorrect: true, Eligible: true, EstimatedCost: &httpapi.CandidateCost{Value: 25, Unit: "ms"}},
		{ID: "full_local", Tier: "4", Supported: true, KnownCorrect: true, Eligible: true, Selected: true, EstimatedCost: &httpapi.CandidateCost{Value: 15, Unit: "ms"}},
		{ID: "local_pushdown", Tier: "3", Supported: true, KnownCorrect: true, Eligible: false, RejectReasons: []string{"unsupported_shape"}, EstimatedCost: &httpapi.CandidateCost{Value: 12, Unit: "ms"}},
	}
	ranked := rankShadowCandidates(candidates)
	if len(ranked) != 2 {
		t.Fatalf("ranked len = %d, want 2", len(ranked))
	}
	if ranked[0].ID != "full_local" || ranked[1].ID != "native_sql" {
		t.Fatalf("ranked order = %+v", ranked)
	}
}

func TestPickShadowAlternateCandidate(t *testing.T) {
	routing := httpapi.RoutingInfo{
		CandidateDecision: &httpapi.CandidateDecision{StrictCandidate: "native_sql", SelectedCandidate: "full_local", ServedCandidate: "native_sql"},
		Candidates: []httpapi.ExecutionCandidate{
			{ID: "native_sql", Tier: "2", Supported: true, KnownCorrect: true, Eligible: true, Served: true, EstimatedCost: &httpapi.CandidateCost{Value: 28, Unit: "ms"}},
			{ID: "full_local", Tier: "4", Supported: true, KnownCorrect: true, Eligible: true, Selected: true, EstimatedCost: &httpapi.CandidateCost{Value: 11, Unit: "ms"}},
		},
	}
	candidate, reason := pickShadowAlternateCandidate(routing)
	if reason != "" {
		t.Fatalf("pick reason = %q", reason)
	}
	if candidate.ID != "full_local" {
		t.Fatalf("candidate = %q, want full_local", candidate.ID)
	}
}

func TestPickShadowAlternateCandidateSkipsWhenSelectedServed(t *testing.T) {
	routing := httpapi.RoutingInfo{
		CandidateDecision: &httpapi.CandidateDecision{StrictCandidate: "native_sql", SelectedCandidate: "native_sql", ServedCandidate: "native_sql"},
		Candidates:        []httpapi.ExecutionCandidate{{ID: "native_sql", Tier: "2", Supported: true, KnownCorrect: true, Eligible: true, Selected: true, Served: true}},
	}
	_, reason := pickShadowAlternateCandidate(routing)
	if reason != "selected_is_served" {
		t.Fatalf("reason = %q, want selected_is_served", reason)
	}
}
