package opt_test

import (
	"errors"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/logical/opt"
)

type noopPass struct{}

func (noopPass) Name() string { return "noop" }
func (noopPass) Apply(n logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	return n, false, nil
}

type alwaysErrPass struct{}

func (alwaysErrPass) Name() string { return "alwaysErr" }
func (alwaysErrPass) Apply(_ logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	return nil, false, errors.New("boom")
}

type churnPass struct{}

func (churnPass) Name() string { return "churn" }
func (churnPass) Apply(n logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	if leaf, ok := n.(*logical.LeafExprPlan); ok {
		return &logical.LeafExprPlan{Expr: leaf.Expr}, true, nil
	}
	return n, false, nil
}

func mustLogical(t *testing.T, q string) logical.Node {
	t.Helper()
	expr, err := logical.ParseExpression(q)
	if err != nil {
		t.Fatalf("ParseExpression: %v", err)
	}
	node, err := logical.ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	return node
}

func TestOptimizeNoChangeReachesFixpoint(t *testing.T) {
	root := mustLogical(t, `up`)
	out, _, err := opt.Optimize(root, []opt.Pass{noopPass{}})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if out != root {
		t.Error("noop pass must return same root pointer")
	}
}

func TestOptimizePropagatesError(t *testing.T) {
	root := mustLogical(t, `up`)
	_, _, err := opt.Optimize(root, []opt.Pass{alwaysErrPass{}})
	if err == nil {
		t.Error("expected error propagation")
	}
}

func TestOptimizeCapsIterations(t *testing.T) {
	root := mustLogical(t, `up`)
	_, _, err := opt.Optimize(root, []opt.Pass{churnPass{}})
	if err == nil {
		t.Error("expected fixpoint-cap error")
	}
}
