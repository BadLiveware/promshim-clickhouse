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

func TestCostPreferSelectsLocalWhenFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "selector", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"selector_instant"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want local override", info)
	}
}
