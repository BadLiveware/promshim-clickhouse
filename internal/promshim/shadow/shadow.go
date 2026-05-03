package shadow

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/prometheus/client_golang/prometheus"
)

type Comparison struct {
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

type InstantResult struct {
	Strategy     string
	Value        model.RuntimeValue
	PlanDuration time.Duration
	EvalDuration time.Duration
	PlanError    *httpapi.APIError
	ExecError    error
}

type RangeResult struct {
	Strategy     string
	Value        model.RuntimeValue
	PlanDuration time.Duration
	EvalDuration time.Duration
	PlanError    *httpapi.APIError
	ExecError    error
}

type Evaluator interface {
	EvaluateInstantShadow(ctx context.Context, req httpapi.InstantQueryRequest) InstantResult
	EvaluateRangeShadow(ctx context.Context, req httpapi.RangeQueryRequest) RangeResult
}

type Runner struct {
	ev      Evaluator
	metrics *Metrics
	summary *SummaryRecorder
}

func NewRunner(ev Evaluator) *Runner {
	metrics := newMetrics()
	return &Runner{
		ev:      ev,
		metrics: metrics,
		summary: newSummaryRecorder(metrics),
	}
}

func (r *Runner) MetricsHandler() http.Handler {
	if r == nil {
		return nil
	}
	return r.metrics.handler()
}

func (r *Runner) Registry() *prometheus.Registry {
	if r == nil {
		return nil
	}
	return r.metrics.registry
}

func (r *Runner) Summary() *Summary {
	if r == nil {
		return nil
	}
	return r.summary.snapshot()
}

func (r *Runner) RunInstant(ctx context.Context, req httpapi.InstantQueryRequest, servedStrategy string, servedValue model.RuntimeValue, servedPlanDuration, servedEvalDuration time.Duration) *Comparison {
	shadow := r.ev.EvaluateInstantShadow(ctx, req)
	base := &Comparison{
		ServedStrategy:   servedStrategy,
		CompareMode:      compareMode(req.Query),
		ServedPlanMillis: durationMillis(servedPlanDuration),
		ServedEvalMillis: durationMillis(servedEvalDuration),
		ShadowPlanMillis: durationMillis(shadow.PlanDuration),
	}
	if shadow.PlanError != nil {
		base.Status = "plan_error"
		base.Detail = shadow.PlanError.Error
		return r.log(req.Query, base)
	}
	base.ShadowStrategy = shadow.Strategy
	base.ShadowEvalMillis = durationMillis(shadow.EvalDuration)
	if shadow.ExecError != nil {
		base.Status = "execution_error"
		base.Detail = shadow.ExecError.Error()
		return r.log(req.Query, base)
	}
	return r.log(req.Query, r.finish(base, false, servedValue, shadow.Value))
}

func (r *Runner) RunRange(ctx context.Context, req httpapi.RangeQueryRequest, servedStrategy string, servedValue model.RuntimeValue, servedPlanDuration, servedEvalDuration time.Duration) *Comparison {
	shadow := r.ev.EvaluateRangeShadow(ctx, req)
	base := &Comparison{
		ServedStrategy:   servedStrategy,
		CompareMode:      compareMode(req.Query),
		ServedPlanMillis: durationMillis(servedPlanDuration),
		ServedEvalMillis: durationMillis(servedEvalDuration),
		ShadowPlanMillis: durationMillis(shadow.PlanDuration),
	}
	if shadow.PlanError != nil {
		base.Status = "plan_error"
		base.Detail = shadow.PlanError.Error
		return r.log(req.Query, base)
	}
	base.ShadowStrategy = shadow.Strategy
	base.ShadowEvalMillis = durationMillis(shadow.EvalDuration)
	if shadow.ExecError != nil {
		base.Status = "execution_error"
		base.Detail = shadow.ExecError.Error()
		return r.log(req.Query, base)
	}
	return r.log(req.Query, r.finish(base, true, servedValue, shadow.Value))
}

func (r *Runner) finish(base *Comparison, isRange bool, servedValue, shadowValue model.RuntimeValue) *Comparison {
	if base.CompareMode != promharness.CompareModeExact {
		base.Status = "skipped"
		base.Detail = "shadow comparison currently supports exact-mode queries only"
		return base
	}
	leftType, leftResult, err := renderValue(isRange, servedValue)
	if err != nil {
		base.Status = "render_error"
		base.Detail = fmt.Sprintf("rendering served result: %v", err)
		return base
	}
	rightType, rightResult, err := renderValue(isRange, shadowValue)
	if err != nil {
		base.Status = "render_error"
		base.Detail = fmt.Sprintf("rendering shadow result: %v", err)
		return base
	}
	if leftType != rightType || !reflect.DeepEqual(leftResult, rightResult) {
		base.Status = "diff"
		base.Detail = fmt.Sprintf("served(%s) and shadow(%s) results differ", leftType, rightType)
		return base
	}
	base.Status = "match"
	return base
}

func compareMode(query string) string {
	return promharness.InferCompareMode(promharness.QuerySpec{Query: query})
}

func renderValue(isRange bool, value model.RuntimeValue) (string, any, error) {
	if isRange {
		return httpapi.RenderRangeQueryValue(value)
	}
	return httpapi.RenderInstantQueryValue(value)
}

func (r *Runner) log(query string, report *Comparison) *Comparison {
	if report == nil {
		return nil
	}
	if r != nil && r.summary != nil {
		r.summary.record(report)
	}
	log.Printf("shadow query=%q served_strategy=%s shadow_strategy=%s compare_mode=%s status=%s detail=%s served_plan_ms=%d served_eval_ms=%d shadow_plan_ms=%d shadow_eval_ms=%d", query, report.ServedStrategy, report.ShadowStrategy, report.CompareMode, report.Status, report.Detail, report.ServedPlanMillis, report.ServedEvalMillis, report.ShadowPlanMillis, report.ShadowEvalMillis)
	return report
}

func durationMillis(value time.Duration) int64 {
	return value.Milliseconds()
}
