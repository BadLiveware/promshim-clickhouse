package promshim

import (
	"context"
	"time"

	httpapi "ch-observability/internal/promshim/httpapi"
	"ch-observability/internal/promshim/shadow"
)

func (h *queryService) EvaluateInstantShadow(ctx context.Context, req httpapi.InstantQueryRequest) shadow.InstantResult {
	shadowReq := req
	shadowReq.NativeLoweringMode = string(NativeLoweringModePrefer)
	planStart := time.Now()
	_, evaluationTime, plan, _, apiErr := h.buildInstantPlan(shadowReq)
	planDuration := time.Since(planStart)
	if apiErr != nil {
		return shadow.InstantResult{PlanDuration: planDuration, PlanError: apiErr}
	}
	strategy := explainPlan(plan).Strategy
	evalStart := time.Now()
	value, err := h.evaluator.evaluate(ctx, plan, evalParams{Mode: evalModeInstant, EvaluationTime: evaluationTime})
	evalDuration := time.Since(evalStart)
	if err != nil {
		return shadow.InstantResult{Strategy: strategy, PlanDuration: planDuration, EvalDuration: evalDuration, ExecError: err}
	}
	return shadow.InstantResult{Strategy: strategy, Value: value, PlanDuration: planDuration, EvalDuration: evalDuration}
}

func (h *queryService) EvaluateRangeShadow(ctx context.Context, req httpapi.RangeQueryRequest) shadow.RangeResult {
	shadowReq := req
	shadowReq.NativeLoweringMode = string(NativeLoweringModePrefer)
	planStart := time.Now()
	_, start, end, step, plan, _, apiErr := h.buildRangePlan(shadowReq)
	planDuration := time.Since(planStart)
	if apiErr != nil {
		return shadow.RangeResult{PlanDuration: planDuration, PlanError: apiErr}
	}
	strategy := explainPlan(plan).Strategy
	evalStart := time.Now()
	value, err := h.evaluator.evaluate(ctx, plan, evalParams{Mode: evalModeRange, Start: start, End: end, Step: step})
	evalDuration := time.Since(evalStart)
	if err != nil {
		return shadow.RangeResult{Strategy: strategy, PlanDuration: planDuration, EvalDuration: evalDuration, ExecError: err}
	}
	return shadow.RangeResult{Strategy: strategy, Value: value, PlanDuration: planDuration, EvalDuration: evalDuration}
}
