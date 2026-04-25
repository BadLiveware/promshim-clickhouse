package promshim

import (
	"testing"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
)

func TestCostShadowDecisionSelectsLocalCandidateUnderCaps(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostShadow, local.NativeLoweringModePrefer, class, "native_sql", nil)
	if info.Decision != "shadow_only" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want shadow local candidate", info)
	}
	if info.CandidateDecision == nil || info.CandidateDecision.StrictCandidate != "native_sql" || info.CandidateDecision.SelectedCandidate != "full_local" || info.CandidateDecision.ServedCandidate != "native_sql" {
		t.Fatalf("candidate decision = %+v", info.CandidateDecision)
	}
	if info.Cost == nil || info.Cost.Local >= info.Cost.Native {
		t.Fatalf("cost estimate = %+v", info.Cost)
	}
}

func TestCostShadowDecisionStaysStrictOnMissingEstimate(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostShadow, local.NativeLoweringModePrefer, class, "native_sql", nil)
	if info.Decision != "strict_missing_estimate" || info.WouldSelect != "native_sql" {
		t.Fatalf("decision = %+v, want missing estimate strict", info)
	}
	if len(info.MissingEstimates) != 1 || info.MissingEstimates[0] != "selector_stats" {
		t.Fatalf("missing estimates = %+v", info.MissingEstimates)
	}
}

func TestCostShadowDecisionStaysStrictOverCap(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 1000000, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostShadow, local.NativeLoweringModePrefer, class, "native_sql", nil)
	if info.Decision != "strict_over_cap" {
		t.Fatalf("decision = %+v, want strict_over_cap", info)
	}
	if len(info.CapHits) == 0 || info.CapHits[0] != "maxLocalInputSamples" {
		t.Fatalf("cap hits = %+v", info.CapHits)
	}
}

func TestCostRoutingIgnoredForForceSupported(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, EstimatedSeries: 10}
	info := routingDecisionForStrict(RoutingPolicyCostShadow, local.NativeLoweringModeForceSupported, class, "native_sql", nil)
	if info.Policy != "strict" || info.Reason != "native_lowering_mode_ignores_cost_routing" {
		t.Fatalf("decision = %+v", info)
	}
}

func TestCostPreferRequiresFamilyGate(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", nil)
	if info.Decision != "strict_low_confidence" || info.Reason != "family_gate_disabled" {
		t.Fatalf("decision = %+v, want disabled family gate", info)
	}
}

func TestCostPreferSelectsLocalWhenRateFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "rate", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"rate_instant"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want local override", info)
	}
}

func TestCostPreferKeepsSelectorServingDisabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"selector_instant"})
	if info.Decision != "strict_low_confidence" || info.Reason != "candidate_serving_disabled" {
		t.Fatalf("decision = %+v, want candidate serving disabled", info)
	}
}

func TestCostPreferKeepsHistogramServingDisabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "histogram_quantile", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"histogram_instant"})
	if info.Decision != "strict_low_confidence" || info.Reason != "candidate_serving_disabled" {
		t.Fatalf("decision = %+v, want candidate serving disabled", info)
	}
}

func TestCostPreferDoesNotCountStrictLocalAsCBEWin(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "rate", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "local", []string{"rate_instant"})
	if info.Decision != "strict_low_confidence" || info.Reason != "strict_reference_already_local" {
		t.Fatalf("decision = %+v, want strict local guard", info)
	}
	if info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("selected/wouldSelect = %q/%q, want local/local", info.SelectedStrategy, info.WouldSelect)
	}
}

func TestCostPreferStaleEstimateStaysStrict(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "rate", SelectorCount: 1, LocalRoundTrips: 1, NativeRoundTrips: 1, EstimateState: httpapi.EstimateState{Stale: 1}}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"rate_instant"})
	if info.Decision != "strict_missing_estimate" || info.Reason != "missing_estimate" {
		t.Fatalf("decision = %+v, want stale strict missing estimate", info)
	}
	if len(info.MissingEstimates) != 1 || info.MissingEstimates[0] != "selector_stats_stale" {
		t.Fatalf("missing estimates = %+v, want selector_stats_stale", info.MissingEstimates)
	}
}

func TestCostRoutingIgnoredForOffMode(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "rate", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModeOff, class, "local", []string{"rate_instant"})
	if info.Policy != "strict" || info.Reason != "native_lowering_mode_ignores_cost_routing" {
		t.Fatalf("decision = %+v, want native-lowering mode ignores cost routing", info)
	}
}

func TestRoutingInfoIncludesCBECandidates(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "rate", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"rate_instant"})
	if info.CandidateDecision == nil {
		t.Fatalf("missing candidate decision: %+v", info)
	}
	if info.CandidateDecision.StrictCandidate != "native_sql" || info.CandidateDecision.SelectedCandidate != "full_local" || info.CandidateDecision.ServedCandidate != "full_local" {
		t.Fatalf("candidate decision = %+v", info.CandidateDecision)
	}
	candidates := map[string]httpapi.ExecutionCandidate{}
	for _, candidate := range info.Candidates {
		candidates[candidate.ID] = candidate
	}
	for _, id := range []string{"whole_query_delegation", "native_sql", "local_pushdown", "full_local"} {
		if _, ok := candidates[id]; !ok {
			t.Fatalf("missing candidate %q in %+v", id, info.Candidates)
		}
	}
	if !candidates["native_sql"].Strict || candidates["native_sql"].Tier != "2" {
		t.Fatalf("native candidate = %+v", candidates["native_sql"])
	}
	if !candidates["full_local"].Selected || !candidates["full_local"].Served || candidates["full_local"].Tier != "4" {
		t.Fatalf("full local candidate = %+v", candidates["full_local"])
	}
	if candidates["local_pushdown"].Supported || len(candidates["local_pushdown"].RejectReasons) == 0 || candidates["local_pushdown"].RejectReasons[0] != "unsupported_shape" {
		t.Fatalf("local pushdown candidate = %+v", candidates["local_pushdown"])
	}
}

func TestCBECandidatesRespectForceSupportedMode(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "rate", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostShadow, local.NativeLoweringModeForceSupported, class, "native_sql", nil)
	for _, candidate := range info.Candidates {
		if candidate.ID == "full_local" && !containsString(candidate.RejectReasons, "unsupported_shape") {
			t.Fatalf("force_supported full local candidate = %+v", candidate)
		}
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
