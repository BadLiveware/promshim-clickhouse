package opt

import (
	"math"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

const maxRepeatedAverageTerms = 16

type cancelRepeatedAverage struct{}

func (cancelRepeatedAverage) Name() string { return "cancel_repeated_average" }

func (cancelRepeatedAverage) Metadata() PassMetadata {
	return PassMetadata{
		Name:     "cancel_repeated_average",
		Families: []string{"binary", "range_function"},
		Preconditions: []string{
			"root or subtree matches (x + x + ... + x) / n",
			"divisor is the exact repeated term count",
			"all additions use implicit one-to-one matching",
			"all operands are structurally identical",
			"all operands are analysis-proven to drop metric name",
		},
		PreservedInvariants:   []string{"value_kind", "time_requirements", "label_set", "implicit_vector_matching", "staleness_and_nan_behavior"},
		MetadataProduced:      []string{"optimized_ir_shape"},
		ExpectedSignals:       []string{"FunctionExecute_drop", "queryDurationP50Ms_drop_when_repeated_range_branches_are_removed"},
		RollbackConfiguration: DisableOptimizedIREnv,
	}
}

func (cancelRepeatedAverage) Apply(root logical.Node, analysis *logical.Analysis) (logical.Node, bool, error) {
	newRoot, changed := cancelRepeatedAverageInTree(root, analysis)
	return newRoot, changed, nil
}

// repeatedAverageReplacement deliberately handles only averages of identical
// metric-name-dropping operands. It must not grow into a generic arithmetic
// simplifier without separate PromQL semantic proof for labels, vector matching,
// staleness, NaN/Inf, and signed-zero behavior.
func repeatedAverageReplacement(node logical.Node, analysis *logical.Analysis) (logical.Node, bool) {
	div, ok := node.(*logical.BinaryPlan)
	if !ok || div.Op != parser.DIV || div.ReturnBool || div.VectorMatching != nil {
		return nil, false
	}
	literal, ok := div.RHS.(*logical.ScalarLiteralPlan)
	if !ok {
		return nil, false
	}
	termCount, ok := repeatedAverageDivisor(literal.Value)
	if !ok {
		return nil, false
	}
	terms, ok := collectImplicitRepeatedAddTerms(div.LHS)
	if !ok || len(terms) != termCount {
		return nil, false
	}
	if len(terms) == 0 {
		return nil, false
	}
	firstExpr := nodeExprString(terms[0])
	if firstExpr == "" {
		return nil, false
	}
	for _, term := range terms {
		if term == nil || nodeExprString(term) != firstExpr {
			return nil, false
		}
		info := analysis.InfoFor(term)
		if info == nil || !info.DropsMetric {
			return nil, false
		}
	}
	return terms[0], true
}

func repeatedAverageDivisor(value float64) (int, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
		return 0, false
	}
	if value < 2 || value > maxRepeatedAverageTerms {
		return 0, false
	}
	return int(value), true
}

func collectImplicitRepeatedAddTerms(node logical.Node) ([]logical.Node, bool) {
	if node == nil {
		return nil, false
	}
	add, ok := node.(*logical.BinaryPlan)
	if !ok || add.Op != parser.ADD {
		return []logical.Node{node}, true
	}
	if add.ReturnBool || !implicitOneToOneMatching(add.VectorMatching) {
		return nil, false
	}
	lhs, ok := collectImplicitRepeatedAddTerms(add.LHS)
	if !ok {
		return nil, false
	}
	rhs, ok := collectImplicitRepeatedAddTerms(add.RHS)
	if !ok {
		return nil, false
	}
	terms := make([]logical.Node, 0, len(lhs)+len(rhs))
	terms = append(terms, lhs...)
	terms = append(terms, rhs...)
	return terms, true
}

func cancelRepeatedAverageInTree(node logical.Node, analysis *logical.Analysis) (logical.Node, bool) {
	if node == nil {
		return nil, false
	}
	if replacement, ok := repeatedAverageReplacement(node, analysis); ok {
		return replacement, true
	}

	switch n := node.(type) {
	case *logical.UnaryPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.BinaryPlan:
		lhs, lhsChanged := cancelRepeatedAverageInTree(n.LHS, analysis)
		rhs, rhsChanged := cancelRepeatedAverageInTree(n.RHS, analysis)
		if !lhsChanged && !rhsChanged {
			return n, false
		}
		clone := *n
		clone.LHS = lhs
		clone.RHS = rhs
		return &clone, true
	case *logical.AggregationPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramQuantilePlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramFractionPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramProjectionPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.HistogramQuantilesPlan:
		child, childChanged := cancelRepeatedAverageInTree(n.Child, analysis)
		params, paramsChanged := cancelRepeatedAverageChildren(n.ParamChildren, analysis)
		if !childChanged && !paramsChanged {
			return n, false
		}
		clone := *n
		clone.Child = child
		clone.ParamChildren = params
		return &clone, true
	case *logical.RangeFunctionPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.VectorPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.RoundPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.SortPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.ScalarConvertPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.InfoPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.PointwiseFunctionPlan:
		child, childChanged := cancelRepeatedAverageInTree(n.Child, analysis)
		params, paramsChanged := cancelRepeatedAverageChildren(n.ParamChildren, analysis)
		if !childChanged && !paramsChanged {
			return n, false
		}
		clone := *n
		clone.Child = child
		clone.ParamChildren = params
		return &clone, true
	case *logical.RatePlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.IncreasePlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.DeltaPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.ChangesPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.DerivPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.QuantileOverTimePlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.AbsentPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.AbsentOverTimePlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.SubqueryPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.LabelReplacePlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
		if !changed {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.LabelJoinPlan:
		child, changed := cancelRepeatedAverageInTree(n.Child, analysis)
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

func cancelRepeatedAverageChildren(children []logical.Node, analysis *logical.Analysis) ([]logical.Node, bool) {
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
		next, childChanged := cancelRepeatedAverageInTree(child, analysis)
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
