package logical

import (
	"math"
	"strings"

	commonmodel "github.com/prometheus/common/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// UnwrapTransparentExpr unwraps parenthesised and step-invariant expressions,
// returning the innermost non-transparent node. Exported so callers in other
// packages (e.g. local.delegated_support) can reuse the same logic.
func UnwrapTransparentExpr(expr parser.Expr) parser.Expr {
	for {
		switch e := expr.(type) {
		case *parser.ParenExpr:
			expr = e.Expr
		case *parser.StepInvariantExpr:
			expr = e.Expr
		default:
			return expr
		}
	}
}

// ExpressionContainsSubquery reports whether the expression tree rooted at
// expr contains a SubqueryExpr node.
func ExpressionContainsSubquery(expr parser.Expr) bool {
	expr = UnwrapTransparentExpr(expr)
	switch node := expr.(type) {
	case *parser.SubqueryExpr:
		return true
	case *parser.Call:
		for _, arg := range node.Args {
			if ExpressionContainsSubquery(arg) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		if node.Param != nil && ExpressionContainsSubquery(node.Param) {
			return true
		}
		return ExpressionContainsSubquery(node.Expr)
	case *parser.BinaryExpr:
		return ExpressionContainsSubquery(node.LHS) || ExpressionContainsSubquery(node.RHS)
	case *parser.UnaryExpr:
		return ExpressionContainsSubquery(node.Expr)
	case *parser.ParenExpr, *parser.StepInvariantExpr:
		return false
	case *parser.MatrixSelector:
		return ExpressionContainsSubquery(node.VectorSelector)
	default:
		return false
	}
}

func aggregatePlanParam(expr *parser.AggregateExpr) (*float64, string, error) {
	if expr == nil || expr.Param == nil {
		return nil, "", nil
	}
	switch expr.Op {
	case parser.TOPK, parser.BOTTOMK, parser.QUANTILE, parser.LIMITK, parser.LIMIT_RATIO:
		literal, ok := UnwrapTransparentExpr(expr.Param).(*parser.NumberLiteral)
		if !ok {
			return nil, "", NewUnsupportedErrorf("aggregation operator %q currently requires a literal scalar parameter", strings.ToLower(expr.Op.String()))
		}
		value := literal.Val
		return &value, "", nil
	case parser.COUNT_VALUES:
		label, err := stringLiteralArgument(expr.Param, "count_values label parameter")
		if err != nil {
			return nil, "", err
		}
		if !commonmodel.UTF8Validation.IsValidLabelName(label) {
			return nil, "", NewBadDataErrorf("invalid destination label name in count_values(): %s", label)
		}
		return nil, label, nil
	default:
		return nil, "", nil
	}
}

func buildLogicalPointwiseFunctionPlan(name string, call *parser.Call) (Node, error) {
	paramNumbers := make([]*float64, 0, max(0, len(call.Args)-1))
	argOffset := 0
	if name == "clamp" || name == "clamp_min" || name == "clamp_max" {
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		paramChildren := make([]Node, 0, len(call.Args)-1)
		for _, arg := range call.Args[1:] {
			builtArg, err := buildLogicalPlan(arg)
			if err != nil {
				return nil, withBuildContext(err, "building logical %s scalar parameter for %q", name, call.String())
			}
			paramChildren = append(paramChildren, builtArg)
			if literal, ok := UnwrapTransparentExpr(arg).(*parser.NumberLiteral); ok {
				valueCopy := literal.Val
				paramNumbers = append(paramNumbers, &valueCopy)
			} else {
				paramNumbers = append(paramNumbers, nil)
			}
		}
		return &PointwiseFunctionPlan{Expr: call, Func: name, ParamNumbers: paramNumbers, ParamChildren: paramChildren, Child: child}, nil
	}
	if len(call.Args) > 0 {
		argOffset = 1
	}
	var child Node
	if argOffset == 1 {
		builtChild, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		child = builtChild
	}
	return &PointwiseFunctionPlan{Expr: call, Func: name, Child: child}, nil
}

func clonePromMatchers(matchers []*promlabels.Matcher) []*promlabels.Matcher {
	if len(matchers) == 0 {
		return nil
	}
	out := make([]*promlabels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		out = append(out, promlabels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return out
}

func stringLiteralArgument(expr parser.Expr, description string) (string, error) {
	expr = UnwrapTransparentExpr(expr)
	literal, ok := expr.(*parser.StringLiteral)
	if !ok {
		return "", NewBadDataErrorf("expected string literal for %s, got %T", description, expr)
	}
	return literal.Val, nil
}

func numberLiteralArgument(expr parser.Expr, description string) (float64, error) {
	value, ok := scalarConstantValue(expr)
	if !ok {
		return 0, NewBadDataErrorf("expected constant scalar expression for %s, got %T", description, UnwrapTransparentExpr(expr))
	}
	return value, nil
}

func scalarConstantValue(expr parser.Expr) (float64, bool) {
	switch node := UnwrapTransparentExpr(expr).(type) {
	case *parser.NumberLiteral:
		return node.Val, true
	case *parser.UnaryExpr:
		value, ok := scalarConstantValue(node.Expr)
		if !ok {
			return 0, false
		}
		switch node.Op {
		case parser.ADD:
			return value, true
		case parser.SUB:
			return -value, true
		default:
			return 0, false
		}
	case *parser.BinaryExpr:
		left, ok := scalarConstantValue(node.LHS)
		if !ok {
			return 0, false
		}
		right, ok := scalarConstantValue(node.RHS)
		if !ok {
			return 0, false
		}
		switch node.Op {
		case parser.ADD:
			return left + right, true
		case parser.SUB:
			return left - right, true
		case parser.MUL:
			return left * right, true
		case parser.DIV:
			return left / right, true
		case parser.MOD:
			return math.Mod(left, right), true
		case parser.POW:
			return math.Pow(left, right), true
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}

func cloneFloat64(value float64) *float64 {
	cloned := value
	return &cloned
}

func deriveAbsentOutputMetric(expr parser.Expr) map[string]string {
	expr = UnwrapTransparentExpr(expr)

	var matchers []*promlabels.Matcher
	switch node := expr.(type) {
	case *parser.VectorSelector:
		matchers = node.LabelMatchers
	case *parser.MatrixSelector:
		vectorSelector, ok := node.VectorSelector.(*parser.VectorSelector)
		if !ok {
			return map[string]string{}
		}
		matchers = vectorSelector.LabelMatchers
	default:
		return map[string]string{}
	}

	result := make(map[string]string)
	has := make(map[string]bool, len(matchers))
	for _, matcher := range matchers {
		if matcher.Name == "__name__" {
			continue
		}
		if matcher.Type == promlabels.MatchEqual && !has[matcher.Name] {
			result[matcher.Name] = matcher.Value
			has[matcher.Name] = true
			continue
		}
		delete(result, matcher.Name)
	}
	return result
}

func cloneVectorMatching(vectorMatching *parser.VectorMatching) *parser.VectorMatching {
	if vectorMatching == nil {
		return nil
	}
	cloned := &parser.VectorMatching{
		Card:           vectorMatching.Card,
		MatchingLabels: append([]string(nil), vectorMatching.MatchingLabels...),
		On:             vectorMatching.On,
		Include:        append([]string(nil), vectorMatching.Include...),
	}
	if vectorMatching.FillValues.LHS != nil {
		lhs := *vectorMatching.FillValues.LHS
		cloned.FillValues.LHS = &lhs
	}
	if vectorMatching.FillValues.RHS != nil {
		rhs := *vectorMatching.FillValues.RHS
		cloned.FillValues.RHS = &rhs
	}
	return cloned
}
