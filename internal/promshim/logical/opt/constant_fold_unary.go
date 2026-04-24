package opt

import (
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

type constantFoldUnaryNegation struct{}

func (constantFoldUnaryNegation) Name() string { return "constant_fold_unary_negation" }

func (constantFoldUnaryNegation) Apply(root logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	newRoot, changed := foldDoubleNegations(root)
	return newRoot, changed, nil
}

// foldDoubleNegations recursively walks the tree, folding `-(-x)` to `x`
// and rebuilding every parent on the path when a rewrite occurs. Returns
// (node, changed).
func foldDoubleNegations(node logical.Node) (logical.Node, bool) {
	if node == nil {
		return nil, false
	}
	switch n := node.(type) {
	case *logical.UnaryPlan:
		if n.Op == parser.SUB {
			if inner, ok := n.Child.(*logical.UnaryPlan); ok && inner.Op == parser.SUB {
				folded, _ := foldDoubleNegations(inner.Child)
				return folded, true
			}
		}
		child, childChanged := foldDoubleNegations(n.Child)
		if !childChanged {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.BinaryPlan:
		lhs, lhsChanged := foldDoubleNegations(n.LHS)
		rhs, rhsChanged := foldDoubleNegations(n.RHS)
		if !lhsChanged && !rhsChanged {
			return n, false
		}
		clone := *n
		clone.LHS = lhs
		clone.RHS = rhs
		return &clone, true

	case *logical.AggregationPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.HistogramQuantilePlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.HistogramFractionPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.HistogramProjectionPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.HistogramQuantilesPlan:
		childNew, childChanged := foldDoubleNegations(n.Child)
		paramChanged := false
		newParamChildren := make([]logical.Node, len(n.ParamChildren))
		copy(newParamChildren, n.ParamChildren)
		for i, pc := range n.ParamChildren {
			if pc == nil {
				continue
			}
			folded, c := foldDoubleNegations(pc)
			if c {
				newParamChildren[i] = folded
				paramChanged = true
			}
		}
		if !childChanged && !paramChanged {
			return n, false
		}
		clone := *n
		clone.Child = childNew
		clone.ParamChildren = newParamChildren
		return &clone, true

	case *logical.RangeFunctionPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.VectorPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.RoundPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.SortPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.ScalarConvertPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.InfoPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.PointwiseFunctionPlan:
		childNew, childChanged := foldDoubleNegations(n.Child)
		paramChanged := false
		newParamChildren := make([]logical.Node, len(n.ParamChildren))
		copy(newParamChildren, n.ParamChildren)
		for i, pc := range n.ParamChildren {
			if pc == nil {
				continue
			}
			folded, c := foldDoubleNegations(pc)
			if c {
				newParamChildren[i] = folded
				paramChanged = true
			}
		}
		if !childChanged && !paramChanged {
			return n, false
		}
		clone := *n
		clone.Child = childNew
		clone.ParamChildren = newParamChildren
		return &clone, true

	case *logical.RatePlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.IncreasePlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.DeltaPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.ChangesPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.DerivPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.QuantileOverTimePlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.AbsentPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.AbsentOverTimePlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.SubqueryPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.LabelReplacePlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	case *logical.LabelJoinPlan:
		child, changed := foldDoubleNegations(n.Child)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true

	// Leaf kinds — no children to walk.
	// *logical.LeafExprPlan, *logical.ScalarLiteralPlan, *logical.ScalarBuiltinPlan
	default:
		return node, false
	}
}
