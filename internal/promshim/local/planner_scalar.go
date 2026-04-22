package local

import (
	"context"
	"time"

	"ch-observability/internal/promshim/local/exec"
	"ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

type scalarLiteralPlan struct {
	Expr  string
	Value float64
}

func (p *scalarLiteralPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		timestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
		return model.ScalarValue{Timestamp: timestamp, Value: p.Value}, nil
	case EvalModeRange:
		return executeRangeScalarPlan(ctx, Evaluator, params, "scalar", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *scalarLiteralPlan) explain() ExplainNode {
	return ExplainNode{Kind: "scalar", Strategy: "local", Expr: p.Expr}
}

type localUnaryPlan struct {
	Expr  string
	Op    parser.ItemType
	Child Plan
}

func (p *localUnaryPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, Evaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating unary expression op=%s", p.Op.String())
	}
	result, err := exec.ApplyUnaryRuntimeValue(p.Op, childValue, exec.EvalParams{
		Mode:           toExecEvalMode(params.Mode),
		EvaluationTime: params.EvaluationTime,
		Start:          params.Start,
		End:            params.End,
		Step:           params.Step,
	})
	if err != nil {
		return nil, WithInternalContext(FromExecError(err), "applying unary expression op=%s", p.Op.String())
	}
	return result, nil
}

func (p *localUnaryPlan) explain() ExplainNode {
	return ExplainNode{Kind: "unary", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localBinaryPlan struct {
	Expr           string
	Op             parser.ItemType
	VectorMatching *parser.VectorMatching
	ReturnBool     bool
	LHS            Plan
	RHS            Plan
}

func (p *localBinaryPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	lhsValue, err := p.LHS.execute(ctx, Evaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating left operand for binary op=%s", p.Op.String())
	}
	rhsValue, err := p.RHS.execute(ctx, Evaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating right operand for binary op=%s", p.Op.String())
	}
	result, err := exec.ApplyBinaryRuntimeValue(p.Op, lhsValue, rhsValue, p.VectorMatching, p.ReturnBool, exec.EvalParams{
		Mode:           toExecEvalMode(params.Mode),
		EvaluationTime: params.EvaluationTime,
		Start:          params.Start,
		End:            params.End,
		Step:           params.Step,
	})
	if err != nil {
		return nil, WithInternalContext(FromExecError(err), "applying binary expression op=%s returnBool=%t", p.Op.String(), p.ReturnBool)
	}
	return result, nil
}

func (p *localBinaryPlan) explain() ExplainNode {
	return ExplainNode{Kind: "binary", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.LHS.explain(), p.RHS.explain()}}
}

type localScalarConvertPlan struct {
	Expr  string
	Child Plan
}

func (p *localScalarConvertPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating scalar child in instant mode")
		}
		scalar, err := exec.ApplyScalar(childValue, exec.EvalParams{Mode: toExecEvalMode(params.Mode), EvaluationTime: params.EvaluationTime, Start: params.Start, End: params.End, Step: params.Step})
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying scalar()")
		}
		return scalar, nil
	case EvalModeRange:
		return executeRangeScalarPlan(ctx, Evaluator, params, "scalar", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localScalarConvertPlan) explain() ExplainNode {
	return ExplainNode{Kind: "scalar", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type scalarBuiltinPlan struct {
	Expr string
	Func string
}

func (p *scalarBuiltinPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		value, err := exec.ApplyScalarBuiltinFunction(p.Func, exec.EvalParams{Mode: toExecEvalMode(params.Mode), EvaluationTime: params.EvaluationTime, Start: params.Start, End: params.End, Step: params.Step})
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying %s", p.Func)
		}
		return value, nil
	case EvalModeRange:
		return executeRangeScalarPlan(ctx, Evaluator, params, p.Func, p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *scalarBuiltinPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr}
}
