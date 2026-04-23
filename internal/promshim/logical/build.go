package logical

import (
	"strings"

	"ch-observability/internal/promshim/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// ToLogical converts a Prometheus expression to the promshim logical IR.
// It is the canonical entry point for parser-expr-to-logical-IR translation.
// Returns an error if expr is nil or if the expression contains constructs
// that cannot be represented in the logical IR.
func ToLogical(expr parser.Expr) (Node, error) {
	if expr == nil {
		return nil, NewUnsupportedErrorf("cannot build logical plan for nil expression")
	}
	return buildLogicalPlan(expr)
}

func buildLogicalPlan(expr parser.Expr) (Node, error) {
	expr = UnwrapTransparentExpr(expr)

	switch node := expr.(type) {
	case *parser.NumberLiteral:
		return &ScalarLiteralPlan{Expr: node, Value: node.Val}, nil
	case *parser.Call:
		return buildLogicalCallPlan(node)
	case *parser.UnaryExpr:
		result := AnalyzeUnaryExpression(node)
		if !result.Supported {
			return nil, NewBuildError(node, result, "unary planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for unary expression %q", node.String())
		}
		return &UnaryPlan{Expr: node, Op: node.Op, Child: child}, nil
	case *parser.BinaryExpr:
		result := AnalyzeBinaryExpression(node)
		if !result.Supported {
			return nil, NewBuildError(node, result, "binary planning")
		}
		lhs, err := buildLogicalPlan(node.LHS)
		if err != nil {
			return nil, withBuildContext(err, "building logical left operand plan for binary expression %q", node.String())
		}
		rhs, err := buildLogicalPlan(node.RHS)
		if err != nil {
			return nil, withBuildContext(err, "building logical right operand plan for binary expression %q", node.String())
		}
		return &BinaryPlan{Expr: node, Op: node.Op, VectorMatching: cloneVectorMatching(node.VectorMatching), ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, nil
	case *parser.SubqueryExpr:
		result := AnalyzeSubqueryExpression(node)
		if !result.Supported {
			return nil, NewBuildError(node, result, "subquery planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for subquery %q", node.String())
		}
		delegatable := AnalyzeDelegatableSubqueryExpression(node).Supported
		return &SubqueryPlan{Expr: node, Range: node.Range, Step: node.Step, Offset: node.OriginalOffset, Timestamp: node.Timestamp, StartOrEnd: node.StartOrEnd, DelegatedLeafCompatible: delegatable, Child: child}, nil
	case *parser.AggregateExpr:
		result := AnalyzeAggregateExpression(node)
		if !result.Supported {
			return nil, NewBuildError(node, result, "aggregate planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for aggregate %q", node.String())
		}
		paramNumber, paramString, err := aggregatePlanParam(node)
		if err != nil {
			return nil, withBuildContext(err, "building logical aggregation parameter for %q", node.String())
		}
		return &AggregationPlan{Expr: node, Op: node.Op, Grouping: append([]string(nil), node.Grouping...), Without: node.Without, ParamNumber: paramNumber, ParamString: paramString, Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(expr)
	}
}

func buildLogicalDelegatedLeaf(expr parser.Expr) (Node, error) {
	expr = UnwrapTransparentExpr(expr)
	result := AnalyzeDelegatableExpression(expr)
	if !result.Supported {
		return nil, NewBuildError(expr, result, "delegated leaf planning")
	}
	return &LeafExprPlan{Expr: expr}, nil
}

func buildLogicalCallPlan(call *parser.Call) (Node, error) {
	name := strings.ToLower(call.Func.Name)
	switch name {
	case "label_replace":
		if result := AnalyzeLabelReplaceCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for label_replace %q", call.String())
		}
		dst, err := stringLiteralArgument(call.Args[1], "label_replace destination label")
		if err != nil {
			return nil, withBuildContext(err, "building logical label_replace %q", call.String())
		}
		repl, err := stringLiteralArgument(call.Args[2], "label_replace replacement")
		if err != nil {
			return nil, withBuildContext(err, "building logical label_replace %q", call.String())
		}
		src, err := stringLiteralArgument(call.Args[3], "label_replace source label")
		if err != nil {
			return nil, withBuildContext(err, "building logical label_replace %q", call.String())
		}
		regexStr, err := stringLiteralArgument(call.Args[4], "label_replace regex")
		if err != nil {
			return nil, withBuildContext(err, "building logical label_replace %q", call.String())
		}
		cfg, err := model.BuildLabelReplaceConfig(dst, repl, src, regexStr)
		if err != nil {
			return nil, withBuildContext(NewBadDataErrorf("%s", err.Error()), "building logical label_replace %q", call.String())
		}
		return &LabelReplacePlan{Expr: call, Config: cfg, Child: child}, nil
	case "label_join":
		if result := AnalyzeLabelJoinCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for label_join %q", call.String())
		}
		dst, err := stringLiteralArgument(call.Args[1], "label_join destination label")
		if err != nil {
			return nil, withBuildContext(err, "building logical label_join %q", call.String())
		}
		sep, err := stringLiteralArgument(call.Args[2], "label_join separator")
		if err != nil {
			return nil, withBuildContext(err, "building logical label_join %q", call.String())
		}
		srcLabels := make([]string, 0, len(call.Args)-3)
		for _, arg := range call.Args[3:] {
			src, err := stringLiteralArgument(arg, "label_join source label")
			if err != nil {
				return nil, withBuildContext(err, "building logical label_join %q", call.String())
			}
			srcLabels = append(srcLabels, src)
		}
		cfg, err := model.BuildLabelJoinConfig(dst, sep, srcLabels)
		if err != nil {
			return nil, withBuildContext(NewBadDataErrorf("%s", err.Error()), "building logical label_join %q", call.String())
		}
		return &LabelJoinPlan{Expr: call, Config: cfg, Child: child}, nil
	case "histogram_quantile":
		if result := AnalyzeHistogramQuantileCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		quantile, err := numberLiteralArgument(call.Args[0], "histogram_quantile quantile")
		if err != nil {
			return nil, withBuildContext(err, "building logical histogram_quantile %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[1])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for histogram_quantile %q", call.String())
		}
		return &HistogramQuantilePlan{Expr: call, Quantile: quantile, Child: child}, nil
	case "histogram_fraction":
		if result := AnalyzeHistogramFractionCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		lower, err := numberLiteralArgument(call.Args[0], "histogram_fraction lower bound")
		if err != nil {
			return nil, withBuildContext(err, "building logical histogram_fraction %q", call.String())
		}
		upper, err := numberLiteralArgument(call.Args[1], "histogram_fraction upper bound")
		if err != nil {
			return nil, withBuildContext(err, "building logical histogram_fraction %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[2])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for histogram_fraction %q", call.String())
		}
		return &HistogramFractionPlan{Expr: call, Lower: lower, Upper: upper, Child: child}, nil
	case "histogram_count", "histogram_sum", "histogram_avg", "histogram_stddev", "histogram_stdvar":
		if result := AnalyzeHistogramProjectionCall(name, call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &HistogramProjectionPlan{Expr: call, Func: name, Child: child}, nil
	case "histogram_quantiles":
		if result := AnalyzeHistogramQuantilesCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		label, err := stringLiteralArgument(call.Args[1], "histogram_quantiles label")
		if err != nil {
			return nil, withBuildContext(err, "building logical histogram_quantiles %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for histogram_quantiles %q", call.String())
		}
		paramNumbers := make([]*float64, 0, len(call.Args)-2)
		paramChildren := make([]Node, 0, len(call.Args)-2)
		for _, arg := range call.Args[2:] {
			builtArg, err := buildLogicalPlan(arg)
			if err != nil {
				return nil, withBuildContext(err, "building logical histogram_quantiles scalar parameter for %q", call.String())
			}
			paramChildren = append(paramChildren, builtArg)
			if literal, ok := UnwrapTransparentExpr(arg).(*parser.NumberLiteral); ok {
				valueCopy := literal.Val
				paramNumbers = append(paramNumbers, &valueCopy)
			} else {
				paramNumbers = append(paramNumbers, nil)
			}
		}
		return &HistogramQuantilesPlan{Expr: call, Label: label, ParamNumbers: paramNumbers, ParamChildren: paramChildren, Child: child}, nil
	case "vector":
		if result := AnalyzeVectorCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for vector %q", call.String())
		}
		return &VectorPlan{Expr: call, Child: child}, nil
	case "pi", "time":
		return &ScalarBuiltinPlan{Expr: call, Func: name}, nil
	case "info":
		if result := AnalyzeInfoCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for info %q", call.String())
		}
		var selectorMatchers []*promlabels.Matcher
		if len(call.Args) > 1 {
			selector, ok := UnwrapTransparentExpr(call.Args[1]).(*parser.VectorSelector)
			if !ok {
				return nil, withBuildContext(NewBadDataErrorf("info selector must be a label selector"), "building logical info %q", call.String())
			}
			selectorMatchers = clonePromMatchers(selector.LabelMatchers)
		}
		return &InfoPlan{Expr: call, SelectorMatchers: selectorMatchers, Child: child}, nil
	case "scalar":
		if result := AnalyzeScalarCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for scalar %q", call.String())
		}
		return &ScalarConvertPlan{Expr: call, Child: child}, nil
	case "round":
		if result := AnalyzeRoundCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for round %q", call.String())
		}
		plan := &RoundPlan{Expr: call, Child: child}
		if len(call.Args) > 1 {
			decimals, err := numberLiteralArgument(call.Args[1], "round decimals")
			if err != nil {
				return nil, withBuildContext(err, "building logical round %q", call.String())
			}
			plan.Decimals = &decimals
		}
		return plan, nil
	case "sort", "sort_desc", "sort_by_label", "sort_by_label_desc":
		if result := AnalyzeSortCall(name, call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		labels := make([]string, 0, max(0, len(call.Args)-1))
		for _, arg := range call.Args[1:] {
			label, err := stringLiteralArgument(arg, name+" label")
			if err != nil {
				return nil, withBuildContext(err, "building logical %s %q", name, call.String())
			}
			labels = append(labels, label)
		}
		return &SortPlan{Expr: call, Func: name, Labels: labels, Child: child}, nil
	case "rate", "irate":
		analyze := AnalyzeRateCall
		if name == "irate" {
			analyze = AnalyzeIrateCall
		}
		if result := analyze(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &RatePlan{Expr: call, Func: name, Child: child}, nil
	case "increase":
		if result := AnalyzeIncreaseCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for increase %q", call.String())
		}
		return &IncreasePlan{Expr: call, Child: child}, nil
	case "delta", "idelta":
		analyze := AnalyzeDeltaCall
		if name == "idelta" {
			analyze = AnalyzeIDeltaCall
		}
		if result := analyze(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &DeltaPlan{Expr: call, Func: name, Child: child}, nil
	case "changes":
		if result := AnalyzeChangesCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for changes %q", call.String())
		}
		return &ChangesPlan{Expr: call, Child: child}, nil
	case "deriv":
		if result := AnalyzeDerivCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for deriv %q", call.String())
		}
		return &DerivPlan{Expr: call, Child: child}, nil
	case "last_over_time", "first_over_time", "sum_over_time", "avg_over_time", "max_over_time", "min_over_time", "count_over_time", "stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "resets", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		if result := AnalyzeRangeFunctionCall(name, call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &RangeFunctionPlan{Expr: call, Func: name, Child: child}, nil
	case "predict_linear":
		if result := AnalyzePredictLinearCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		duration, err := numberLiteralArgument(call.Args[1], "predict_linear duration")
		if err != nil {
			return nil, withBuildContext(err, "building logical predict_linear %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for predict_linear %q", call.String())
		}
		return &RangeFunctionPlan{Expr: call, Func: name, ParamNumber: cloneFloat64(duration), Child: child}, nil
	case "double_exponential_smoothing", "holt_winters":
		if result := AnalyzeDoubleExponentialSmoothingCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		sf, err := numberLiteralArgument(call.Args[1], name+" smoothing factor")
		if err != nil {
			return nil, withBuildContext(err, "building logical %s %q", name, call.String())
		}
		tf, err := numberLiteralArgument(call.Args[2], name+" trend factor")
		if err != nil {
			return nil, withBuildContext(err, "building logical %s %q", name, call.String())
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &RangeFunctionPlan{Expr: call, Func: name, ParamNumbers: []*float64{cloneFloat64(sf), cloneFloat64(tf)}, Child: child}, nil
	case "abs", "ceil", "floor", "sgn",
		"exp", "ln", "log2", "log10", "sqrt",
		"clamp", "clamp_min", "clamp_max",
		"sin", "cos", "tan", "asin", "acos", "atan",
		"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
		"deg", "rad", "timestamp",
		"minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return buildLogicalPointwiseFunctionPlan(name, call)
	case "quantile_over_time":
		if result := AnalyzeQuantileOverTimeCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		quantile, err := numberLiteralArgument(call.Args[0], "quantile_over_time quantile")
		if err != nil {
			return nil, withBuildContext(err, "building logical quantile_over_time %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[1])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for quantile_over_time %q", call.String())
		}
		return &QuantileOverTimePlan{Expr: call, Quantile: quantile, Child: child}, nil
	case "absent":
		if result := AnalyzeAbsentCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for absent %q", call.String())
		}
		return &AbsentPlan{Expr: call, OutputMetric: deriveAbsentOutputMetric(call.Args[0]), Child: child}, nil
	case "absent_over_time":
		if result := AnalyzeAbsentOverTimeCall(call); !result.Supported {
			return nil, NewBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withBuildContext(err, "building logical child plan for absent_over_time %q", call.String())
		}
		return &AbsentOverTimePlan{Expr: call, OutputMetric: deriveAbsentOutputMetric(call.Args[0]), Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(call)
	}
}
