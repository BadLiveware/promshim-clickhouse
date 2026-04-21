package promshim

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promharness"
	httpapi "github.com/BadLiveware/promshim-ch/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

type shadowComparison struct {
	ServedStrategy   string `json:"servedStrategy,omitempty"`
	ShadowStrategy   string `json:"shadowStrategy,omitempty"`
	CompareMode      string `json:"compareMode,omitempty"`
	Status           string `json:"status,omitempty"`
	Detail           string `json:"detail,omitempty"`
	ServedPlanMillis int64  `json:"servedPlanMillis"`
	ServedEvalMillis int64  `json:"servedEvalMillis"`
	ShadowPlanMillis int64  `json:"shadowPlanMillis"`
	ShadowEvalMillis int64  `json:"shadowEvalMillis"`
}

func (h *queryService) runInstantShadowComparison(ctx context.Context, req httpapi.InstantQueryRequest, servedPlan queryPlan, servedValue model.RuntimeValue, servedPlanDuration, servedEvalDuration time.Duration) *shadowComparison {
	shadowReq := req
	shadowReq.NativeLoweringMode = string(NativeLoweringModePrefer)
	planStart := time.Now()
	_, evaluationTime, shadowPlan, _, apiErr := h.buildInstantPlan(shadowReq)
	shadowPlanDuration := time.Since(planStart)
	if apiErr != nil {
		return h.logShadowComparison(req.Query, &shadowComparison{ServedStrategy: explainPlan(servedPlan).Strategy, CompareMode: shadowCompareMode(req.Query), Status: "plan_error", Detail: apiErr.Error, ServedPlanMillis: durationMillis(servedPlanDuration), ServedEvalMillis: durationMillis(servedEvalDuration), ShadowPlanMillis: durationMillis(shadowPlanDuration)})
	}
	evalStart := time.Now()
	shadowValue, err := h.evaluator.evaluate(ctx, shadowPlan, evalParams{Mode: evalModeInstant, EvaluationTime: evaluationTime})
	shadowEvalDuration := time.Since(evalStart)
	if err != nil {
		return h.logShadowComparison(req.Query, &shadowComparison{ServedStrategy: explainPlan(servedPlan).Strategy, ShadowStrategy: explainPlan(shadowPlan).Strategy, CompareMode: shadowCompareMode(req.Query), Status: "execution_error", Detail: err.Error(), ServedPlanMillis: durationMillis(servedPlanDuration), ServedEvalMillis: durationMillis(servedEvalDuration), ShadowPlanMillis: durationMillis(shadowPlanDuration), ShadowEvalMillis: durationMillis(shadowEvalDuration)})
	}
	return h.finishShadowComparison(req.Query, evalModeInstant, servedPlan, shadowPlan, servedValue, shadowValue, servedPlanDuration, servedEvalDuration, shadowPlanDuration, shadowEvalDuration)
}

func (h *queryService) runRangeShadowComparison(ctx context.Context, req httpapi.RangeQueryRequest, servedPlan queryPlan, servedValue model.RuntimeValue, servedPlanDuration, servedEvalDuration time.Duration) *shadowComparison {
	shadowReq := req
	shadowReq.NativeLoweringMode = string(NativeLoweringModePrefer)
	planStart := time.Now()
	_, start, end, step, shadowPlan, _, apiErr := h.buildRangePlan(shadowReq)
	shadowPlanDuration := time.Since(planStart)
	if apiErr != nil {
		return h.logShadowComparison(req.Query, &shadowComparison{ServedStrategy: explainPlan(servedPlan).Strategy, CompareMode: shadowCompareMode(req.Query), Status: "plan_error", Detail: apiErr.Error, ServedPlanMillis: durationMillis(servedPlanDuration), ServedEvalMillis: durationMillis(servedEvalDuration), ShadowPlanMillis: durationMillis(shadowPlanDuration)})
	}
	evalStart := time.Now()
	shadowValue, err := h.evaluator.evaluate(ctx, shadowPlan, evalParams{Mode: evalModeRange, Start: start, End: end, Step: step})
	shadowEvalDuration := time.Since(evalStart)
	if err != nil {
		return h.logShadowComparison(req.Query, &shadowComparison{ServedStrategy: explainPlan(servedPlan).Strategy, ShadowStrategy: explainPlan(shadowPlan).Strategy, CompareMode: shadowCompareMode(req.Query), Status: "execution_error", Detail: err.Error(), ServedPlanMillis: durationMillis(servedPlanDuration), ServedEvalMillis: durationMillis(servedEvalDuration), ShadowPlanMillis: durationMillis(shadowPlanDuration), ShadowEvalMillis: durationMillis(shadowEvalDuration)})
	}
	return h.finishShadowComparison(req.Query, evalModeRange, servedPlan, shadowPlan, servedValue, shadowValue, servedPlanDuration, servedEvalDuration, shadowPlanDuration, shadowEvalDuration)
}

func (h *queryService) finishShadowComparison(query string, mode evalMode, servedPlan, shadowPlan queryPlan, servedValue, shadowValue model.RuntimeValue, servedPlanDuration, servedEvalDuration, shadowPlanDuration, shadowEvalDuration time.Duration) *shadowComparison {
	report := &shadowComparison{
		ServedStrategy:   explainPlan(servedPlan).Strategy,
		ShadowStrategy:   explainPlan(shadowPlan).Strategy,
		CompareMode:      shadowCompareMode(query),
		ServedPlanMillis: durationMillis(servedPlanDuration),
		ServedEvalMillis: durationMillis(servedEvalDuration),
		ShadowPlanMillis: durationMillis(shadowPlanDuration),
		ShadowEvalMillis: durationMillis(shadowEvalDuration),
	}
	if report.CompareMode != promharness.CompareModeExact {
		report.Status = "skipped"
		report.Detail = "shadow comparison currently supports exact-mode queries only"
		return h.logShadowComparison(query, report)
	}
	leftType, leftResult, err := renderShadowValue(mode, servedValue)
	if err != nil {
		report.Status = "render_error"
		report.Detail = fmt.Sprintf("rendering served result: %v", err)
		return h.logShadowComparison(query, report)
	}
	rightType, rightResult, err := renderShadowValue(mode, shadowValue)
	if err != nil {
		report.Status = "render_error"
		report.Detail = fmt.Sprintf("rendering shadow result: %v", err)
		return h.logShadowComparison(query, report)
	}
	if leftType != rightType || !reflect.DeepEqual(leftResult, rightResult) {
		report.Status = "diff"
		report.Detail = fmt.Sprintf("served(%s) and shadow(%s) results differ", leftType, rightType)
		return h.logShadowComparison(query, report)
	}
	report.Status = "match"
	return h.logShadowComparison(query, report)
}

func shadowCompareMode(query string) string {
	return promharness.InferCompareMode(promharness.QuerySpec{Query: query})
}

func renderShadowValue(mode evalMode, value model.RuntimeValue) (string, any, error) {
	if mode == evalModeRange {
		return httpapi.RenderRangeQueryValue(value)
	}
	return httpapi.RenderInstantQueryValue(value)
}

func (h *queryService) logShadowComparison(query string, report *shadowComparison) *shadowComparison {
	if report == nil {
		return nil
	}
	if h != nil && h.shadowSummary != nil {
		h.shadowSummary.record(report)
	}
	log.Printf("shadow query=%q served_strategy=%s shadow_strategy=%s compare_mode=%s status=%s detail=%s served_plan_ms=%d served_eval_ms=%d shadow_plan_ms=%d shadow_eval_ms=%d", query, report.ServedStrategy, report.ShadowStrategy, report.CompareMode, report.Status, report.Detail, report.ServedPlanMillis, report.ServedEvalMillis, report.ShadowPlanMillis, report.ShadowEvalMillis)
	return report
}

func durationMillis(value time.Duration) int64 {
	return value.Milliseconds()
}
