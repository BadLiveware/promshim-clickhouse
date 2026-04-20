package plan

import (
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

type SupportResult struct {
	Supported  bool
	Difficulty Difficulty
	Reason     string
}

func ParseExpression(query string) (parser.Expr, error) {
	p := parser.NewParser(parser.Options{})
	return p.ParseExpr(query)
}

func AnalyzeExpression(expr parser.Expr) SupportResult {
	switch e := expr.(type) {
	case *parser.VectorSelector:
		return SupportResult{Supported: true, Difficulty: DifficultyEasy}
	case *parser.MatrixSelector:
		return AnalyzeDelegatableExpression(e.VectorSelector)
	case *parser.ParenExpr:
		return AnalyzeExpression(e.Expr)
	case *parser.StepInvariantExpr:
		return AnalyzeExpression(e.Expr)
	case *parser.Call:
		return analyzeCallExpression(e, AnalyzeDelegatableExpression)
	case *parser.AggregateExpr:
		return AnalyzeAggregateExpression(e)
	case *parser.BinaryExpr:
		return AnalyzeBinaryExpression(e)
	case *parser.SubqueryExpr:
		return unsupported(DifficultyHard, "subqueries are not implemented yet")
	case *parser.UnaryExpr:
		return AnalyzeUnaryExpression(e)
	case *parser.NumberLiteral:
		return SupportResult{Supported: true, Difficulty: DifficultyMedium}
	case *parser.StringLiteral:
		return unsupported(DifficultyMedium, "string-only expressions are not implemented yet")
	default:
		return unsupported(DifficultyHard, fmt.Sprintf("PromQL node %T is not implemented yet", expr))
	}
}

func AnalyzeDelegatableExpression(expr parser.Expr) SupportResult {
	switch e := expr.(type) {
	case *parser.VectorSelector:
		return SupportResult{Supported: true, Difficulty: DifficultyEasy}
	case *parser.MatrixSelector:
		return AnalyzeDelegatableExpression(e.VectorSelector)
	case *parser.ParenExpr:
		return AnalyzeDelegatableExpression(e.Expr)
	case *parser.StepInvariantExpr:
		return AnalyzeDelegatableExpression(e.Expr)
	case *parser.Call:
		return analyzeCallExpression(e, AnalyzeDelegatableExpression)
	case *parser.AggregateExpr:
		return unsupported(DifficultyMedium, "nested aggregation operators are not implemented yet")
	case *parser.BinaryExpr:
		return AnalyzeBinaryExpression(e)
	case *parser.SubqueryExpr:
		return unsupported(DifficultyHard, "subqueries are not implemented yet")
	case *parser.UnaryExpr:
		return AnalyzeUnaryExpression(e)
	case *parser.NumberLiteral:
		return SupportResult{Supported: true, Difficulty: DifficultyMedium}
	case *parser.StringLiteral:
		return unsupported(DifficultyMedium, "string-only expressions are not implemented yet")
	default:
		return unsupported(DifficultyHard, fmt.Sprintf("PromQL node %T is not implemented yet", expr))
	}
}

func analyzeCallExpression(call *parser.Call, recurse func(parser.Expr) SupportResult) SupportResult {
	name := strings.ToLower(call.Func.Name)
	if isSupportedLeafFunction(name) {
		for _, arg := range call.Args {
			result := recurse(arg)
			if !result.Supported {
				return result
			}
		}
		return SupportResult{Supported: true, Difficulty: DifficultyEasy}
	}

	switch name {
	case "label_replace":
		return AnalyzeLabelReplaceCall(call)
	case "label_join":
		return AnalyzeLabelJoinCall(call)
	case "histogram_quantile", "histogram_fraction", "histogram_avg", "histogram_count", "histogram_sum":
		return unsupported(DifficultyHard, fmt.Sprintf("function %q is not implemented yet", name))
	default:
		return unsupported(DifficultyMedium, fmt.Sprintf("function %q is not implemented yet", name))
	}
}

func AnalyzeLabelReplaceCall(call *parser.Call) SupportResult {
	if len(call.Args) != 5 {
		return unsupported(DifficultyMedium, "label_replace requires five arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyMedium, "label_replace requires a vector argument")
	}
	result := AnalyzeExpression(call.Args[0])
	if !result.Supported {
		return result
	}
	for _, arg := range call.Args[1:] {
		if arg.Type() != parser.ValueTypeString {
			return unsupported(DifficultyMedium, "label_replace string arguments are not implemented yet")
		}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeLabelJoinCall(call *parser.Call) SupportResult {
	if len(call.Args) < 4 {
		return unsupported(DifficultyMedium, "label_join requires at least four arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyMedium, "label_join requires a vector argument")
	}
	result := AnalyzeExpression(call.Args[0])
	if !result.Supported {
		return result
	}
	for _, arg := range call.Args[1:] {
		if arg.Type() != parser.ValueTypeString {
			return unsupported(DifficultyMedium, "label_join string arguments are not implemented yet")
		}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeAggregateExpression(expr *parser.AggregateExpr) SupportResult {
	op := strings.ToLower(expr.Op.String())
	if !isSupportedLocalAggregation(expr.Op) {
		return unsupported(DifficultyMedium, fmt.Sprintf("aggregation operator %q is not implemented yet", op))
	}
	if expr.Param != nil {
		return unsupported(DifficultyMedium, "aggregation parameters are not implemented yet")
	}
	child := AnalyzeDelegatableExpression(expr.Expr)
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func isSupportedLocalAggregation(op parser.ItemType) bool {
	switch op {
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG:
		return true
	default:
		return false
	}
}

func AnalyzeBinaryExpression(expr *parser.BinaryExpr) SupportResult {
	if expr.VectorMatching != nil || isSetOperator(expr.Op.String()) {
		return unsupported(DifficultyHard, "vector matching and set operators are not implemented yet")
	}
	if !isSupportedLocalBinaryOperator(expr.Op) {
		return unsupported(DifficultyMedium, fmt.Sprintf("binary operator %q is not implemented yet", expr.Op.String()))
	}

	lhsType, rhsType := expr.LHS.Type(), expr.RHS.Type()
	supportedShape := (lhsType == parser.ValueTypeScalar && rhsType == parser.ValueTypeScalar) ||
		(lhsType == parser.ValueTypeVector && rhsType == parser.ValueTypeScalar) ||
		(lhsType == parser.ValueTypeScalar && rhsType == parser.ValueTypeVector)
	if !supportedShape {
		return unsupported(DifficultyMedium, "only scalar-scalar and vector-scalar binary expressions are implemented yet")
	}

	lhs := AnalyzeExpression(expr.LHS)
	if !lhs.Supported {
		return lhs
	}
	rhs := AnalyzeExpression(expr.RHS)
	if !rhs.Supported {
		return rhs
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeUnaryExpression(expr *parser.UnaryExpr) SupportResult {
	if expr.Op != parser.ADD && expr.Op != parser.SUB {
		return unsupported(DifficultyMedium, fmt.Sprintf("unary operator %q is not implemented yet", expr.Op.String()))
	}
	typeOf := expr.Expr.Type()
	if typeOf != parser.ValueTypeScalar && typeOf != parser.ValueTypeVector {
		return unsupported(DifficultyMedium, fmt.Sprintf("unary expression only allowed on scalar or instant vector, got %q", typeOf))
	}
	child := AnalyzeExpression(expr.Expr)
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func isSupportedLocalBinaryOperator(op parser.ItemType) bool {
	switch op {
	case parser.ADD, parser.SUB, parser.MUL, parser.DIV, parser.MOD, parser.POW,
		parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func unsupported(d Difficulty, reason string) SupportResult {
	return SupportResult{Supported: false, Difficulty: d, Reason: reason}
}

func isSupportedLeafFunction(name string) bool {
	switch name {
	case "rate", "irate", "increase", "delta", "idelta", "deriv", "changes":
		return true
	default:
		return false
	}
}

func isSetOperator(op string) bool {
	switch strings.ToLower(op) {
	case "and", "or", "unless":
		return true
	default:
		return false
	}
}
