package logical

import (
	"testing"
)

func TestToLogicalLeafPlanForSelector(t *testing.T) {
	expr, err := ParseExpression(`up{job="api"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	node, err := ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	leaf, ok := node.(*LeafExprPlan)
	if !ok {
		t.Fatalf("expected *LeafExprPlan, got %T", node)
	}
	if leaf.Expr.String() != `up{job="api"}` {
		t.Fatalf("expr roundtrip: %q", leaf.Expr.String())
	}
}

func TestToLogicalRejectsUnsupportedExpressions(t *testing.T) {
	_, err := ToLogical(nil)
	if err == nil {
		t.Fatal("expected error for nil expression")
	}
}
