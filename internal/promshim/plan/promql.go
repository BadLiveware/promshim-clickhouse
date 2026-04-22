package plan

import (
	"fmt"
	"regexp"
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

var holtWintersAliasPattern = regexp.MustCompile(`\bholt_winters\s*\(`)

func ParseExpression(query string) (parser.Expr, error) {
	query = holtWintersAliasPattern.ReplaceAllString(query, "double_exponential_smoothing(")
	p := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true})
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
		return AnalyzeSubqueryExpression(e)
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
		return AnalyzeAggregateExpression(e)
	case *parser.BinaryExpr:
		return AnalyzeBinaryExpression(e)
	case *parser.SubqueryExpr:
		return AnalyzeDelegatableSubqueryExpression(e)
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
	if name == "increase" {
		return AnalyzeIncreaseCall(call)
	}
	if name == "vector" {
		return AnalyzeVectorCall(call)
	}
	if name == "scalar" {
		return AnalyzeScalarCall(call)
	}
	if name == "info" {
		return AnalyzeInfoCall(call)
	}
	if name == "round" {
		return AnalyzeRoundCall(call)
	}
	if name == "clamp" || name == "clamp_min" || name == "clamp_max" {
		return AnalyzeClampCall(name, call)
	}
	if name == "sort" || name == "sort_desc" || name == "sort_by_label" || name == "sort_by_label_desc" {
		return AnalyzeSortCall(name, call)
	}
	if name == "rate" {
		return AnalyzeRateCall(call)
	}
	if name == "irate" {
		return AnalyzeIrateCall(call)
	}
	if name == "delta" {
		return AnalyzeDeltaCall(call)
	}
	if name == "idelta" {
		return AnalyzeIDeltaCall(call)
	}
	if name == "changes" {
		return AnalyzeChangesCall(call)
	}
	if name == "deriv" {
		return AnalyzeDerivCall(call)
	}
	if name == "resets" {
		return AnalyzeRangeFunctionCall(name, call)
	}
	if name == "predict_linear" {
		return AnalyzePredictLinearCall(call)
	}
	if name == "double_exponential_smoothing" || name == "holt_winters" {
		return AnalyzeDoubleExponentialSmoothingCall(call)
	}
	if isSupportedLeafFunction(name) {
		if isSubqueryUnsupportedFunction(name) {
			for _, arg := range call.Args {
				if containsSubqueryExpr(arg) {
					return unsupported(DifficultyHard, fmt.Sprintf("function %q with subquery arguments is not implemented yet", name))
				}
			}
		}
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
	case "histogram_quantile":
		return AnalyzeHistogramQuantileCall(call)
	case "histogram_fraction":
		return AnalyzeHistogramFractionCall(call)
	case "histogram_count", "histogram_sum", "histogram_avg", "histogram_stddev", "histogram_stdvar":
		return AnalyzeHistogramProjectionCall(name, call)
	case "histogram_quantiles":
		return AnalyzeHistogramQuantilesCall(call)
	case "last_over_time", "first_over_time", "sum_over_time", "avg_over_time", "max_over_time", "min_over_time", "count_over_time", "stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "resets", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		return AnalyzeRangeFunctionCall(name, call)
	case "predict_linear":
		return AnalyzePredictLinearCall(call)
	case "quantile_over_time":
		return AnalyzeQuantileOverTimeCall(call)
	case "absent":
		return AnalyzeAbsentCall(call)
	case "absent_over_time":
		return AnalyzeAbsentOverTimeCall(call)
	default:
		return unsupported(DifficultyMedium, fmt.Sprintf("function %q is not implemented yet", name))
	}
}

func AnalyzeSubqueryExpression(expr *parser.SubqueryExpr) SupportResult {
	if expr == nil || expr.Expr == nil {
		return unsupported(DifficultyHard, "subquery expression is invalid")
	}
	if expr.Expr.Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, fmt.Sprintf("subquery inner expression must evaluate to instant vector, got %q", expr.Expr.Type()))
	}
	child := AnalyzeExpression(expr.Expr)
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeDelegatableSubqueryExpression(expr *parser.SubqueryExpr) SupportResult {
	if expr == nil || expr.Expr == nil {
		return unsupported(DifficultyHard, "subquery expression is invalid")
	}
	if expr.Expr.Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, fmt.Sprintf("subquery inner expression must evaluate to instant vector, got %q", expr.Expr.Type()))
	}
	child := AnalyzeDelegatableExpression(expr.Expr)
	if !child.Supported {
		return child
	}
	if child.Difficulty != DifficultyEasy {
		return unsupported(DifficultyHard, "delegated subquery inner expression currently requires delegated leaf-compatible semantics")
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeAggregateExpression(expr *parser.AggregateExpr) SupportResult {
	op := strings.ToLower(expr.Op.String())
	if !isSupportedLocalAggregation(expr.Op) {
		return unsupported(DifficultyMedium, fmt.Sprintf("aggregation operator %q is not implemented yet", op))
	}
	if result := analyzeAggregationParameter(expr); !result.Supported {
		return result
	}
	child := AnalyzeDelegatableExpression(expr.Expr)
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func analyzeAggregationParameter(expr *parser.AggregateExpr) SupportResult {
	switch expr.Op {
	case parser.TOPK, parser.BOTTOMK, parser.QUANTILE, parser.LIMITK, parser.LIMIT_RATIO:
		if expr.Param == nil {
			return unsupported(DifficultyMedium, fmt.Sprintf("aggregation operator %q requires a scalar parameter", strings.ToLower(expr.Op.String())))
		}
		switch unwrapTransparentExpr(expr.Param).(type) {
		case *parser.NumberLiteral:
			return SupportResult{Supported: true, Difficulty: DifficultyMedium}
		default:
			return unsupported(DifficultyMedium, fmt.Sprintf("aggregation operator %q currently requires a literal scalar parameter", strings.ToLower(expr.Op.String())))
		}
	case parser.COUNT_VALUES:
		if expr.Param == nil {
			return unsupported(DifficultyMedium, "count_values requires a string label parameter")
		}
		switch unwrapTransparentExpr(expr.Param).(type) {
		case *parser.StringLiteral:
			return SupportResult{Supported: true, Difficulty: DifficultyMedium}
		default:
			return unsupported(DifficultyMedium, "count_values currently requires a literal string label parameter")
		}
	default:
		if expr.Param != nil {
			return unsupported(DifficultyMedium, "aggregation parameters are not implemented for this operator")
		}
		return SupportResult{Supported: true, Difficulty: DifficultyMedium}
	}
}

func isSupportedLocalAggregation(op parser.ItemType) bool {
	switch op {
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG, parser.TOPK, parser.BOTTOMK, parser.COUNT_VALUES,
		parser.STDDEV, parser.STDVAR, parser.QUANTILE, parser.GROUP, parser.LIMITK, parser.LIMIT_RATIO:
		return true
	default:
		return false
	}
}

func AnalyzeBinaryExpression(expr *parser.BinaryExpr) SupportResult {
	lhsType, rhsType := expr.LHS.Type(), expr.RHS.Type()
	if isSetOperator(expr.Op.String()) {
		if lhsType != parser.ValueTypeVector || rhsType != parser.ValueTypeVector {
			return unsupported(DifficultyHard, "set operators currently require instant-vector operands")
		}
		if expr.VectorMatching != nil && expr.VectorMatching.Card != parser.CardManyToMany {
			return unsupported(DifficultyHard, "set operators require many-to-many vector matching")
		}
		lhs := AnalyzeExpression(expr.LHS)
		if !lhs.Supported {
			return lhs
		}
		rhs := AnalyzeExpression(expr.RHS)
		if !rhs.Supported {
			return rhs
		}
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	if !isSupportedLocalBinaryOperator(expr.Op) {
		return unsupported(DifficultyMedium, fmt.Sprintf("binary operator %q is not implemented yet", expr.Op.String()))
	}
	difficulty := DifficultyMedium
	supportedShape := (lhsType == parser.ValueTypeScalar && rhsType == parser.ValueTypeScalar) ||
		(lhsType == parser.ValueTypeVector && rhsType == parser.ValueTypeScalar) ||
		(lhsType == parser.ValueTypeScalar && rhsType == parser.ValueTypeVector)
	if lhsType == parser.ValueTypeVector && rhsType == parser.ValueTypeVector {
		supportedShape = true
		difficulty = DifficultyHard
		if expr.VectorMatching != nil && expr.VectorMatching.Card == parser.CardManyToMany {
			return unsupported(DifficultyHard, "many-to-many matching is only allowed for set operators")
		}
	}
	if !supportedShape {
		return unsupported(DifficultyMedium, "only scalar-scalar, vector-scalar, and vector-vector binary expressions are implemented yet")
	}

	lhs := AnalyzeExpression(expr.LHS)
	if !lhs.Supported {
		return lhs
	}
	rhs := AnalyzeExpression(expr.RHS)
	if !rhs.Supported {
		return rhs
	}
	return SupportResult{Supported: true, Difficulty: difficulty}
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

func isSupportedLeafFunction(name string) bool {
	switch name {
	case "rate", "irate", "increase", "delta", "idelta", "deriv", "changes", "resets",
		"abs", "ceil", "floor", "sgn",
		"exp", "ln", "log2", "log10", "sqrt",
		"sin", "cos", "tan", "asin", "acos", "atan",
		"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
		"deg", "rad", "pi", "time", "timestamp",
		"minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month",
		"month", "year":
		return true
	default:
		return false
	}
}

func isSubqueryUnsupportedFunction(name string) bool {
	switch name {
	case "increase", "delta", "idelta", "deriv", "changes", "resets":
		return true
	default:
		return false
	}
}

func containsSubqueryExpr(expr parser.Expr) bool {
	expr = unwrapTransparentExpr(expr)
	switch node := expr.(type) {
	case *parser.SubqueryExpr:
		return true
	case *parser.Call:
		for _, arg := range node.Args {
			if containsSubqueryExpr(arg) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		if node.Param != nil && containsSubqueryExpr(node.Param) {
			return true
		}
		return containsSubqueryExpr(node.Expr)
	case *parser.BinaryExpr:
		return containsSubqueryExpr(node.LHS) || containsSubqueryExpr(node.RHS)
	case *parser.UnaryExpr:
		return containsSubqueryExpr(node.Expr)
	case *parser.ParenExpr:
		return containsSubqueryExpr(node.Expr)
	case *parser.StepInvariantExpr:
		return containsSubqueryExpr(node.Expr)
	case *parser.MatrixSelector:
		return containsSubqueryExpr(node.VectorSelector)
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
