package promshim

import (
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

type logicalPlan interface {
	logicalPlan()
	valueType() parser.ValueType
	exprString() string
}

type logicalLeafExprPlan struct {
	Expr parser.Expr
}

func (*logicalLeafExprPlan) logicalPlan() {}

func (p *logicalLeafExprPlan) valueType() parser.ValueType {
	return p.Expr.Type()
}

func (p *logicalLeafExprPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

type logicalScalarLiteralPlan struct {
	Expr  parser.Expr
	Value float64
}

func (*logicalScalarLiteralPlan) logicalPlan() {}

func (p *logicalScalarLiteralPlan) valueType() parser.ValueType {
	return parser.ValueTypeScalar
}

func (p *logicalScalarLiteralPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

type logicalUnaryPlan struct {
	Expr  parser.Expr
	Op    parser.ItemType
	Child logicalPlan
}

func (*logicalUnaryPlan) logicalPlan() {}

func (p *logicalUnaryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}

func (p *logicalUnaryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

type logicalBinaryPlan struct {
	Expr       parser.Expr
	Op         parser.ItemType
	ReturnBool bool
	LHS        logicalPlan
	RHS        logicalPlan
}

func (*logicalBinaryPlan) logicalPlan() {}

func (p *logicalBinaryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}

func (p *logicalBinaryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

type logicalAggregationPlan struct {
	Expr     parser.Expr
	Op       parser.ItemType
	Grouping []string
	Without  bool
	Child    logicalPlan
}

func (*logicalAggregationPlan) logicalPlan() {}

func (p *logicalAggregationPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}

func (p *logicalAggregationPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

type logicalLabelReplacePlan struct {
	Expr   parser.Expr
	Config localLabelReplaceConfig
	Child  logicalPlan
}

func (*logicalLabelReplacePlan) logicalPlan() {}

func (p *logicalLabelReplacePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}

func (p *logicalLabelReplacePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

type logicalLabelJoinPlan struct {
	Expr   parser.Expr
	Config localLabelJoinConfig
	Child  logicalPlan
}

func (*logicalLabelJoinPlan) logicalPlan() {}

func (p *logicalLabelJoinPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}

func (p *logicalLabelJoinPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}

func buildLogicalPlan(expr parser.Expr) (logicalPlan, error) {
	expr = unwrapTransparentExpr(expr)

	switch node := expr.(type) {
	case *parser.NumberLiteral:
		return &logicalScalarLiteralPlan{Expr: node, Value: node.Val}, nil
	case *parser.Call:
		return buildLogicalCallPlan(node)
	case *parser.UnaryExpr:
		result := analyzeUnaryExpression(node)
		if !result.Supported {
			return nil, newPlanBuildError(node, result, "unary planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for unary expression %q", node.String())
		}
		return &logicalUnaryPlan{Expr: node, Op: node.Op, Child: child}, nil
	case *parser.BinaryExpr:
		result := analyzeBinaryExpression(node)
		if !result.Supported {
			return nil, newPlanBuildError(node, result, "binary planning")
		}
		lhs, err := buildLogicalPlan(node.LHS)
		if err != nil {
			return nil, withInternalContext(err, "building logical left operand plan for binary expression %q", node.String())
		}
		rhs, err := buildLogicalPlan(node.RHS)
		if err != nil {
			return nil, withInternalContext(err, "building logical right operand plan for binary expression %q", node.String())
		}
		return &logicalBinaryPlan{Expr: node, Op: node.Op, ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, nil
	case *parser.AggregateExpr:
		result := analyzeAggregateExpression(node)
		if !result.Supported {
			return nil, newPlanBuildError(node, result, "aggregate planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for aggregate %q", node.String())
		}
		return &logicalAggregationPlan{
			Expr:     node,
			Op:       node.Op,
			Grouping: append([]string(nil), node.Grouping...),
			Without:  node.Without,
			Child:    child,
		}, nil
	default:
		return buildLogicalDelegatedLeaf(expr)
	}
}

func buildLogicalDelegatedLeaf(expr parser.Expr) (logicalPlan, error) {
	expr = unwrapTransparentExpr(expr)
	result := analyzeDelegatableExpression(expr)
	if !result.Supported {
		return nil, newPlanBuildError(expr, result, "delegated leaf planning")
	}
	return &logicalLeafExprPlan{Expr: expr}, nil
}

func buildLogicalCallPlan(call *parser.Call) (logicalPlan, error) {
	name := strings.ToLower(call.Func.Name)
	switch name {
	case "label_replace":
		if result := analyzeLabelReplaceCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for label_replace %q", call.String())
		}
		dst, err := stringLiteralArgument(call.Args[1], "label_replace destination label")
		if err != nil {
			return nil, withInternalContext(err, "building logical label_replace %q", call.String())
		}
		repl, err := stringLiteralArgument(call.Args[2], "label_replace replacement")
		if err != nil {
			return nil, withInternalContext(err, "building logical label_replace %q", call.String())
		}
		src, err := stringLiteralArgument(call.Args[3], "label_replace source label")
		if err != nil {
			return nil, withInternalContext(err, "building logical label_replace %q", call.String())
		}
		regexStr, err := stringLiteralArgument(call.Args[4], "label_replace regex")
		if err != nil {
			return nil, withInternalContext(err, "building logical label_replace %q", call.String())
		}
		cfg, err := buildLabelReplaceConfig(dst, repl, src, regexStr)
		if err != nil {
			return nil, withInternalContext(err, "building logical label_replace %q", call.String())
		}
		return &logicalLabelReplacePlan{Expr: call, Config: cfg, Child: child}, nil
	case "label_join":
		if result := analyzeLabelJoinCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for label_join %q", call.String())
		}
		dst, err := stringLiteralArgument(call.Args[1], "label_join destination label")
		if err != nil {
			return nil, withInternalContext(err, "building logical label_join %q", call.String())
		}
		sep, err := stringLiteralArgument(call.Args[2], "label_join separator")
		if err != nil {
			return nil, withInternalContext(err, "building logical label_join %q", call.String())
		}
		srcLabels := make([]string, 0, len(call.Args)-3)
		for _, arg := range call.Args[3:] {
			src, err := stringLiteralArgument(arg, "label_join source label")
			if err != nil {
				return nil, withInternalContext(err, "building logical label_join %q", call.String())
			}
			srcLabels = append(srcLabels, src)
		}
		cfg, err := buildLabelJoinConfig(dst, sep, srcLabels)
		if err != nil {
			return nil, withInternalContext(err, "building logical label_join %q", call.String())
		}
		return &logicalLabelJoinPlan{Expr: call, Config: cfg, Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(call)
	}
}

func stringLiteralArgument(expr parser.Expr, description string) (string, error) {
	expr = unwrapTransparentExpr(expr)
	literal, ok := expr.(*parser.StringLiteral)
	if !ok {
		return "", newBadDataErrorf("expected string literal for %s, got %T", description, expr)
	}
	return literal.Val, nil
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
