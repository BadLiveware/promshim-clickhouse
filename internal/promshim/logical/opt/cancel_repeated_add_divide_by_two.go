package opt

import (
	"math"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

type cancelRepeatedAddDivideByTwo struct{}

func (cancelRepeatedAddDivideByTwo) Name() string { return "cancel_repeated_add_divide_by_two" }

func (cancelRepeatedAddDivideByTwo) Metadata() PassMetadata {
	return PassMetadata{
		Name:                  "cancel_repeated_add_divide_by_two",
		Families:              []string{"binary", "range_function"},
		Preconditions:         []string{"root or subtree matches (x + x) / 2", "addition uses implicit one-to-one matching", "both operands are structurally identical", "operands are analysis-proven to drop metric name"},
		PreservedInvariants:   []string{"value_kind", "time_requirements", "label_set", "implicit_vector_matching", "staleness_and_nan_behavior"},
		MetadataProduced:      []string{"optimized_ir_shape"},
		ExpectedSignals:       []string{"FunctionExecute_drop", "queryDurationP50Ms_drop_when_repeated_range_branch_is_removed"},
		RollbackConfiguration: DisableOptimizedIREnv,
	}
}

func (cancelRepeatedAddDivideByTwo) Apply(root logical.Node, analysis *logical.Analysis) (logical.Node, bool, error) {
	newRoot, changed := cancelRepeatedAddDivTwo(root, analysis)
	return newRoot, changed, nil
}

func cancelRepeatedAddDivTwo(node logical.Node, analysis *logical.Analysis) (logical.Node, bool) {
	if node == nil {
		return nil, false
	}
	if replacement, ok := repeatedAddDivTwoReplacement(node, analysis); ok {
		return replacement, true
	}

	switch n := node.(type) {
	case *logical.UnaryPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.BinaryPlan:
		lhs, lhsChanged := cancelRepeatedAddDivTwo(n.LHS, analysis)
		rhs, rhsChanged := cancelRepeatedAddDivTwo(n.RHS, analysis)
		if !lhsChanged && !rhsChanged {
			return n, false
		}
		clone := *n
		clone.LHS = lhs
		clone.RHS = rhs
		return &clone, true
	case *logical.AggregationPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramQuantilePlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramFractionPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramProjectionPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramQuantilesPlan:
		child, childChanged := cancelRepeatedAddDivTwo(n.Child, analysis)
		params, paramsChanged := cancelRepeatedAddDivTwoChildren(n.ParamChildren, analysis)
		if !childChanged && !paramsChanged {
			return n, false
		}
		clone := *n
		clone.Child = child
		clone.ParamChildren = params
		return &clone, true
	case *logical.RangeFunctionPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.VectorPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.RoundPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.SortPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.ScalarConvertPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.InfoPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.PointwiseFunctionPlan:
		child, childChanged := cancelRepeatedAddDivTwo(n.Child, analysis)
		params, paramsChanged := cancelRepeatedAddDivTwoChildren(n.ParamChildren, analysis)
		if !childChanged && !paramsChanged {
			return n, false
		}
		clone := *n
		clone.Child = child
		clone.ParamChildren = params
		return &clone, true
	case *logical.RatePlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.IncreasePlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.DeltaPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.ChangesPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.DerivPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.QuantileOverTimePlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.AbsentPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.AbsentOverTimePlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.SubqueryPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.LabelReplacePlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.LabelJoinPlan:
		child, changed := cancelRepeatedAddDivTwo(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	default:
		return node, false
	}
}

func cancelRepeatedAddDivTwoChildren(children []logical.Node, analysis *logical.Analysis) ([]logical.Node, bool) {
	if len(children) == 0 {
		return children, false
	}
	changed := false
	rewritten := make([]logical.Node, len(children))
	copy(rewritten, children)
	for i, child := range children {
		if child == nil {
			continue
		}
		next, childChanged := cancelRepeatedAddDivTwo(child, analysis)
		if childChanged {
			rewritten[i] = next
			changed = true
		}
	}
	if !changed {
		return children, false
	}
	return rewritten, true
}

func repeatedAddDivTwoReplacement(node logical.Node, analysis *logical.Analysis) (logical.Node, bool) {
	div, ok := node.(*logical.BinaryPlan)
	if !ok || div.Op != parser.DIV || div.ReturnBool || div.VectorMatching != nil {
		return nil, false
	}
	literal, ok := div.RHS.(*logical.ScalarLiteralPlan)
	if !ok || math.Abs(literal.Value-2) > 1e-12 {
		return nil, false
	}
	add, ok := div.LHS.(*logical.BinaryPlan)
	if !ok || add.Op != parser.ADD || add.ReturnBool || !implicitOneToOneMatching(add.VectorMatching) {
		return nil, false
	}
	if add.LHS == nil || add.RHS == nil || nodeExprString(add.LHS) != nodeExprString(add.RHS) {
		return nil, false
	}
	lhsInfo := analysis.InfoFor(add.LHS)
	rhsInfo := analysis.InfoFor(add.RHS)
	if lhsInfo == nil || rhsInfo == nil || !lhsInfo.DropsMetric || !rhsInfo.DropsMetric {
		return nil, false
	}
	return add.LHS, true
}

func nodeExprString(node logical.Node) string {
	if node == nil {
		return ""
	}
	described, ok := node.(interface{ ExprString() string })
	if !ok {
		return ""
	}
	return described.ExprString()
}

func implicitOneToOneMatching(matching *parser.VectorMatching) bool {
	if matching == nil {
		return true
	}
	return matching.Card == parser.CardOneToOne && !matching.On && len(matching.MatchingLabels) == 0 && len(matching.Include) == 0 && matching.FillValues.LHS == nil && matching.FillValues.RHS == nil
}
