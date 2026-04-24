package logical

import (
	"fmt"

	"github.com/prometheus/prometheus/promql/parser"
)

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

func AnalyzeHistogramQuantilesCall(call *parser.Call) SupportResult {
	if len(call.Args) < 3 {
		return unsupported(DifficultyHard, "histogram_quantiles requires at least three arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, "histogram_quantiles requires a vector histogram argument")
	}
	if call.Args[1].Type() != parser.ValueTypeString {
		return unsupported(DifficultyHard, "histogram_quantiles requires a string destination label argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	for _, arg := range call.Args[2:] {
		if arg.Type() != parser.ValueTypeScalar {
			return unsupported(DifficultyHard, "histogram_quantiles requires scalar quantile arguments")
		}
		result := AnalyzeExpression(arg)
		if !result.Supported {
			return result
		}
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

func AnalyzeScalarCall(call *parser.Call) SupportResult {
	if len(call.Args) != 1 {
		return unsupported(DifficultyMedium, "scalar requires one argument")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, "scalar requires a vector argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	return SupportResult{Supported: true, Difficulty: DifficultyMedium}
}

func AnalyzeInfoCall(call *parser.Call) SupportResult {
	if len(call.Args) < 1 || len(call.Args) > 2 {
		return unsupported(DifficultyMedium, "info requires one or two arguments")
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, "info requires a vector first argument")
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	if len(call.Args) == 2 {
		selector, ok := unwrapTransparentExpr(call.Args[1]).(*parser.VectorSelector)
		if !ok {
			return unsupported(DifficultyHard, "info currently requires a label-selector second argument")
		}
		if selector.Name != "" {
			return unsupported(DifficultyHard, "info currently requires selector matchers without a metric prefix")
		}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
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

func AnalyzeClampCall(name string, call *parser.Call) SupportResult {
	expectedArgs := 3
	if name != "clamp" {
		expectedArgs = 2
	}
	if len(call.Args) != expectedArgs {
		return unsupported(DifficultyMedium, fmt.Sprintf("%s requires %d arguments", name, expectedArgs))
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, fmt.Sprintf("%s requires a vector first argument", name))
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	for _, arg := range call.Args[1:] {
		if arg.Type() != parser.ValueTypeScalar {
			return unsupported(DifficultyHard, fmt.Sprintf("%s requires scalar bound arguments", name))
		}
		child := AnalyzeExpression(arg)
		if !child.Supported {
			return child
		}
	}
	return SupportResult{Supported: true, Difficulty: DifficultyHard}
}

func AnalyzeSortCall(name string, call *parser.Call) SupportResult {
	if len(call.Args) < 1 {
		return unsupported(DifficultyMedium, fmt.Sprintf("%s requires at least one argument", name))
	}
	if call.Args[0].Type() != parser.ValueTypeVector {
		return unsupported(DifficultyHard, fmt.Sprintf("%s requires a vector argument", name))
	}
	child := AnalyzeExpression(call.Args[0])
	if !child.Supported {
		return child
	}
	switch name {
	case "sort", "sort_desc":
		if len(call.Args) != 1 {
			return unsupported(DifficultyMedium, fmt.Sprintf("%s requires exactly one argument", name))
		}
	case "sort_by_label", "sort_by_label_desc":
		if len(call.Args) < 2 {
			return unsupported(DifficultyMedium, fmt.Sprintf("%s requires at least one label argument", name))
		}
		for _, arg := range call.Args[1:] {
			if arg.Type() != parser.ValueTypeString {
				return unsupported(DifficultyHard, fmt.Sprintf("%s requires string label arguments", name))
			}
		}
	default:
		return unsupported(DifficultyMedium, fmt.Sprintf("sort function %q is not implemented yet", name))
	}
	return SupportResult{Supported: true, Difficulty: DifficultyEasy}
}
