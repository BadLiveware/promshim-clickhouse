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
	p := parser.NewParser(parser.Options{EnableBinopFillModifiers: true})
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
	if name == "round" {
		return AnalyzeRoundCall(call)
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
	case "histogram_count", "histogram_sum", "histogram_avg":
		return AnalyzeHistogramProjectionCall(name, call)
	case "last_over_time", "sum_over_time", "avg_over_time", "max_over_time", "min_over_time", "count_over_time":
		return AnalyzeRangeFunctionCall(name, call)
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

func AnalyzeQuantileOverTimeCall(call *parser.Call) SupportResult {
	if len(call.Args) != 2 {
		return unsupported(DifficultyHard, "quantile_over_time requires two arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeScalar {
		return unsupported(DifficultyHard, "quantile_over_time requires a scalar quantile argument")
	}
	if _, ok := unwrapTransparentExpr(call.Args[0]).(*parser.NumberLiteral); !ok {
		return unsupported(DifficultyHard, "quantile_over_time currently requires a literal scalar quantile argument")
	}
	if call.Args[1].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "quantile_over_time requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[1])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
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

func AnalyzeHistogramQuantileCall(call *parser.Call) SupportResult {
	if len(call.Args) != 2 {
		return unsupported(DifficultyHard, "histogram_quantile requires two arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeScalar {
		return unsupported(DifficultyHard, "histogram_quantile requires a scalar quantile argument")
	}
	if _, ok := unwrapTransparentExpr(call.Args[0]).(*parser.NumberLiteral); !ok {
		return unsupported(DifficultyHard, "histogram_quantile currently requires a literal scalar quantile argument")
	}
	if call.Args[1].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, "histogram_quantile requires a vector histogram argument")
	}
	child := AnalyzeExpression(call.Args[1])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeHistogramProjectionCall(name string, call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, fmt.Sprintf("%s requires one argument", name))
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, fmt.Sprintf("%s requires a vector histogram argument", name))
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeHistogramFractionCall(call *parser.Call) SupportResult {
	if len(call.Args) != 3 {
		return unsupported(DifficultyHard, "histogram_fraction requires three arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeScalar || call.Args[1].Type() != parser.ValueTypeScalar {
		return unsupported(DifficultyHard, "histogram_fraction requires scalar lower and upper bound arguments")
	}
	if _, ok := unwrapTransparentExpr(call.Args[0]).(*parser.NumberLiteral); !ok {
		return unsupported(DifficultyHard, "histogram_fraction currently requires a literal scalar lower bound")
	}
	if _, ok := unwrapTransparentExpr(call.Args[1]).(*parser.NumberLiteral); !ok {
		return unsupported(DifficultyHard, "histogram_fraction currently requires a literal scalar upper bound")
	}
	if call.Args[2].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, "histogram_fraction requires a vector histogram argument")
	}
	child := AnalyzeExpression(call.Args[2])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeRangeFunctionCall(name string, call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, fmt.Sprintf("%s requires one argument", name))
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, fmt.Sprintf("%s requires a matrix argument", name))
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeIncreaseCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "increase requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "increase requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if containsSubqueryExpr(call.Args[0]) {
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeRateCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "rate requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "rate requires a matrix argument")
	}
	if containsSubqueryExpr(call.Args[0]) {
		child := AnalyzeExpression(call.Args[0])
		if !child.Supported {
			return child
		}
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeDeltaCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "delta requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "delta requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if containsSubqueryExpr(call.Args[0]) {
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeIDeltaCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "idelta requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "idelta requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if containsSubqueryExpr(call.Args[0]) {
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeChangesCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "changes requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "changes requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if containsSubqueryExpr(call.Args[0]) {
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeDerivCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "deriv requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "deriv requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if containsSubqueryExpr(call.Args[0]) {
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeIrateCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "irate requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "irate requires a matrix argument")
	}
	if containsSubqueryExpr(call.Args[0]) {
		child := AnalyzeExpression(call.Args[0])
		if !child.Supported {
			return child
		}
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}

func AnalyzeVectorCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyMedium, "vector requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeScalar {
		return unsupported(DifficultyHard, "vector requires a scalar argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeRoundCall(call *parser.Call) SupportResult {
	if len(call.Args) < 1 || len(call.Args) > 2 {
		return unsupported(DifficultyMedium, "round requires one or two arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, "round requires a vector argument")
	}
	if len(call.Args) > 1 {
		n := call.Args[1]
		if n.Type() != parser.ValueTypeScalar {
			return unsupported(DifficultyHard, "round requires scalar second argument")
		}
		child := AnalyzeExpression(n)
		if !child.Supported {
			return child
		}
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeAbsentCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyMedium, "absent requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyMedium, "absent requires a vector argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeAbsentOverTimeCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyHard, "absent_over_time requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "absent_over_time requires a matrix argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeLastOverTimeCall(call *parser.Call) SupportResult {
	return AnalyzeRangeFunctionCall("last_over_time", call)
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
	case parser.TOPK, parser.BOTTOMK:
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
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG, parser.TOPK, parser.BOTTOMK, parser.COUNT_VALUES:
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
	case "rate", "irate", "increase", "delta", "idelta", "deriv", "changes":
		return true
	default:
		return false
	}
}

func isSubqueryUnsupportedFunction(name string) bool {
	switch name {
	case "increase", "delta", "idelta", "deriv", "changes":
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
