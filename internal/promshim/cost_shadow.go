package promshim

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
)

func (h *queryService) runCostShadowInstant(ctx context.Context, req httpapi.InstantQueryRequest, served model.RuntimeValue, routing httpapi.RoutingInfo, strictDuration time.Duration) {
	candidate, skipReason := pickShadowAlternateCandidate(routing)
	if skipReason != "" {
		observeShadowCandidateOutcome(routing, "none", "skipped_"+skipReason)
		return
	}
	alternateReq := req
	alternateReq.NativeLoweringMode = candidateNativeMode(candidate)
	start := time.Now()
	_, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(alternateReq)
	if apiErr != nil {
		status := "plan_error"
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, candidate.Strategy, status)
		observeShadowCandidateOutcome(routing, candidate.ID, status)
		return
	}
	alternateExplain := local.ExplainPlanWithLowering(plan, analysis.Root)
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime})
	duration := time.Since(start)
	routingmetrics.ObserveShadowDuration(routing.Class.Family, candidate.ID, duration.Seconds())
	observePredictionRatio(routing.Class.Family, duration, strictDuration)
	if err != nil {
		status := "execution_error"
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, status)
		observeShadowCandidateOutcome(routing, candidate.ID, status)
		return
	}
	status := compareRuntimeValues(false, served, value)
	routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, status)
	observeShadowCandidateOutcome(routing, candidate.ID, status)
	if status != "match" {
		routingmetrics.ObserveShadowDivergence(routing.Class.Family, status)
	}
}

func (h *queryService) runCostShadowRange(ctx context.Context, req httpapi.RangeQueryRequest, served model.RuntimeValue, routing httpapi.RoutingInfo, strictDuration time.Duration) {
	candidate, skipReason := pickShadowAlternateCandidate(routing)
	if skipReason != "" {
		observeShadowCandidateOutcome(routing, "none", "skipped_"+skipReason)
		return
	}
	alternateReq := req
	alternateReq.NativeLoweringMode = candidateNativeMode(candidate)
	start := time.Now()
	_, rangeStart, rangeEnd, step, plan, analysis, apiErr := h.buildRangePlan(ctx, alternateReq, false)
	if apiErr != nil {
		status := "plan_error"
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, candidate.Strategy, status)
		observeShadowCandidateOutcome(routing, candidate.ID, status)
		return
	}
	alternateExplain := local.ExplainPlanWithLowering(plan, analysis.Root)
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeRange, Start: rangeStart, End: rangeEnd, Step: step})
	duration := time.Since(start)
	routingmetrics.ObserveShadowDuration(routing.Class.Family, candidate.ID, duration.Seconds())
	observePredictionRatio(routing.Class.Family, duration, strictDuration)
	if err != nil {
		status := "execution_error"
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, status)
		observeShadowCandidateOutcome(routing, candidate.ID, status)
		return
	}
	status := compareRuntimeValues(true, served, value)
	routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, status)
	observeShadowCandidateOutcome(routing, candidate.ID, status)
	if status != "match" {
		routingmetrics.ObserveShadowDivergence(routing.Class.Family, status)
	}
}

func candidateNativeMode(candidate httpapi.ExecutionCandidate) string {
	switch candidate.ID {
	case string(cbeCandidateNativeSQL):
		return string(local.NativeLoweringModeForceSupported)
	case string(cbeCandidateLocalPushdown):
		return string(local.NativeLoweringModeLocalPushdown)
	case string(cbeCandidateFullLocal):
		return string(local.NativeLoweringModeOff)
	default:
		return string(local.NativeLoweringModePrefer)
	}
}

func pickShadowAlternateCandidate(routing httpapi.RoutingInfo) (httpapi.ExecutionCandidate, string) {
	decision := routing.CandidateDecision
	if decision == nil {
		return httpapi.ExecutionCandidate{}, "missing_candidate_decision"
	}
	ranked := rankShadowCandidates(routing.Candidates)
	if len(ranked) == 0 {
		return httpapi.ExecutionCandidate{}, "no_ranked_candidates"
	}
	selectedID := decision.SelectedCandidate
	if selectedID != "" {
		for _, candidate := range ranked {
			if candidate.ID == selectedID {
				if candidate.Served {
					return httpapi.ExecutionCandidate{}, "selected_is_served"
				}
				if !canExecuteShadowCandidate(candidate) {
					return httpapi.ExecutionCandidate{}, "selected_not_executable"
				}
				return candidate, ""
			}
		}
	}
	for _, candidate := range ranked {
		if candidate.Served {
			continue
		}
		if !canExecuteShadowCandidate(candidate) {
			continue
		}
		return candidate, ""
	}
	return httpapi.ExecutionCandidate{}, "no_executable_candidate"
}

func rankShadowCandidates(candidates []httpapi.ExecutionCandidate) []httpapi.ExecutionCandidate {
	ranked := make([]httpapi.ExecutionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Supported || !candidate.KnownCorrect || !candidate.Eligible {
			continue
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		left, right := ranked[i], ranked[j]
		if left.Selected != right.Selected {
			return left.Selected
		}
		leftCost, leftHasCost := candidateCost(left)
		rightCost, rightHasCost := candidateCost(right)
		if leftHasCost != rightHasCost {
			return leftHasCost
		}
		if leftHasCost && leftCost != rightCost {
			return leftCost < rightCost
		}
		return candidateTier(left) < candidateTier(right)
	})
	return ranked
}

func candidateTier(candidate httpapi.ExecutionCandidate) int {
	tier, err := strconv.Atoi(candidate.Tier)
	if err != nil {
		return 999
	}
	return tier
}

func candidateCost(candidate httpapi.ExecutionCandidate) (float64, bool) {
	if candidate.EstimatedCost == nil || candidate.EstimatedCost.Value <= 0 {
		return 0, false
	}
	return candidate.EstimatedCost.Value, true
}

func canExecuteShadowCandidate(candidate httpapi.ExecutionCandidate) bool {
	return slices.Contains([]string{string(cbeCandidateNativeSQL), string(cbeCandidateLocalPushdown), string(cbeCandidateFullLocal)}, candidate.ID)
}

func observeShadowCandidateOutcome(routing httpapi.RoutingInfo, alternateCandidate, status string) {
	decision := routing.CandidateDecision
	strict, selected, served := "unknown", "unknown", "unknown"
	if decision != nil {
		strict = decision.StrictCandidate
		selected = decision.SelectedCandidate
		served = decision.ServedCandidate
	}
	routingmetrics.ObserveShadowCandidateOutcome(routing.Class.Family, strict, selected, served, alternateCandidate, status)
}

func observePredictionRatio(family string, alternate, strict time.Duration) {
	if strict <= 0 || alternate <= 0 {
		return
	}
	routingmetrics.ObservePredictionError(family, float64(alternate)/float64(strict))
}

func compareRuntimeValues(isRange bool, left, right model.RuntimeValue) string {
	leftType, leftResult, err := renderRuntimeValue(isRange, left)
	if err != nil {
		return "served_render_error"
	}
	rightType, rightResult, err := renderRuntimeValue(isRange, right)
	if err != nil {
		return "alternate_render_error"
	}
	if leftType != rightType || !reflect.DeepEqual(leftResult, rightResult) {
		return "diff"
	}
	return "match"
}

func renderRuntimeValue(isRange bool, value model.RuntimeValue) (string, any, error) {
	if isRange {
		return httpapi.RenderRangeQueryValue(value)
	}
	resultType, result, err := httpapi.RenderInstantQueryValue(value)
	if err != nil {
		return "", nil, fmt.Errorf("render instant value: %w", err)
	}
	return resultType, result, nil
}
