package promshim

import (
	"context"

	"github.com/BadLiveware/promshim-ch/internal/promshim/exec"
	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

type localHistogramQuantilePlan struct {
	Expr     string
	Quantile float64
	Child    queryPlan
}

func (p *localHistogramQuantilePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating histogram_quantile child quantile=%v", p.Quantile)
	}
	result, err := exec.ApplyHistogramQuantileRuntimeValue(p.Quantile, childValue)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying histogram_quantile quantile=%v", p.Quantile)
	}
	return result, nil
}

func (p *localHistogramQuantilePlan) explain() ExplainNode {
	return ExplainNode{Kind: "histogram_quantile", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localHistogramFractionPlan struct {
	Expr  string
	Lower float64
	Upper float64
	Child queryPlan
}

func (p *localHistogramFractionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating histogram_fraction child bounds=[%v,%v]", p.Lower, p.Upper)
	}
	result, err := exec.ApplyHistogramFractionRuntimeValue(p.Lower, p.Upper, childValue)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying histogram_fraction bounds=[%v,%v]", p.Lower, p.Upper)
	}
	return result, nil
}

func (p *localHistogramFractionPlan) explain() ExplainNode {
	return ExplainNode{Kind: "histogram_fraction", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localHistogramProjectionPlan struct {
	Expr  string
	Func  string
	Child queryPlan
}

func (p *localHistogramProjectionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating %s child", p.Func)
	}
	var result model.RuntimeValue
	switch p.Func {
	case "histogram_count":
		result, err = exec.ApplyHistogramCountRuntimeValue(childValue)
	case "histogram_sum":
		result, err = exec.ApplyHistogramSumRuntimeValue(childValue)
	case "histogram_avg":
		result, err = exec.ApplyHistogramAvgRuntimeValue(childValue)
	default:
		return nil, newExecutionErrorf("unknown histogram projection function %q", p.Func)
	}
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying %s", p.Func)
	}
	return result, nil
}

func (p *localHistogramProjectionPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}
