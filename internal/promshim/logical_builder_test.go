package promshim

import (
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildLogicalPlanCreatesDelegatedLeafPlan(t *testing.T) {
	expr, err := plan.ParseExpression("up")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical leaf plan, got error: %v", err)
	}
	leaf, ok := plan.(*logicalLeafExprPlan)
	if !ok {
		t.Fatalf("expected logicalLeafExprPlan, got %T", plan)
	}
	if leaf.ExprString() != "up" {
		t.Fatalf("expected expr string to be preserved, got %q", leaf.ExprString())
	}
	if leaf.ValueType() != parser.ValueTypeVector {
		t.Fatalf("expected vector value type, got %q", leaf.ValueType())
	}
}

func TestBuildLogicalPlanCreatesAggregationPlan(t *testing.T) {
	expr, err := plan.ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical aggregation plan, got error: %v", err)
	}
	agg, ok := plan.(*logicalAggregationPlan)
	if !ok {
		t.Fatalf("expected logicalAggregationPlan, got %T", plan)
	}
	if agg.Op != parser.SUM {
		t.Fatalf("expected sum op, got %v", agg.Op)
	}
	if !sameStrings(agg.Grouping, []string{"job"}) {
		t.Fatalf("unexpected grouping: %#v", agg.Grouping)
	}
	if _, ok := agg.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected logical leaf child, got %T", agg.Child)
	}
}

func TestBuildLogicalPlanCreatesLabelReplacePlan(t *testing.T) {
	expr, err := plan.ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildLogicalPlan(expr)
	if err != nil {
		t.Fatalf("expected logical label_replace plan, got error: %v", err)
	}
	labelPlan, ok := plan.(*logicalLabelReplacePlan)
	if !ok {
		t.Fatalf("expected logicalLabelReplacePlan, got %T", plan)
	}
	if labelPlan.Config.Dst != "job_copy" || labelPlan.Config.Src != "job" {
		t.Fatalf("unexpected label_replace config: %#v", labelPlan.Config)
	}
	if _, ok := labelPlan.Child.(*logicalLeafExprPlan); !ok {
		t.Fatalf("expected logical leaf child, got %T", labelPlan.Child)
	}
}

func TestBuildExecPlanLowersLogicalAggregationPlan(t *testing.T) {
	logical := &logicalAggregationPlan{
		Expr:     mustParseExpr(t, "sum by (job) (up)"),
		Op:       parser.SUM,
		Grouping: []string{"job"},
		Child:    &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
	}

	plan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution aggregation plan, got error: %v", err)
	}
	agg, ok := plan.(*localAggregationPlan)
	if !ok {
		t.Fatalf("expected localAggregationPlan, got %T", plan)
	}
	if agg.Op != parser.SUM {
		t.Fatalf("expected sum op, got %v", agg.Op)
	}
	if _, ok := agg.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated child, got %T", agg.Child)
	}
}

func TestBuildExecPlanLowersLogicalLabelJoinPlan(t *testing.T) {
	cfg, err := model.BuildLabelJoinConfig("joined", "/", []string{"job", "namespace"})
	if err != nil {
		t.Fatal(err)
	}
	logical := &logicalLabelJoinPlan{
		Expr:   mustParseExpr(t, `label_join(up, "joined", "/", "job", "namespace")`),
		Config: cfg,
		Child:  &logicalLeafExprPlan{Expr: mustParseExpr(t, "up")},
	}

	plan, err := buildExecPlan(logical)
	if err != nil {
		t.Fatalf("expected execution label_join plan, got error: %v", err)
	}
	joinPlan, ok := plan.(*localLabelJoinPlan)
	if !ok {
		t.Fatalf("expected localLabelJoinPlan, got %T", plan)
	}
	if joinPlan.Config.Dst != "joined" {
		t.Fatalf("unexpected label_join config: %#v", joinPlan.Config)
	}
	if _, ok := joinPlan.Child.(*delegatedExprPlan); !ok {
		t.Fatalf("expected delegated child, got %T", joinPlan.Child)
	}
}

func mustParseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := plan.ParseExpression(query)
	if err != nil {
		t.Fatalf("plan.ParseExpression(%q): %v", query, err)
	}
	return expr
}
