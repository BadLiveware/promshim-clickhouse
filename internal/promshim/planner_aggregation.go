package promshim

import (
	"context"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

type localAggregationPlan struct {
	Expr        string
	Op          parser.ItemType
	Grouping    []string
	Without     bool
	ParamNumber *float64
	ParamString string
	Reason      string
	Child       queryPlan
}

func (p *localAggregationPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating local aggregation op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	result, err := exec.AggregateRuntimeValue(p.Op, childValue, exec.AggregationOptions{
		Grouping:       p.Grouping,
		Without:        p.Without,
		EvaluationTime: params.EvaluationTime,
		ParamNumber:    p.ParamNumber,
		ParamString:    p.ParamString,
	})
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "aggregating local result op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	return result, nil
}

func (p *localAggregationPlan) explain() ExplainNode {
	return ExplainNode{Kind: "aggregation", Strategy: "local", Expr: p.Expr, Reason: p.Reason, Children: []ExplainNode{p.Child.explain()}}
}

type localVectorPlan struct {
	Expr  string
	Child queryPlan
}

func (p *localVectorPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating vector child in instant mode")
		}
		vector, err := exec.ApplyVector(childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying vector()")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "vector", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localVectorPlan) explain() ExplainNode {
	return ExplainNode{Kind: "vector", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localRoundPlan struct {
	Expr     string
	Decimals *float64
	Child    queryPlan
}

func (p *localRoundPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating round child in instant mode")
		}
		vector, err := exec.ApplyRound(childValue, p.Decimals)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying round")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "round", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRoundPlan) explain() ExplainNode {
	return ExplainNode{Kind: "round", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localSortPlan struct {
	Expr   string
	Func   string
	Labels []string
	Child  queryPlan
}

func (p *localSortPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		vector, err := exec.ApplySortFunction(p.Func, childValue, p.Labels)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, p.Func, p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localSortPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localPointwiseFunctionPlan struct {
	Expr         string
	Func         string
	ParamNumbers []*float64
	Child        queryPlan
}

func (p *localPointwiseFunctionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		var childValue model.RuntimeValue
		var err error
		if p.Child != nil {
			childValue, err = p.Child.execute(ctx, evaluator, params)
			if err != nil {
				return nil, withInternalContext(err, "evaluating %s child in instant mode", p.Func)
			}
		}
		vector, err := exec.ApplyPointwiseFunction(p.Func, childValue, exec.EvalParams{Mode: toExecEvalMode(params.Mode), EvaluationTime: params.EvaluationTime, Start: params.Start, End: params.End, Step: params.Step}, p.ParamNumbers)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, p.Func, p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localPointwiseFunctionPlan) explain() ExplainNode {
	children := []ExplainNode{}
	if p.Child != nil {
		children = append(children, p.Child.explain())
	}
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: children}
}
