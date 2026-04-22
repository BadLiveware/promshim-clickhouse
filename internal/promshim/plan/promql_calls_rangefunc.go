package plan

import (
	"fmt"

	"github.com/prometheus/prometheus/promql/parser"
)

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

func AnalyzePredictLinearCall(call *parser.Call) SupportResult {
	if len(call.Args) != 2 {
		return unsupported(DifficultyHard, "predict_linear requires two arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "predict_linear requires a matrix argument")
	}
	if call.Args[1].Type() != parser.ValueTypeScalar {
		return unsupported(DifficultyHard, "predict_linear requires a scalar duration argument")
	}
	if _, ok := unwrapTransparentExpr(call.Args[1]).(*parser.NumberLiteral); !ok {
		return unsupported(DifficultyHard, "predict_linear currently requires a literal scalar duration argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if containsSubqueryExpr(call.Args[0]) {
		return SupportResult{Supported: true, Difficulty: DifficultyHard}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeDoubleExponentialSmoothingCall(call *parser.Call) SupportResult {
	if len(call.Args) != 3 {
		return unsupported(DifficultyHard, "double_exponential_smoothing requires three arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeMatrix {
		return unsupported(DifficultyHard, "double_exponential_smoothing requires a matrix argument")
	}
	if call.Args[1].Type() != parser.ValueTypeScalar || call.Args[2].Type() != parser.ValueTypeScalar {
		return unsupported(DifficultyHard, "double_exponential_smoothing requires scalar smoothing and trend factors")
	}
	sfExpr := unwrapTransparentExpr(call.Args[1])
	tfExpr := unwrapTransparentExpr(call.Args[2])
	sf, ok := sfExpr.(*parser.NumberLiteral)
	if !ok {
		return unsupported(DifficultyHard, "double_exponential_smoothing currently requires a literal scalar smoothing factor")
	}
	tf, ok := tfExpr.(*parser.NumberLiteral)
	if !ok {
		return unsupported(DifficultyHard, "double_exponential_smoothing currently requires a literal scalar trend factor")
	}
	if sf.Val <= 0 || sf.Val >= 1 {
		return unsupported(DifficultyHard, "double_exponential_smoothing smoothing factor must be between 0 and 1 exclusive")
	}
	if tf.Val <= 0 || tf.Val >= 1 {
		return unsupported(DifficultyHard, "double_exponential_smoothing trend factor must be between 0 and 1 exclusive")
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
