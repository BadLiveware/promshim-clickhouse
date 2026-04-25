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

func TestCostRoutingAllowsFreshZeroSeriesEstimate(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 0, EstimatedInputSamples: 0, EstimatedOutputPoints: 0, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true, EstimateState: httpapi.EstimateState{Source: "cache", Fresh: true, SelectorCount: 1}}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"aggregation_instant"})
	if info.Decision != "local_override" || !info.EstimatesAvailable || len(info.MissingEstimates) != 0 {
		t.Fatalf("decision = %+v, want fresh zero-series estimate to be usable", info)
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
	capEval, ok := capEvaluationByName(info.CapEvaluations, "maxLocalInputSamples")
	if !ok {
		t.Fatalf("missing cap evaluation in %+v", info.CapEvaluations)
	}
	if capEval.Estimate != 1000000 || capEval.Limit != 50000 || !capEval.Exceeded || capEval.OverBy != 950000 || capEval.Unit != "samples" {
		t.Fatalf("cap evaluation = %+v", capEval)
	}
	seriesEval, ok := capEvaluationByName(info.CapEvaluations, "maxLocalOutputSeries")
	if !ok || seriesEval.Exceeded || seriesEval.Estimate != 10 || seriesEval.Limit != 5000 {
		t.Fatalf("series cap evaluation = %+v ok=%v", seriesEval, ok)
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

func TestCostPreferSelectsLocalForRepeatedRateBinaryWhenFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "binary", SelectorCount: 2, EstimatedSeries: 60, EstimatedInputSamples: 14460, EstimatedOutputPoints: 60, LocalRoundTrips: 2, NativeRoundTrips: 1, HasRangeFunction: true, HasRepeatedRangeFunc: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"binary_repeated_rate_instant"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want repeated rate binary local override", info)
	}
	if info.Cost == nil || info.Cost.Local >= info.Cost.Native {
		t.Fatalf("cost estimate = %+v", info.Cost)
	}
}

func TestCostPreferKeepsRepeatedRateBinaryStrictForUnsupportedShapes(t *testing.T) {
	tests := []struct {
		name  string
		class httpapi.QueryCostClass
	}{
		{
			name:  "range endpoint",
			class: httpapi.QueryCostClass{Endpoint: "query_range", Family: "binary", SelectorCount: 2, EstimatedSeries: 60, EstimatedInputSamples: 14460, EstimatedOutputPoints: 60, LocalRoundTrips: 2, NativeRoundTrips: 1, HasRangeFunction: true, HasRepeatedRangeFunc: true},
		},
		{
			name:  "explicit vector matching",
			class: httpapi.QueryCostClass{Endpoint: "query", Family: "binary", SelectorCount: 2, EstimatedSeries: 60, EstimatedInputSamples: 14460, EstimatedOutputPoints: 60, LocalRoundTrips: 2, NativeRoundTrips: 1, HasRangeFunction: true, HasRepeatedRangeFunc: true, HasVectorJoin: true},
		},
		{
			name:  "not repeated",
			class: httpapi.QueryCostClass{Endpoint: "query", Family: "binary", SelectorCount: 2, EstimatedSeries: 60, EstimatedInputSamples: 14460, EstimatedOutputPoints: 60, LocalRoundTrips: 2, NativeRoundTrips: 1, HasRangeFunction: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, tc.class, "native_sql", []string{"binary_repeated_rate_instant"})
			if info.Decision != "strict_low_confidence" || info.Reason != "family_not_local_candidate" && info.Reason != "candidate_serving_disabled" {
				t.Fatalf("decision = %+v, want strict unsupported binary shape", info)
			}
		})
	}
}

func TestCostPreferKeepsRepeatedRateBinaryStrictWhenOverCap(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "binary", SelectorCount: 2, EstimatedSeries: 60, EstimatedInputSamples: 86460, EstimatedOutputPoints: 60, LocalRoundTrips: 2, NativeRoundTrips: 1, HasRangeFunction: true, HasRepeatedRangeFunc: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"binary_repeated_rate_instant"})
	if info.Decision != "strict_over_cap" || len(info.CapHits) == 0 || info.CapHits[0] != "maxLocalInputSamples" {
		t.Fatalf("decision = %+v, want maxLocalInputSamples cap", info)
	}
}

func TestCostPreferSelectsLocalWhenHistogramFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "histogram_quantile", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1, HasHistogram: true, HasAggregation: true, HasRangeFunction: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"histogram_instant"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want histogram local override", info)
	}
}

func TestCostPreferKeepsHistogramServingDisabledForUnsupportedShapes(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "histogram_quantile", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1, HasHistogram: true, HasAggregation: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"histogram_instant"})
	if info.Decision != "strict_low_confidence" || info.Reason != "candidate_serving_disabled" {
		t.Fatalf("decision = %+v, want candidate serving disabled", info)
	}
}

func TestCostPreferKeepsHistogramRangeQueriesStrict(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query_range", Family: "histogram_quantile", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1, HasHistogram: true, HasAggregation: true, HasRangeFunction: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"histogram_instant"})
	if info.Decision != "strict_low_confidence" || info.Reason != "family_not_local_candidate" {
		t.Fatalf("decision = %+v, want range histogram strict", info)
	}
}

func TestCostPreferSelectsLocalForRangeAggregationWhenFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true, HasRangeFunction: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"range_aggregation_instant"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want range aggregation local override", info)
	}
}

func TestCostPreferKeepsRangeAggregationStrictWhenOverCap(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 30, EstimatedInputSamples: 86430, EstimatedOutputPoints: 30, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true, HasRangeFunction: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"range_aggregation_instant"})
	if info.Decision != "strict_over_cap" || len(info.CapHits) == 0 || info.CapHits[0] != "maxLocalInputSamples" {
		t.Fatalf("decision = %+v, want maxLocalInputSamples cap", info)
	}
	capEval, ok := capEvaluationByName(info.CapEvaluations, "maxLocalInputSamples")
	if !ok || capEval.Estimate != 86430 || capEval.Limit != 50000 || !capEval.Exceeded || capEval.OverBy != 36430 {
		t.Fatalf("cap evaluation = %+v ok=%v", capEval, ok)
	}
}

func TestCostPreferSelectsLocalForPlainAggregationWhenFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 10, EstimatedInputSamples: 100, EstimatedOutputPoints: 10, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"aggregation_instant"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want plain aggregation local override", info)
	}
}

func TestCostPreferKeepsPlainAggregationRangeQueriesStrictWithoutRangeGate(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query_range", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 30, EstimatedInputSamples: 1_210_230, EstimatedOutputPoints: 169, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"aggregation_instant"})
	if info.Decision != "strict_low_confidence" || info.Reason != "family_gate_disabled" {
		t.Fatalf("decision = %+v, want plain aggregation range strict (gate disabled)", info)
	}
}

func TestCostPreferSelectsLocalForPlainAggregationRangeWhenFamilyGateEnabled(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query_range", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 30, EstimatedInputSamples: 1_210_230, EstimatedOutputPoints: 169, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"aggregation_range"})
	if info.Decision != "local_override" || info.SelectedStrategy != "local" || info.WouldSelect != "local" {
		t.Fatalf("decision = %+v, want plain aggregation range local override", info)
	}
	capEval, ok := capEvaluationByName(info.CapEvaluations, "maxLocalInputSamples")
	if !ok || capEval.Limit != 1_500_000 || capEval.Exceeded {
		t.Fatalf("cap evaluation = %+v ok=%v, want range aggregation cap limit 1500000 under limit", capEval, ok)
	}
}

func TestCostPreferKeepsPlainAggregationRangeStrictWhenOverRangeCap(t *testing.T) {
	class := httpapi.QueryCostClass{Endpoint: "query_range", Family: "aggregation", SelectorCount: 1, EstimatedSeries: 30, EstimatedInputSamples: 1_600_000, EstimatedOutputPoints: 169, LocalRoundTrips: 1, NativeRoundTrips: 1, HasAggregation: true}
	info := routingDecisionForStrict(RoutingPolicyCostPrefer, local.NativeLoweringModePrefer, class, "native_sql", []string{"aggregation_range"})
	if info.Decision != "strict_over_cap" || len(info.CapHits) == 0 || info.CapHits[0] != "maxLocalInputSamples" {
		t.Fatalf("decision = %+v, want maxLocalInputSamples cap for range aggregation", info)
	}
	capEval, ok := capEvaluationByName(info.CapEvaluations, "maxLocalInputSamples")
	if !ok || capEval.Limit != 1_500_000 || !capEval.Exceeded || capEval.OverBy != 100_000 {
		t.Fatalf("cap evaluation = %+v ok=%v, want range cap limit 1500000 over by 100000", capEval, ok)
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

func capEvaluationByName(items []httpapi.RoutingCapEvaluation, name string) (httpapi.RoutingCapEvaluation, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return httpapi.RoutingCapEvaluation{}, false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
