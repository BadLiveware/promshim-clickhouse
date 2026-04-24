package opt

import (
	"fmt"

	"github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// Pass is a pure rewrite over the logical IR. Apply returns the
// (possibly-rewritten) root, whether it made any change, and any error.
// Implementations MUST NOT mutate the input node tree in place.
type Pass interface {
	Name() string
	Apply(root logical.Node, analysis *logical.Analysis) (logical.Node, bool, error)
}

const maxOptimizeIterations = 8

// Optimize runs passes in fixed order, re-analyzing between any rewriting
// pass, until a full iteration produces no change. It errors if the
// fixpoint cap is exceeded.
func Optimize(root logical.Node, passes []Pass) (logical.Node, *logical.Analysis, error) {
	if root == nil {
		return nil, nil, fmt.Errorf("opt: Optimize requires a non-nil root")
	}
	analysis := logical.Analyze(root)
	for iter := 0; iter < maxOptimizeIterations; iter++ {
		changed := false
		for _, p := range passes {
			next, didChange, err := p.Apply(root, analysis)
			if err != nil {
				return nil, nil, fmt.Errorf("opt: pass %q: %w", p.Name(), err)
			}
			if didChange {
				root = next
				analysis = logical.Analyze(root)
				changed = true
			}
		}
		if !changed {
			return root, analysis, nil
		}
	}
	return nil, nil, fmt.Errorf("opt: did not reach fixpoint in %d iterations", maxOptimizeIterations)
}

// DefaultPasses is the canonical ordered pass list applied to every query.
var DefaultPasses = []Pass{
	constantFoldUnaryNegation{},
}
