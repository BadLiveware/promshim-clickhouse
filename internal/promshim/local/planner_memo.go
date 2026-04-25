package local

import (
	"context"
	"fmt"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

type localMemoizedPlan struct {
	Expr  string
	Inner Plan
}

func memoizeLocalPlan(expr string, inner Plan) Plan {
	if expr == "" || inner == nil {
		return inner
	}
	return &localMemoizedPlan{Expr: expr, Inner: inner}
}

func shouldMemoizeRepeatedLocalPlan(plan Plan) bool {
	switch plan.(type) {
	case *localRatePlan, *localIncreasePlan, *localRangeFunctionPlan:
		return true
	default:
		return false
	}
}

func logicalExprString(node logicalpkg.Node) string {
	if node == nil {
		return ""
	}
	if described, ok := node.(interface{ ExprString() string }); ok {
		return described.ExprString()
	}
	return ""
}

func (p *localMemoizedPlan) execute(ctx context.Context, evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	if evaluator == nil {
		return p.Inner.execute(ctx, evaluator, params)
	}
	key := localMemoKey(p.Expr, params)
	if value, ok := evaluator.memoizedValue(key); ok {
		return cloneRuntimeValue(value), nil
	}
	value, err := p.Inner.execute(ctx, evaluator, params)
	if err != nil {
		return nil, err
	}
	evaluator.storeMemoizedValue(key, cloneRuntimeValue(value))
	return value, nil
}

func (p *localMemoizedPlan) explain() ExplainNode {
	explain := p.Inner.explain()
	explain.RulesApplied = append(explain.RulesApplied, "local_repeated_expression_cache")
	return explain
}

func localMemoKey(expr string, params EvalParams) string {
	return fmt.Sprintf("%s|mode=%s|eval=%d|start=%d|end=%d|step=%d", expr, params.Mode, unixMilli(params.EvaluationTime), unixMilli(params.Start), unixMilli(params.End), params.Step.Milliseconds())
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (e *Evaluator) memoizedValue(key string) (model.RuntimeValue, bool) {
	if e == nil || e.localMemo == nil {
		return nil, false
	}
	value, ok := e.localMemo[key]
	return value, ok
}

func (e *Evaluator) storeMemoizedValue(key string, value model.RuntimeValue) {
	if e == nil {
		return
	}
	if e.localMemo == nil {
		e.localMemo = map[string]model.RuntimeValue{}
	}
	e.localMemo[key] = value
}

func cloneRuntimeValue(value model.RuntimeValue) model.RuntimeValue {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: model.CloneSamples(typed.Samples)}
	case model.MatrixValue:
		return model.MatrixValue{Series: model.CloneSeries(typed.Series)}
	case model.ScalarValue:
		return typed
	default:
		return value
	}
}
