package promshim

import (
	"fmt"
	"strings"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
)

type RoutingPolicy string

const (
	RoutingPolicyStrict     RoutingPolicy = "strict"
	RoutingPolicyCostShadow RoutingPolicy = "cost_shadow"
	RoutingPolicyCostPrefer RoutingPolicy = "cost_prefer"
)

func ParseRoutingPolicy(raw string) (RoutingPolicy, error) {
	policy := RoutingPolicy(strings.ToLower(strings.TrimSpace(raw)))
	switch policy {
	case "", RoutingPolicyStrict:
		return RoutingPolicyStrict, nil
	case RoutingPolicyCostShadow, RoutingPolicyCostPrefer:
		return policy, nil
	default:
		return "", fmt.Errorf("unsupported routing policy %q (want strict, cost_shadow, or cost_prefer)", raw)
	}
}

func NormalizeRoutingPolicy(policy RoutingPolicy) RoutingPolicy {
	normalized, err := ParseRoutingPolicy(string(policy))
	if err != nil {
		return RoutingPolicyStrict
	}
	return normalized
}

type costModel struct {
	MaxLocalInputSamples  int64
	MaxLocalOutputPoints  int64
	MaxLocalOutputSeries  int64
	MaxLocalRoundTrips    int
	MinRelativeLocalRatio float64
	MinAbsoluteWinMS      float64
}

func defaultCostModel() costModel {
	return costModel{MaxLocalInputSamples: 50_000, MaxLocalOutputPoints: 10_000, MaxLocalOutputSeries: 5_000, MaxLocalRoundTrips: 2, MinRelativeLocalRatio: 0.70, MinAbsoluteWinMS: 3}
}

func routingDecisionForStrict(policy RoutingPolicy, mode local.NativeLoweringMode, class httpapi.QueryCostClass, strictStrategy string, enabledFamilies []string) httpapi.RoutingInfo {
	class.RootStrategyStrict = strictStrategy
	effectivePolicy := NormalizeRoutingPolicy(policy)
	if mode == local.NativeLoweringModeForceSupported || mode == local.NativeLoweringModeOff || mode == local.NativeLoweringModeShadow {
		// Cost routing is deliberately ignored in modes that already have explicit
		// execution semantics. This keeps force_supported native-only visibility and
		// the existing shadow/off behavior independent from future cost policy work.
		info := baseRoutingInfo(RoutingPolicyStrict, "strict", "native_lowering_mode_ignores_cost_routing", class, strictStrategy)
		attachCBECandidates(&info, mode)
		recordRoutingInfo(info)
		return info
	}
	if effectivePolicy == RoutingPolicyStrict {
		info := baseRoutingInfo(effectivePolicy, "strict", "strict_policy", class, strictStrategy)
		attachCBECandidates(&info, mode)
		recordRoutingInfo(info)
		return info
	}
	info := defaultCostModel().decide(class, strictStrategy, effectivePolicy, enabledFamilies)
	attachCBECandidates(&info, mode)
	recordRoutingInfo(info)
	return info
}

func (m costModel) decide(class httpapi.QueryCostClass, strictStrategy string, policy RoutingPolicy, enabledFamilies []string) httpapi.RoutingInfo {
	info := baseRoutingInfo(policy, "shadow_only", "cost_shadow_strict_default", class, strictStrategy)
	info.EnabledFamilies = append([]string(nil), enabledFamilies...)
	info.Caps = map[string]int64{
		"maxLocalInputSamples": int64(m.MaxLocalInputSamples),
		"maxLocalOutputPoints": int64(m.MaxLocalOutputPoints),
		"maxLocalOutputSeries": int64(m.MaxLocalOutputSeries),
		"maxLocalRoundTrips":   int64(m.MaxLocalRoundTrips),
	}
	info.Cost = estimateRoutingCost(class)
	if strictStrategy == "delegated_promql" {
		info.Decision = "strict_low_confidence"
		info.Reason = "delegated_promql_has_no_cost_model"
		return info
	}
	if len(info.MissingEstimates) > 0 {
		info.Decision = "strict_missing_estimate"
		info.Reason = "missing_estimate"
		return info
	}
	for _, capName := range m.capHits(class) {
		info.CapHits = append(info.CapHits, capName)
		routingmetrics.ObserveOverCap(class.Family, capName)
	}
	if len(info.CapHits) > 0 {
		info.Decision = "strict_over_cap"
		info.Reason = "hard_cap"
		return info
	}
	if !localCandidateFamily(class) {
		info.Decision = "strict_low_confidence"
		info.Reason = "family_not_local_candidate"
		return info
	}
	if info.Cost == nil || info.Cost.Native <= 0 || info.Cost.Local <= 0 {
		info.Decision = "strict_missing_estimate"
		info.Reason = "missing_cost"
		return info
	}
	if info.Cost.Local > m.MinRelativeLocalRatio*info.Cost.Native || info.Cost.Native-info.Cost.Local < m.MinAbsoluteWinMS {
		info.Decision = "strict_low_confidence"
		info.Reason = "predicted_win_below_margin"
		return info
	}
	info.WouldSelect = "local"
	info.Reason = class.Family + "_local_candidate_under_caps"
	if policy == RoutingPolicyCostPrefer {
		if !familyEnabled(class, enabledFamilies) {
			info.WouldSelect = strictStrategy
			info.Decision = "strict_low_confidence"
			info.Reason = "family_gate_disabled"
			return info
		}
		if !costPreferServingCandidateAllowed(class) {
			info.WouldSelect = strictStrategy
			info.Decision = "strict_low_confidence"
			info.Reason = "candidate_serving_disabled"
			return info
		}
		if strictStrategy == "local" {
			// Safety rail: strict may already be local because native planning for the
			// expression root is unavailable (for example aggregation/range-local paths).
			// That is not a CBE win over native and must not be reported as one.
			info.WouldSelect = strictStrategy
			info.Decision = "strict_low_confidence"
			info.Reason = "strict_reference_already_local"
			return info
		}
		if hasKnownCostPreferDivergence(class) {
			info.WouldSelect = strictStrategy
			info.Decision = "strict_low_confidence"
			info.Reason = "known_divergence"
			return info
		}
		info.Decision = "local_override"
		info.SelectedStrategy = "local"
		return info
	}
	info.Decision = "shadow_only"
	return info
}

func costPreferServingCandidateAllowed(class httpapi.QueryCostClass) bool {
	// Safety rail: 04 only enables served CBE changes for short-window instant
	// rate/increase queries with a single selector. Range, histogram, and broad
	// aggregation families stay strict/reference until they have dedicated
	// evidence and tighter caps in later plan slices.
	if class.Endpoint != "query" {
		return false
	}
	if class.Family != "rate" && class.Family != "increase" {
		return false
	}
	if class.SelectorCount != 1 {
		return false
	}
	if class.HasHistogram || class.HasVectorJoin || class.HasSubquery {
		return false
	}
	return true
}

func hasKnownCostPreferDivergence(class httpapi.QueryCostClass) bool {
	_ = class
	return false
}

func baseRoutingInfo(policy RoutingPolicy, decision, reason string, class httpapi.QueryCostClass, strictStrategy string) httpapi.RoutingInfo {
	missing := missingEstimateFields(class)
	return httpapi.RoutingInfo{Policy: string(policy), StrictStrategy: strictStrategy, SelectedStrategy: strictStrategy, WouldSelect: strictStrategy, Decision: decision, Reason: reason, EstimatesAvailable: len(missing) == 0, MissingEstimates: missing, Class: class}
}

func recordRoutingInfo(info httpapi.RoutingInfo) {
	for _, field := range info.MissingEstimates {
		routingmetrics.ObserveMissingEstimate(info.Class.Family, field)
	}
	routingmetrics.ObserveDecision(info.Policy, info.Decision, info.StrictStrategy, info.SelectedStrategy, info.Class.Family, info.Reason)
}

func missingEstimateFields(class httpapi.QueryCostClass) []string {
	if class.SelectorCount == 0 || class.EstimatedSeries != 0 {
		return nil
	}
	if class.EstimateState.Stale > 0 {
		return []string{"selector_stats_stale"}
	}
	return []string{"selector_stats"}
}

func (m costModel) capHits(class httpapi.QueryCostClass) []string {
	var hits []string
	if class.EstimatedInputSamples > m.MaxLocalInputSamples {
		hits = append(hits, "maxLocalInputSamples")
	}
	if class.EstimatedOutputPoints > m.MaxLocalOutputPoints {
		hits = append(hits, "maxLocalOutputPoints")
	}
	if class.EstimatedSeries > m.MaxLocalOutputSeries {
		hits = append(hits, "maxLocalOutputSeries")
	}
	if class.LocalRoundTrips > m.MaxLocalRoundTrips {
		hits = append(hits, "maxLocalRoundTrips")
	}
	if class.HasSubquery {
		hits = append(hits, "subquery")
	}
	if class.HasVectorJoin && class.EstimatedSeries > 1000 {
		hits = append(hits, "highCardinalityVectorJoin")
	}
	if class.RangePointsPerSeries > 240 && class.Family != "range_selector" {
		hits = append(hits, "rangePointsPerSeries")
	}
	return hits
}

func estimateRoutingCost(class httpapi.QueryCostClass) *httpapi.RoutingCost {
	nativeBase, localBase := familyBases(class.Family)
	if nativeBase == 0 || localBase == 0 {
		return nil
	}
	nativeCost := nativeBase + float64(class.EstimatedInputSamples)/10_000 + float64(class.EstimatedOutputPoints)/20_000 + float64(class.NativeRoundTrips)*2
	localCost := localBase + float64(class.EstimatedInputSamples)/50_000 + float64(class.EstimatedOutputPoints)/50_000 + float64(class.LocalRoundTrips)*2
	return &httpapi.RoutingCost{Native: nativeCost, Local: localCost, Unit: "ms_p50_estimate"}
}

func familyBases(family string) (native, local float64) {
	switch family {
	case "selector", "range_selector":
		return 24, 7
	case "rate", "range_rate", "increase":
		return 30, 20
	case "histogram_quantile":
		return 150, 35
	default:
		return 0, 0
	}
}

func familyEnabled(class httpapi.QueryCostClass, enabled []string) bool {
	gate := familyGate(class)
	for _, item := range enabled {
		if strings.TrimSpace(item) == gate {
			return true
		}
	}
	return false
}

func familyGate(class httpapi.QueryCostClass) string {
	switch class.Family {
	case "selector":
		return "selector_instant"
	case "rate", "increase":
		return "rate_instant"
	case "histogram_quantile":
		return "histogram_instant"
	case "range_selector":
		return "range_selector_tiny"
	default:
		return class.Family
	}
}

func localCandidateFamily(class httpapi.QueryCostClass) bool {
	switch class.Family {
	case "selector":
		return class.Endpoint == "query"
	case "rate", "increase":
		return class.Endpoint == "query" && class.SelectorCount == 1
	case "histogram_quantile":
		return class.Endpoint == "query" && !class.HasVectorJoin
	case "range_selector":
		return class.RangePointsPerSeries > 0 && class.RangePointsPerSeries <= 60
	default:
		return false
	}
}
