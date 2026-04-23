package local

import (
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type logicalPlan = logical.Node

type logicalLeafExprPlan = logical.LeafExprPlan

type logicalScalarLiteralPlan = logical.ScalarLiteralPlan

type logicalUnaryPlan = logical.UnaryPlan

type logicalBinaryPlan = logical.BinaryPlan

type logicalAggregationPlan = logical.AggregationPlan

type logicalHistogramQuantilePlan = logical.HistogramQuantilePlan

type logicalHistogramFractionPlan = logical.HistogramFractionPlan

type logicalHistogramProjectionPlan = logical.HistogramProjectionPlan

type logicalHistogramQuantilesPlan = logical.HistogramQuantilesPlan

type logicalRangeFunctionPlan = logical.RangeFunctionPlan

type logicalVectorPlan = logical.VectorPlan

type logicalRoundPlan = logical.RoundPlan

type logicalSortPlan = logical.SortPlan

type logicalScalarConvertPlan = logical.ScalarConvertPlan

type logicalInfoPlan = logical.InfoPlan

type logicalPointwiseFunctionPlan = logical.PointwiseFunctionPlan

type logicalScalarBuiltinPlan = logical.ScalarBuiltinPlan

type logicalRatePlan = logical.RatePlan

type logicalIncreasePlan = logical.IncreasePlan

type logicalDeltaPlan = logical.DeltaPlan

type logicalChangesPlan = logical.ChangesPlan

type logicalDerivPlan = logical.DerivPlan

type logicalQuantileOverTimePlan = logical.QuantileOverTimePlan

type logicalAbsentPlan = logical.AbsentPlan

type logicalAbsentOverTimePlan = logical.AbsentOverTimePlan

type logicalSubqueryPlan = logical.SubqueryPlan

type logicalLabelReplacePlan = logical.LabelReplacePlan

type logicalLabelJoinPlan = logical.LabelJoinPlan

func BuildLogicalPlan(expr parser.Expr) (logicalPlan, error) {
	expr = unwrapTransparentExpr(expr)

	switch node := expr.(type) {
	case *parser.NumberLiteral:
		return &logicalScalarLiteralPlan{Expr: node, Value: node.Val}, nil
	case *parser.Call:
		return buildLogicalCallPlan(node)
	case *parser.UnaryExpr:
		result := logical.AnalyzeUnaryExpression(node)
		if !result.Supported {
			return nil, NewPlanBuildError(node, result, "unary planning")
		}
		child, err := BuildLogicalPlan(node.Expr)
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for unary expression %q", node.String())
		}
		return &logicalUnaryPlan{Expr: node, Op: node.Op, Child: child}, nil
	case *parser.BinaryExpr:
		result := logical.AnalyzeBinaryExpression(node)
		if !result.Supported {
			return nil, NewPlanBuildError(node, result, "binary planning")
		}
		lhs, err := BuildLogicalPlan(node.LHS)
		if err != nil {
			return nil, WithInternalContext(err, "building logical left operand plan for binary expression %q", node.String())
		}
		rhs, err := BuildLogicalPlan(node.RHS)
		if err != nil {
			return nil, WithInternalContext(err, "building logical right operand plan for binary expression %q", node.String())
		}
		return &logicalBinaryPlan{Expr: node, Op: node.Op, VectorMatching: cloneVectorMatching(node.VectorMatching), ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, nil
	case *parser.SubqueryExpr:
		result := logical.AnalyzeSubqueryExpression(node)
		if !result.Supported {
			return nil, NewPlanBuildError(node, result, "subquery planning")
		}
		child, err := BuildLogicalPlan(node.Expr)
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for subquery %q", node.String())
		}
		delegatable := logical.AnalyzeDelegatableSubqueryExpression(node).Supported
		return &logicalSubqueryPlan{Expr: node, Range: node.Range, Step: node.Step, Offset: node.OriginalOffset, Timestamp: node.Timestamp, StartOrEnd: node.StartOrEnd, DelegatedLeafCompatible: delegatable, Child: child}, nil
	case *parser.AggregateExpr:
		result := logical.AnalyzeAggregateExpression(node)
		if !result.Supported {
			return nil, NewPlanBuildError(node, result, "aggregate planning")
		}
		child, err := BuildLogicalPlan(node.Expr)
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for aggregate %q", node.String())
		}
		paramNumber, paramString, err := aggregatePlanParam(node)
		if err != nil {
			return nil, WithInternalContext(err, "building logical aggregation parameter for %q", node.String())
		}
		return &logicalAggregationPlan{Expr: node, Op: node.Op, Grouping: append([]string(nil), node.Grouping...), Without: node.Without, ParamNumber: paramNumber, ParamString: paramString, Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(expr)
	}
}

func buildLogicalDelegatedLeaf(expr parser.Expr) (logicalPlan, error) {
	expr = unwrapTransparentExpr(expr)
	result := logical.AnalyzeDelegatableExpression(expr)
	if !result.Supported {
		return nil, NewPlanBuildError(expr, result, "delegated leaf planning")
	}
	return &logicalLeafExprPlan{Expr: expr}, nil
}

func buildLogicalCallPlan(call *parser.Call) (logicalPlan, error) {
	name := strings.ToLower(call.Func.Name)
	switch name {
	case "label_replace":
		if result := logical.AnalyzeLabelReplaceCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for label_replace %q", call.String())
		}
		dst, err := stringLiteralArgument(call.Args[1], "label_replace destination label")
		if err != nil {
			return nil, WithInternalContext(err, "building logical label_replace %q", call.String())
		}
		repl, err := stringLiteralArgument(call.Args[2], "label_replace replacement")
		if err != nil {
			return nil, WithInternalContext(err, "building logical label_replace %q", call.String())
		}
		src, err := stringLiteralArgument(call.Args[3], "label_replace source label")
		if err != nil {
			return nil, WithInternalContext(err, "building logical label_replace %q", call.String())
		}
		regexStr, err := stringLiteralArgument(call.Args[4], "label_replace regex")
		if err != nil {
			return nil, WithInternalContext(err, "building logical label_replace %q", call.String())
		}
		cfg, err := model.BuildLabelReplaceConfig(dst, repl, src, regexStr)
		if err != nil {
			return nil, WithInternalContext(NewBadDataErrorf("%s", err.Error()), "building logical label_replace %q", call.String())
		}
		return &logicalLabelReplacePlan{Expr: call, Config: cfg, Child: child}, nil
	case "label_join":
		if result := logical.AnalyzeLabelJoinCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for label_join %q", call.String())
		}
		dst, err := stringLiteralArgument(call.Args[1], "label_join destination label")
		if err != nil {
			return nil, WithInternalContext(err, "building logical label_join %q", call.String())
		}
		sep, err := stringLiteralArgument(call.Args[2], "label_join separator")
		if err != nil {
			return nil, WithInternalContext(err, "building logical label_join %q", call.String())
		}
		srcLabels := make([]string, 0, len(call.Args)-3)
		for _, arg := range call.Args[3:] {
			src, err := stringLiteralArgument(arg, "label_join source label")
			if err != nil {
				return nil, WithInternalContext(err, "building logical label_join %q", call.String())
			}
			srcLabels = append(srcLabels, src)
		}
		cfg, err := model.BuildLabelJoinConfig(dst, sep, srcLabels)
		if err != nil {
			return nil, WithInternalContext(NewBadDataErrorf("%s", err.Error()), "building logical label_join %q", call.String())
		}
		return &logicalLabelJoinPlan{Expr: call, Config: cfg, Child: child}, nil
	case "histogram_quantile":
		if result := logical.AnalyzeHistogramQuantileCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		quantile, err := numberLiteralArgument(call.Args[0], "histogram_quantile quantile")
		if err != nil {
			return nil, WithInternalContext(err, "building logical histogram_quantile %q", call.String())
		}
		child, err := BuildLogicalPlan(call.Args[1])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for histogram_quantile %q", call.String())
		}
		return &logicalHistogramQuantilePlan{Expr: call, Quantile: quantile, Child: child}, nil
	case "histogram_fraction":
		if result := logical.AnalyzeHistogramFractionCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		lower, err := numberLiteralArgument(call.Args[0], "histogram_fraction lower bound")
		if err != nil {
			return nil, WithInternalContext(err, "building logical histogram_fraction %q", call.String())
		}
		upper, err := numberLiteralArgument(call.Args[1], "histogram_fraction upper bound")
		if err != nil {
			return nil, WithInternalContext(err, "building logical histogram_fraction %q", call.String())
		}
		child, err := BuildLogicalPlan(call.Args[2])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for histogram_fraction %q", call.String())
		}
		return &logicalHistogramFractionPlan{Expr: call, Lower: lower, Upper: upper, Child: child}, nil
	case "histogram_count", "histogram_sum", "histogram_avg", "histogram_stddev", "histogram_stdvar":
		if result := logical.AnalyzeHistogramProjectionCall(name, call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalHistogramProjectionPlan{Expr: call, Func: name, Child: child}, nil
	case "histogram_quantiles":
		if result := logical.AnalyzeHistogramQuantilesCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		label, err := stringLiteralArgument(call.Args[1], "histogram_quantiles label")
		if err != nil {
			return nil, WithInternalContext(err, "building logical histogram_quantiles %q", call.String())
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for histogram_quantiles %q", call.String())
		}
		paramNumbers := make([]*float64, 0, len(call.Args)-2)
		paramChildren := make([]logicalPlan, 0, len(call.Args)-2)
		for _, arg := range call.Args[2:] {
			builtArg, err := BuildLogicalPlan(arg)
			if err != nil {
				return nil, WithInternalContext(err, "building logical histogram_quantiles scalar parameter for %q", call.String())
			}
			paramChildren = append(paramChildren, builtArg)
			if literal, ok := unwrapTransparentExpr(arg).(*parser.NumberLiteral); ok {
				valueCopy := literal.Val
				paramNumbers = append(paramNumbers, &valueCopy)
			} else {
				paramNumbers = append(paramNumbers, nil)
			}
		}
		return &logicalHistogramQuantilesPlan{Expr: call, Label: label, ParamNumbers: paramNumbers, ParamChildren: paramChildren, Child: child}, nil
	case "vector":
		if result := logical.AnalyzeVectorCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for vector %q", call.String())
		}
		return &logicalVectorPlan{Expr: call, Child: child}, nil
	case "pi", "time":
		return &logicalScalarBuiltinPlan{Expr: call, Func: name}, nil
	case "info":
		if result := logical.AnalyzeInfoCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for info %q", call.String())
		}
		var selectorMatchers []*promlabels.Matcher
		if len(call.Args) > 1 {
			selector, ok := unwrapTransparentExpr(call.Args[1]).(*parser.VectorSelector)
			if !ok {
				return nil, WithInternalContext(NewBadDataErrorf("info selector must be a label selector"), "building logical info %q", call.String())
			}
			selectorMatchers = clonePromMatchers(selector.LabelMatchers)
		}
		return &logicalInfoPlan{Expr: call, SelectorMatchers: selectorMatchers, Child: child}, nil
	case "scalar":
		if result := logical.AnalyzeScalarCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for scalar %q", call.String())
		}
		return &logicalScalarConvertPlan{Expr: call, Child: child}, nil
	case "round":
		if result := logical.AnalyzeRoundCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for round %q", call.String())
		}
		plan := &logicalRoundPlan{Expr: call, Child: child}
		if len(call.Args) > 1 {
			decimals, err := numberLiteralArgument(call.Args[1], "round decimals")
			if err != nil {
				return nil, WithInternalContext(err, "building logical round %q", call.String())
			}
			plan.Decimals = &decimals
		}
		return plan, nil
	case "sort", "sort_desc", "sort_by_label", "sort_by_label_desc":
		if result := logical.AnalyzeSortCall(name, call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		labels := make([]string, 0, max(0, len(call.Args)-1))
		for _, arg := range call.Args[1:] {
			label, err := stringLiteralArgument(arg, name+" label")
			if err != nil {
				return nil, WithInternalContext(err, "building logical %s %q", name, call.String())
			}
			labels = append(labels, label)
		}
		return &logicalSortPlan{Expr: call, Func: name, Labels: labels, Child: child}, nil
	case "rate", "irate":
		analyze := logical.AnalyzeRateCall
		if name == "irate" {
			analyze = logical.AnalyzeIrateCall
		}
		if result := analyze(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalRatePlan{Expr: call, Func: name, Child: child}, nil
	case "increase":
		if result := logical.AnalyzeIncreaseCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for increase %q", call.String())
		}
		return &logicalIncreasePlan{Expr: call, Child: child}, nil
	case "delta", "idelta":
		analyze := logical.AnalyzeDeltaCall
		if name == "idelta" {
			analyze = logical.AnalyzeIDeltaCall
		}
		if result := analyze(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalDeltaPlan{Expr: call, Func: name, Child: child}, nil
	case "changes":
		if result := logical.AnalyzeChangesCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for changes %q", call.String())
		}
		return &logicalChangesPlan{Expr: call, Child: child}, nil
	case "deriv":
		if result := logical.AnalyzeDerivCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for deriv %q", call.String())
		}
		return &logicalDerivPlan{Expr: call, Child: child}, nil
	case "last_over_time", "first_over_time", "sum_over_time", "avg_over_time", "max_over_time", "min_over_time", "count_over_time", "stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "resets", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		if result := logical.AnalyzeRangeFunctionCall(name, call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalRangeFunctionPlan{Expr: call, Func: name, Child: child}, nil
	case "predict_linear":
		if result := logical.AnalyzePredictLinearCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		duration, err := numberLiteralArgument(call.Args[1], "predict_linear duration")
		if err != nil {
			return nil, WithInternalContext(err, "building logical predict_linear %q", call.String())
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for predict_linear %q", call.String())
		}
		return &logicalRangeFunctionPlan{Expr: call, Func: name, ParamNumber: cloneFloat64(duration), Child: child}, nil
	case "double_exponential_smoothing", "holt_winters":
		if result := logical.AnalyzeDoubleExponentialSmoothingCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		sf, err := numberLiteralArgument(call.Args[1], name+" smoothing factor")
		if err != nil {
			return nil, WithInternalContext(err, "building logical %s %q", name, call.String())
		}
		tf, err := numberLiteralArgument(call.Args[2], name+" trend factor")
		if err != nil {
			return nil, WithInternalContext(err, "building logical %s %q", name, call.String())
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalRangeFunctionPlan{Expr: call, Func: name, ParamNumbers: []*float64{cloneFloat64(sf), cloneFloat64(tf)}, Child: child}, nil
	case "abs", "ceil", "floor", "sgn",
		"exp", "ln", "log2", "log10", "sqrt",
		"clamp", "clamp_min", "clamp_max",
		"sin", "cos", "tan", "asin", "acos", "atan",
		"sinh", "cosh", "tanh", "asinh", "acosh", "atanh",
		"deg", "rad", "timestamp",
		"minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return buildLogicalPointwiseFunctionPlan(name, call)
	case "quantile_over_time":
		if result := logical.AnalyzeQuantileOverTimeCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		quantile, err := numberLiteralArgument(call.Args[0], "quantile_over_time quantile")
		if err != nil {
			return nil, WithInternalContext(err, "building logical quantile_over_time %q", call.String())
		}
		child, err := BuildLogicalPlan(call.Args[1])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for quantile_over_time %q", call.String())
		}
		return &logicalQuantileOverTimePlan{Expr: call, Quantile: quantile, Child: child}, nil
	case "absent":
		if result := logical.AnalyzeAbsentCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for absent %q", call.String())
		}
		return &logicalAbsentPlan{Expr: call, OutputMetric: deriveAbsentOutputMetric(call.Args[0]), Child: child}, nil
	case "absent_over_time":
		if result := logical.AnalyzeAbsentOverTimeCall(call); !result.Supported {
			return nil, NewPlanBuildError(call, result, "call planning")
		}
		child, err := BuildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, WithInternalContext(err, "building logical child plan for absent_over_time %q", call.String())
		}
		return &logicalAbsentOverTimePlan{Expr: call, OutputMetric: deriveAbsentOutputMetric(call.Args[0]), Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(call)
	}
}
