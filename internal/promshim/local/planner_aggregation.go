package local

import (
	"context"

	"ch-observability/internal/promshim/local/exec"
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
	Child       Plan
}

func (p *localAggregationPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, Evaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating local aggregation op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	result, err := exec.AggregateRuntimeValue(p.Op, childValue, exec.AggregationOptions{
		Grouping:       p.Grouping,
		Without:        p.Without,
		EvaluationTime: params.EvaluationTime,
		ParamNumber:    p.ParamNumber,
		ParamString:    p.ParamString,
	})
	if err != nil {
		return nil, WithInternalContext(FromExecError(err), "aggregating local result op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	return result, nil
}

func (p *localAggregationPlan) explain() ExplainNode {
	return ExplainNode{Kind: "aggregation", Strategy: "local", Expr: p.Expr, Reason: p.Reason, Children: []ExplainNode{p.Child.explain()}}
}

type localVectorPlan struct {
	Expr  string
	Child Plan
}

func (p *localVectorPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating vector child in instant mode")
		}
		vector, err := exec.ApplyVector(childValue)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying vector()")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "vector", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localVectorPlan) explain() ExplainNode {
	return ExplainNode{Kind: "vector", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localRoundPlan struct {
	Expr     string
	Decimals *float64
	Child    Plan
}

func (p *localRoundPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating round child in instant mode")
		}
		vector, err := exec.ApplyRound(childValue, p.Decimals)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying round")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "round", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRoundPlan) explain() ExplainNode {
	return ExplainNode{Kind: "round", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localSortPlan struct {
	Expr   string
	Func   string
	Labels []string
	Child  Plan
}

func (p *localSortPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		vector, err := exec.ApplySortFunction(p.Func, childValue, p.Labels)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, p.Func, p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localSortPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localPointwiseFunctionPlan struct {
	Expr         string
	Func         string
	ParamNumbers []*float64
	Child        Plan
}

func (p *localPointwiseFunctionPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		var childValue model.RuntimeValue
		var err error
		if p.Child != nil {
			childValue, err = p.Child.execute(ctx, Evaluator, params)
			if err != nil {
				return nil, WithInternalContext(err, "evaluating %s child in instant mode", p.Func)
			}
		}
		vector, err := exec.ApplyPointwiseFunction(p.Func, childValue, exec.EvalParams{Mode: toExecEvalMode(params.Mode), EvaluationTime: params.EvaluationTime, Start: params.Start, End: params.End, Step: params.Step}, p.ParamNumbers)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, p.Func, p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localPointwiseFunctionPlan) explain() ExplainNode {
	children := []ExplainNode{}
	if p.Child != nil {
		children = append(children, p.Child.explain())
	}
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: children}
}
