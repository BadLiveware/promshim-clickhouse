package promshim

import (
	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
)

type cbeCandidateID string

const (
	cbeCandidateWholeQueryDelegation cbeCandidateID = "whole_query_delegation"
	cbeCandidateNativeSQL            cbeCandidateID = "native_sql"
	cbeCandidateLocalPushdown        cbeCandidateID = "local_pushdown"
	cbeCandidateFullLocal            cbeCandidateID = "full_local"
	cbeCandidateUnknown              cbeCandidateID = "unknown"
)

type cbeRejectReason string

const (
	cbeRejectUnsupportedShape cbeRejectReason = "unsupported_shape"
	cbeRejectPolicyIgnored    cbeRejectReason = "policy_ignored"
	cbeRejectMissingEstimate  cbeRejectReason = "missing_estimate"
	cbeRejectOverCap          cbeRejectReason = "over_cap"
	cbeRejectNotEligible      cbeRejectReason = "not_eligible"
)

func attachCBECandidates(info *httpapi.RoutingInfo, mode local.NativeLoweringMode) {
	if info == nil {
		return
	}
	strictID := cbeCandidateForStrategyInMode(info.StrictStrategy, mode)
	selectedID := cbeCandidateForStrategyInMode(info.WouldSelect, mode)
	if selectedID == cbeCandidateUnknown {
		selectedID = cbeCandidateForStrategyInMode(info.SelectedStrategy, mode)
	}
	servedID := cbeCandidateForStrategyInMode(info.SelectedStrategy, mode)
	info.CandidateDecision = &httpapi.CandidateDecision{
		StrictCandidate:   string(strictID),
		SelectedCandidate: string(selectedID),
		ServedCandidate:   string(servedID),
	}
	info.Candidates = buildCBECandidates(*info, mode, strictID, selectedID, servedID)
	for _, candidate := range info.Candidates {
		state := "eligible"
		if !candidate.Eligible {
			state = firstCandidateRejectReason(candidate)
		}
		routingmetrics.ObserveCandidate(info.Policy, candidate.Family, candidate.ID, boolLabel(candidate.Selected), boolLabel(candidate.Served), state)
	}
}

func cbeCandidateForStrategyInMode(strategy string, mode local.NativeLoweringMode) cbeCandidateID {
	switch strategy {
	case "delegated_promql":
		return cbeCandidateWholeQueryDelegation
	case "native_sql":
		return cbeCandidateNativeSQL
	case "local":
		if mode == local.NativeLoweringModeLocalPushdown {
			return cbeCandidateLocalPushdown
		}
		return cbeCandidateFullLocal
	default:
		return cbeCandidateUnknown
	}
}

func buildCBECandidates(info httpapi.RoutingInfo, mode local.NativeLoweringMode, strictID, selectedID, servedID cbeCandidateID) []httpapi.ExecutionCandidate {
	candidates := []httpapi.ExecutionCandidate{
		candidateFromStrict(info, cbeCandidateWholeQueryDelegation, "1", "delegated_promql", strictID, selectedID, servedID),
		candidateFromStrict(info, cbeCandidateNativeSQL, "2", "native_sql", strictID, selectedID, servedID),
		candidateFromStrict(info, cbeCandidateLocalPushdown, "3", "local", strictID, selectedID, servedID),
		candidateFromStrict(info, cbeCandidateFullLocal, "4", "local", strictID, selectedID, servedID),
	}
	for i := range candidates {
		applyCandidateGates(&candidates[i], info, mode)
	}
	return candidates
}

func candidateFromStrict(info httpapi.RoutingInfo, id cbeCandidateID, tier, strategy string, strictID, selectedID, servedID cbeCandidateID) httpapi.ExecutionCandidate {
	supported := id == strictID || id == selectedID || id == servedID
	knownCorrect := supported
	var advisory []string
	if id == cbeCandidateLocalPushdown && histogramLocalPushdownCandidate(info) {
		supported = true
		knownCorrect = true
		advisory = append(advisory, "shadow_candidate=histogram_local_pushdown", "estimate_scope=conservative_full_local_caps")
	}
	candidate := httpapi.ExecutionCandidate{
		ID:                 string(id),
		Tier:               tier,
		Strategy:           strategy,
		Family:             info.Class.Family,
		Strict:             id == strictID,
		Selected:           id == selectedID,
		Served:             id == servedID,
		Supported:          supported,
		KnownCorrect:       knownCorrect,
		Eligible:           supported && knownCorrect,
		EstimatesAvailable: info.EstimatesAvailable,
		Advisory:           advisory,
	}
	if id == cbeCandidateNativeSQL && info.Cost != nil {
		candidate.EstimatedCost = &httpapi.CandidateCost{Value: info.Cost.Native, Unit: info.Cost.Unit}
	}
	if (id == cbeCandidateLocalPushdown || id == cbeCandidateFullLocal) && info.Cost != nil {
		candidate.EstimatedCost = &httpapi.CandidateCost{Value: info.Cost.Local, Unit: info.Cost.Unit}
	}
	if id == cbeCandidateLocalPushdown && histogramLocalPushdownCandidate(info) {
		annotateHistogramLocalPushdownCandidate(&candidate, info)
	}
	return candidate
}

func annotateHistogramLocalPushdownCandidate(candidate *httpapi.ExecutionCandidate, info httpapi.RoutingInfo) {
	if candidate == nil {
		return
	}
	model := defaultCostModel()
	candidate.Estimates = append(candidate.Estimates,
		httpapi.CandidateEstimate{Name: "nativeChildInputSamples", Value: info.Class.EstimatedInputSamples, Unit: "samples", Scope: "native_child"},
		httpapi.CandidateEstimate{Name: "nativeChildOutputPointsConservative", Value: info.Class.EstimatedOutputPoints, Unit: "points", Scope: "native_child"},
		httpapi.CandidateEstimate{Name: "localEngineRoundTrips", Value: int64(info.Class.LocalRoundTrips), Unit: "round_trips", Scope: "local_engine"},
	)
	if len(info.Class.HistogramChildGroupingLabels) > 0 {
		candidate.Estimates = append(candidate.Estimates, httpapi.CandidateEstimate{Name: "localEngineGroupingLabels", Value: int64(len(info.Class.HistogramChildGroupingLabels)), Unit: "labels", Scope: "local_engine"})
	}
	candidate.CapEvaluations = append(candidate.CapEvaluations,
		candidateCapEvaluation("maxNativeChildInputSamples", info.Class.EstimatedInputSamples, model.maxLocalInputSamplesLimit(info.Class), "samples", "native_child"),
		candidateCapEvaluation("maxNativeChildOutputPointsConservative", info.Class.EstimatedOutputPoints, int64(model.MaxLocalOutputPoints), "points", "native_child"),
		candidateCapEvaluation("maxLocalEngineRoundTrips", int64(info.Class.LocalRoundTrips), int64(model.MaxLocalRoundTrips), "round_trips", "local_engine"),
	)
	candidate.Advisory = append(candidate.Advisory,
		"candidate_cap_scope=local_engine,native_child",
		"local_engine_estimate=unknown_native_child_output_cardinality",
		"routing_deferred=needs_histogram_memory_policy",
	)
	if info.Class.HistogramChildGroupsByLeOnly {
		candidate.Advisory = append(candidate.Advisory, "local_engine_output_shape=classic_histogram_buckets_only")
	}
}

func candidateCapEvaluation(name string, estimate, limit int64, unit, scope string) httpapi.RoutingCapEvaluation {
	evaluation := capEvaluation(name, estimate, limit, unit)
	evaluation.Scope = scope
	return evaluation
}

func histogramLocalPushdownCandidate(info httpapi.RoutingInfo) bool {
	class := info.Class
	return class.Endpoint == "query" && class.Family == "histogram_quantile" && class.HasHistogram && class.HasAggregation && class.HasRangeFunction && !class.HasVectorJoin && !class.HasSubquery && class.SelectorCount == 1
}

func applyCandidateGates(candidate *httpapi.ExecutionCandidate, info httpapi.RoutingInfo, mode local.NativeLoweringMode) {
	if candidate == nil {
		return
	}
	if candidate.Supported && candidate.KnownCorrect {
		// Candidate eligibility is intentionally stricter than served strict routing:
		// missing estimates or cap hits may reject a candidate from CBE ranking while
		// the strict/reference candidate remains the safe served fallback.
		candidate.RejectReasons = candidateRejectReasons(*candidate, info, mode)
		candidate.Eligible = len(candidate.RejectReasons) == 0
		return
	}
	candidate.RejectReasons = append(candidate.RejectReasons, string(cbeRejectUnsupportedShape))
	candidate.Eligible = false
}

func firstCandidateRejectReason(candidate httpapi.ExecutionCandidate) string {
	if len(candidate.RejectReasons) == 0 {
		return string(cbeRejectNotEligible)
	}
	return candidate.RejectReasons[0]
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func candidateRejectReasons(candidate httpapi.ExecutionCandidate, info httpapi.RoutingInfo, mode local.NativeLoweringMode) []string {
	var reasons []string
	if mode == local.NativeLoweringModeForceSupported && candidate.ID != string(cbeCandidateNativeSQL) {
		reasons = append(reasons, string(cbeRejectPolicyIgnored))
	}
	if mode == local.NativeLoweringModeLocalPushdown && candidate.ID != string(cbeCandidateLocalPushdown) {
		reasons = append(reasons, string(cbeRejectPolicyIgnored))
	}
	if !info.EstimatesAvailable && candidate.ID != string(cbeCandidateWholeQueryDelegation) {
		reasons = append(reasons, string(cbeRejectMissingEstimate))
	}
	if len(info.CapHits) > 0 {
		reasons = append(reasons, string(cbeRejectOverCap))
	}
	return reasons
}
