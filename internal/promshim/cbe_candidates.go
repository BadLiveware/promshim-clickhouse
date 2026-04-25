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
	strictID := cbeCandidateForStrategy(info.StrictStrategy)
	selectedID := cbeCandidateForStrategy(info.WouldSelect)
	if selectedID == cbeCandidateUnknown {
		selectedID = cbeCandidateForStrategy(info.SelectedStrategy)
	}
	servedID := cbeCandidateForStrategy(info.SelectedStrategy)
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

func cbeCandidateForStrategy(strategy string) cbeCandidateID {
	switch strategy {
	case "delegated_promql":
		return cbeCandidateWholeQueryDelegation
	case "native_sql":
		return cbeCandidateNativeSQL
	case "local":
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
	candidate := httpapi.ExecutionCandidate{
		ID:                 string(id),
		Tier:               tier,
		Strategy:           strategy,
		Family:             info.Class.Family,
		Strict:             id == strictID,
		Selected:           id == selectedID,
		Served:             id == servedID,
		Supported:          id == strictID || id == selectedID || id == servedID,
		KnownCorrect:       id == strictID || id == selectedID || id == servedID,
		Eligible:           id == strictID || id == selectedID || id == servedID,
		EstimatesAvailable: info.EstimatesAvailable,
	}
	if id == cbeCandidateNativeSQL && info.Cost != nil {
		candidate.EstimatedCost = &httpapi.CandidateCost{Value: info.Cost.Native, Unit: info.Cost.Unit}
	}
	if (id == cbeCandidateLocalPushdown || id == cbeCandidateFullLocal) && info.Cost != nil {
		candidate.EstimatedCost = &httpapi.CandidateCost{Value: info.Cost.Local, Unit: info.Cost.Unit}
	}
	return candidate
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
	if !info.EstimatesAvailable && candidate.ID != string(cbeCandidateWholeQueryDelegation) {
		reasons = append(reasons, string(cbeRejectMissingEstimate))
	}
	if len(info.CapHits) > 0 {
		reasons = append(reasons, string(cbeRejectOverCap))
	}
	return reasons
}
