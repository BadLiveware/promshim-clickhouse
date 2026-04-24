package opt_test

import (
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/logical/opt"
)

func TestConstantFoldDoubleNegation(t *testing.T) {
	root := mustLogical(t, `- -up`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.LeafExprPlan); !ok {
		t.Fatalf("expected LeafExprPlan after fold, got %T", out)
	}
}

func TestConstantFoldSingleNegationUnchanged(t *testing.T) {
	root := mustLogical(t, `-up`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.UnaryPlan); !ok {
		t.Fatalf("expected UnaryPlan unchanged, got %T", out)
	}
}
