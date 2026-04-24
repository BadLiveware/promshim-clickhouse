package promshim

import (
	"context"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/shadow"
)

func (h *queryService) EvaluateInstantShadow(ctx context.Context, req httpapi.InstantQueryRequest) shadow.InstantResult {
	shadowReq := req
	shadowReq.NativeLoweringMode = string(local.NativeLoweringModePrefer)
	planStart := time.Now()
	_, evaluationTime, plan, _, apiErr := h.buildInstantPlan(shadowReq)
	planDuration := time.Since(planStart)
	if apiErr != nil {
		return shadow.InstantResult{PlanDuration: planDuration, PlanError: apiErr}
	}
	strategy := local.ExplainPlan(plan).Strategy
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime})
	evalDuration := time.Since(evalStart)
	if err != nil {
		return shadow.InstantResult{Strategy: strategy, PlanDuration: planDuration, EvalDuration: evalDuration, ExecError: err}
	}
	return shadow.InstantResult{Strategy: strategy, Value: value, PlanDuration: planDuration, EvalDuration: evalDuration}
}

func (h *queryService) EvaluateRangeShadow(ctx context.Context, req httpapi.RangeQueryRequest) shadow.RangeResult {
	shadowReq := req
	shadowReq.NativeLoweringMode = string(local.NativeLoweringModePrefer)
	planStart := time.Now()
	_, start, end, step, plan, _, apiErr := h.buildRangePlan(shadowReq)
	planDuration := time.Since(planStart)
	if apiErr != nil {
		return shadow.RangeResult{PlanDuration: planDuration, PlanError: apiErr}
	}
	strategy := local.ExplainPlan(plan).Strategy
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeRange, Start: start, End: end, Step: step})
	evalDuration := time.Since(evalStart)
	if err != nil {
		return shadow.RangeResult{Strategy: strategy, PlanDuration: planDuration, EvalDuration: evalDuration, ExecError: err}
	}
	return shadow.RangeResult{Strategy: strategy, Value: value, PlanDuration: planDuration, EvalDuration: evalDuration}
}
