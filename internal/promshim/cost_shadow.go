package promshim

import (
	"context"
	"fmt"
	"reflect"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
)

func (h *queryService) runCostShadowInstant(ctx context.Context, req httpapi.InstantQueryRequest, served model.RuntimeValue, routing httpapi.RoutingInfo, strictDuration time.Duration) {
	alternateReq := req
	alternateReq.NativeLoweringMode = string(local.NativeLoweringModeOff)
	start := time.Now()
	_, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(alternateReq)
	if apiErr != nil {
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, "local", "plan_error")
		return
	}
	alternateExplain := local.ExplainPlanWithLowering(plan, analysis.Root)
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime})
	duration := time.Since(start)
	routingmetrics.ObserveShadowDuration(routing.Class.Family, "local", duration.Seconds())
	observePredictionRatio(routing.Class.Family, duration, strictDuration)
	if err != nil {
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, "execution_error")
		return
	}
	status := compareRuntimeValues(false, served, value)
	routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, status)
	if status != "match" {
		routingmetrics.ObserveShadowDivergence(routing.Class.Family, status)
	}
}

func (h *queryService) runCostShadowRange(ctx context.Context, req httpapi.RangeQueryRequest, served model.RuntimeValue, routing httpapi.RoutingInfo, strictDuration time.Duration) {
	alternateReq := req
	alternateReq.NativeLoweringMode = string(local.NativeLoweringModeOff)
	start := time.Now()
	_, rangeStart, rangeEnd, step, plan, analysis, apiErr := h.buildRangePlan(alternateReq)
	if apiErr != nil {
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, "local", "plan_error")
		return
	}
	alternateExplain := local.ExplainPlanWithLowering(plan, analysis.Root)
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeRange, Start: rangeStart, End: rangeEnd, Step: step})
	duration := time.Since(start)
	routingmetrics.ObserveShadowDuration(routing.Class.Family, "local", duration.Seconds())
	observePredictionRatio(routing.Class.Family, duration, strictDuration)
	if err != nil {
		routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, "execution_error")
		return
	}
	status := compareRuntimeValues(true, served, value)
	routingmetrics.ObserveShadowRun(routing.Class.Family, routing.StrictStrategy, alternateExplain.Strategy, status)
	if status != "match" {
		routingmetrics.ObserveShadowDivergence(routing.Class.Family, status)
	}
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
