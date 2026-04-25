package promshim

import (
	"testing"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
)

func TestSelectedCandidateMode(t *testing.T) {
	tests := []struct {
		name    string
		routing httpapi.RoutingInfo
		want    string
		ok      bool
	}{
		{
			name:    "missing decision",
			routing: httpapi.RoutingInfo{},
			ok:      false,
		},
		{
			name: "native sql",
			routing: httpapi.RoutingInfo{CandidateDecision: &httpapi.CandidateDecision{
				SelectedCandidate: "native_sql",
			}},
			want: "force_supported",
			ok:   true,
		},
		{
			name: "full local",
			routing: httpapi.RoutingInfo{CandidateDecision: &httpapi.CandidateDecision{
				SelectedCandidate: "full_local",
			}},
			want: "off",
			ok:   true,
		},
		{
			name: "unsupported candidate",
			routing: httpapi.RoutingInfo{CandidateDecision: &httpapi.CandidateDecision{
				SelectedCandidate: "whole_query_delegation",
			}},
			ok: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := selectedCandidateMode(tt.routing)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("selectedCandidateMode() = (%q,%t), want (%q,%t)", got, ok, tt.want, tt.ok)
			}
		})
	}
}
