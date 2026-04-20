package promshim

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
		return analyzeDelegatableExpression(e.VectorSelector)
	case *parser.ParenExpr:
		return AnalyzeExpression(e.Expr)
	case *parser.StepInvariantExpr:
		return AnalyzeExpression(e.Expr)
	case *parser.Call:
		return analyzeCallExpression(e, analyzeDelegatableExpression)
	case *parser.AggregateExpr:
		return analyzeAggregateExpression(e)
	case *parser.BinaryExpr:
		if e.VectorMatching != nil || isSetOperator(e.Op.String()) {
			return unsupported(DifficultyHard, "vector matching and set operators are not implemented yet")
		}
		return unsupported(DifficultyMedium, "binary operators are not implemented yet")
	case *parser.SubqueryExpr:
		return unsupported(DifficultyHard, "subqueries are not implemented yet")
	case *parser.UnaryExpr:
		return unsupported(DifficultyMedium, "unary operators are not implemented yet")
	case *parser.NumberLiteral:
		return unsupported(DifficultyMedium, "scalar-only expressions are not implemented yet")
	case *parser.StringLiteral:
		return unsupported(DifficultyMedium, "string-only expressions are not implemented yet")
	default:
		return unsupported(DifficultyHard, fmt.Sprintf("PromQL node %T is not implemented yet", expr))
	}
}

func analyzeDelegatableExpression(expr parser.Expr) SupportResult {
	switch e := expr.(type) {
	case *parser.VectorSelector:
		return SupportResult{Supported: true, Difficulty: DifficultyEasy}
	case *parser.MatrixSelector:
		return analyzeDelegatableExpression(e.VectorSelector)
	case *parser.ParenExpr:
		return analyzeDelegatableExpression(e.Expr)
	case *parser.StepInvariantExpr:
		return analyzeDelegatableExpression(e.Expr)
	case *parser.Call:
		return analyzeCallExpression(e, analyzeDelegatableExpression)
	case *parser.AggregateExpr:
		return unsupported(DifficultyMedium, "nested aggregation operators are not implemented yet")
	case *parser.BinaryExpr:
		if e.VectorMatching != nil || isSetOperator(e.Op.String()) {
			return unsupported(DifficultyHard, "vector matching and set operators are not implemented yet")
		}
		return unsupported(DifficultyMedium, "binary operators are not implemented yet")
	case *parser.SubqueryExpr:
		return unsupported(DifficultyHard, "subqueries are not implemented yet")
	case *parser.UnaryExpr:
		return unsupported(DifficultyMedium, "unary operators are not implemented yet")
	case *parser.NumberLiteral:
		return unsupported(DifficultyMedium, "scalar-only expressions are not implemented yet")
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
	case "label_replace", "label_join":
		return unsupported(DifficultyMedium, "label mutation helpers are not implemented yet")
	case "histogram_quantile", "histogram_fraction", "histogram_avg", "histogram_count", "histogram_sum":
		return unsupported(DifficultyHard, fmt.Sprintf("function %q is not implemented yet", name))
	default:
		return unsupported(DifficultyMedium, fmt.Sprintf("function %q is not implemented yet", name))
	}
}

func analyzeAggregateExpression(expr *parser.AggregateExpr) SupportResult {
	op := strings.ToLower(expr.Op.String())
	if expr.Op != parser.SUM {
		return unsupported(DifficultyMedium, fmt.Sprintf("aggregation operator %q is not implemented yet", op))
	}
	if expr.Param != nil {
		return unsupported(DifficultyMedium, "aggregation parameters are not implemented yet")
	}
	child := analyzeDelegatableExpression(expr.Expr)
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func isSupportedSumAggregation(expr *parser.AggregateExpr) bool {
	return analyzeAggregateExpression(expr).Supported
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
