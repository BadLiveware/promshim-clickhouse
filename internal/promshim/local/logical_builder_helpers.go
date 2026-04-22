package local

import (
	"strings"

	commonmodel "github.com/prometheus/common/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func aggregatePlanParam(expr *parser.AggregateExpr) (*float64, string, error) {
	if expr == nil || expr.Param == nil {
		return nil, "", nil
	}
	switch expr.Op {
	case parser.TOPK, parser.BOTTOMK, parser.QUANTILE:
		literal, ok := unwrapTransparentExpr(expr.Param).(*parser.NumberLiteral)
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

func buildLogicalPointwiseFunctionPlan(name string, call *parser.Call) (logicalPlan, error) {
	paramNumbers := make([]*float64, 0, max(0, len(call.Args)-1))
	argOffset := 0
	if name == "clamp" || name == "clamp_min" || name == "clamp_max" {
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		for _, arg := range call.Args[1:] {
			value, err := numberLiteralArgument(arg, name+" scalar parameter")
			if err != nil {
				return nil, WithInternalContext(err, "building logical %s %q", name, call.String())
			}
			valueCopy := value
			paramNumbers = append(paramNumbers, &valueCopy)
		}
		return &logicalPointwiseFunctionPlan{Expr: call, Func: name, ParamNumbers: paramNumbers, Child: child}, nil
	}
	if len(call.Args) > 0 {
		argOffset = 1
	}
	var child logicalPlan
	if argOffset == 1 {
		builtChild, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		child = builtChild
	}
	return &logicalPointwiseFunctionPlan{Expr: call, Func: name, Child: child}, nil
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
	expr = unwrapTransparentExpr(expr)
	literal, ok := expr.(*parser.StringLiteral)
	if !ok {
		return "", NewBadDataErrorf("expected string literal for %s, got %T", description, expr)
	}
	return literal.Val, nil
}

func numberLiteralArgument(expr parser.Expr, description string) (float64, error) {
	expr = unwrapTransparentExpr(expr)
	literal, ok := expr.(*parser.NumberLiteral)
	if !ok {
		return 0, NewBadDataErrorf("expected numeric literal for %s, got %T", description, expr)
	}
	return literal.Val, nil
}

func cloneFloat64(value float64) *float64 {
	cloned := value
	return &cloned
}

func deriveAbsentOutputMetric(expr parser.Expr) map[string]string {
	expr = unwrapTransparentExpr(expr)

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
		if matcher.Name == promlabels.MetricName {
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

func unwrapTransparentExpr(expr parser.Expr) parser.Expr {
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

func expressionContainsSubquery(expr parser.Expr) bool {
	expr = unwrapTransparentExpr(expr)
	switch node := expr.(type) {
	case *parser.SubqueryExpr:
		return true
	case *parser.Call:
		for _, arg := range node.Args {
			if expressionContainsSubquery(arg) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		if node.Param != nil && expressionContainsSubquery(node.Param) {
			return true
		}
		return expressionContainsSubquery(node.Expr)
	case *parser.BinaryExpr:
		return expressionContainsSubquery(node.LHS) || expressionContainsSubquery(node.RHS)
	case *parser.UnaryExpr:
		return expressionContainsSubquery(node.Expr)
	case *parser.ParenExpr, *parser.StepInvariantExpr:
		return false
	case *parser.MatrixSelector:
		return expressionContainsSubquery(node.VectorSelector)
	default:
		return false
	}
}
