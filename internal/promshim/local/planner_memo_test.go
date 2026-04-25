package local

import (
	"context"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

type countingMemoPlan struct {
	calls int
}

func (p *countingMemoPlan) execute(context.Context, *Evaluator, EvalParams) (model.RuntimeValue, error) {
	p.calls++
	return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"__name__": "up"}, Timestamp: 1, Value: 7}}}, nil
}

func (p *countingMemoPlan) explain() ExplainNode {
	return ExplainNode{Kind: "counting", Strategy: "local", Expr: "rate(up[5m])"}
}

func TestLocalMemoizedPlanReusesDeepCopiedRuntimeValue(t *testing.T) {
	child := &countingMemoPlan{}
	lhs := memoizeLocalPlan("rate(up[5m])", child)
	rhs := memoizeLocalPlan("rate(up[5m])", child)
	evaluator := &Evaluator{localMemo: map[string]model.RuntimeValue{}}
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(100, 0).UTC()}

	first, err := lhs.execute(context.Background(), evaluator, params)
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	firstVector := first.(model.VectorValue)
	firstVector.Samples[0].Metric["mutated"] = "yes"
	firstVector.Samples[0].Value = 99

	second, err := rhs.execute(context.Background(), evaluator, params)
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if child.calls != 1 {
		t.Fatalf("child calls = %d, want 1", child.calls)
	}
	secondVector := second.(model.VectorValue)
	if secondVector.Samples[0].Value != 7 {
		t.Fatalf("cached value = %v, want original 7", secondVector.Samples[0].Value)
	}
	if _, ok := secondVector.Samples[0].Metric["mutated"]; ok {
		t.Fatalf("cached metric reused mutated map: %#v", secondVector.Samples[0].Metric)
	}
}

func TestBuildPlanMemoizesRepeatedLocalRateOperands(t *testing.T) {
	t.Setenv(DisableLocalRepeatedExpressionCacheEnv, "")
	expr, err := logical.ParseExpression("(rate(up[5m]) + rate(up[5m])) / 2")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, NativeLoweringMode: NativeLoweringModeOff})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	root, ok := built.(*localBinaryPlan)
	if !ok {
		t.Fatalf("expected root binary plan, got %T", built)
	}
	repeated, ok := root.LHS.(*localBinaryPlan)
	if !ok {
		t.Fatalf("expected repeated operand binary plan, got %T", root.LHS)
	}
	if _, ok := repeated.LHS.(*localMemoizedPlan); !ok {
		t.Fatalf("expected memoized left rate operand, got %T", repeated.LHS)
	}
	if _, ok := repeated.RHS.(*localMemoizedPlan); !ok {
		t.Fatalf("expected memoized right rate operand, got %T", repeated.RHS)
	}
}

func TestBuildPlanHonorsLocalRepeatedExpressionCacheDisableEnv(t *testing.T) {
	t.Setenv(DisableLocalRepeatedExpressionCacheEnv, "true")
	expr, err := logical.ParseExpression("rate(up[5m]) + rate(up[5m])")
	if err != nil {
		t.Fatal(err)
	}

	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, NativeLoweringMode: NativeLoweringModeOff})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	repeated, ok := built.(*localBinaryPlan)
	if !ok {
		t.Fatalf("expected binary plan, got %T", built)
	}
	if _, ok := repeated.LHS.(*localMemoizedPlan); ok {
		t.Fatalf("left operand unexpectedly memoized with env gate set")
	}
	if _, ok := repeated.RHS.(*localMemoizedPlan); ok {
		t.Fatalf("right operand unexpectedly memoized with env gate set")
	}
}
