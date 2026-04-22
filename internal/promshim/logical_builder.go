package promshim

import (
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type logicalPlan = plan.LogicalPlan

type logicalLeafExprPlan = plan.LogicalLeafExprPlan

type logicalScalarLiteralPlan = plan.LogicalScalarLiteralPlan

type logicalUnaryPlan = plan.LogicalUnaryPlan

type logicalBinaryPlan = plan.LogicalBinaryPlan

type logicalAggregationPlan = plan.LogicalAggregationPlan

type logicalHistogramQuantilePlan = plan.LogicalHistogramQuantilePlan

type logicalHistogramFractionPlan = plan.LogicalHistogramFractionPlan

type logicalHistogramProjectionPlan = plan.LogicalHistogramProjectionPlan

type logicalRangeFunctionPlan = plan.LogicalRangeFunctionPlan

type logicalVectorPlan = plan.LogicalVectorPlan

type logicalRoundPlan = plan.LogicalRoundPlan

type logicalSortPlan = plan.LogicalSortPlan

type logicalScalarConvertPlan = plan.LogicalScalarConvertPlan

type logicalInfoPlan = plan.LogicalInfoPlan

type logicalPointwiseFunctionPlan = plan.LogicalPointwiseFunctionPlan

type logicalScalarBuiltinPlan = plan.LogicalScalarBuiltinPlan

type logicalRatePlan = plan.LogicalRatePlan

type logicalIncreasePlan = plan.LogicalIncreasePlan

type logicalDeltaPlan = plan.LogicalDeltaPlan

type logicalChangesPlan = plan.LogicalChangesPlan

type logicalDerivPlan = plan.LogicalDerivPlan

type logicalQuantileOverTimePlan = plan.LogicalQuantileOverTimePlan

type logicalAbsentPlan = plan.LogicalAbsentPlan

type logicalAbsentOverTimePlan = plan.LogicalAbsentOverTimePlan

type logicalSubqueryPlan = plan.LogicalSubqueryPlan

type logicalLabelReplacePlan = plan.LogicalLabelReplacePlan

type logicalLabelJoinPlan = plan.LogicalLabelJoinPlan

func buildLogicalPlan(expr parser.Expr) (logicalPlan, error) {
	expr = unwrapTransparentExpr(expr)

	switch node := expr.(type) {
	case *parser.NumberLiteral:
		return &logicalScalarLiteralPlan{Expr: node, Value: node.Val}, nil
	case *parser.Call:
		return buildLogicalCallPlan(node)
	case *parser.UnaryExpr:
		result := plan.AnalyzeUnaryExpression(node)
		if !result.Supported {
			return nil, newPlanBuildError(node, result, "unary planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for unary expression %q", node.String())
		}
		return &logicalUnaryPlan{Expr: node, Op: node.Op, Child: child}, nil
	case *parser.BinaryExpr:
		result := plan.AnalyzeBinaryExpression(node)
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
		return &logicalBinaryPlan{Expr: node, Op: node.Op, VectorMatching: cloneVectorMatching(node.VectorMatching), ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, nil
	case *parser.SubqueryExpr:
		result := plan.AnalyzeSubqueryExpression(node)
		if !result.Supported {
			return nil, newPlanBuildError(node, result, "subquery planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for subquery %q", node.String())
		}
		delegatable := plan.AnalyzeDelegatableSubqueryExpression(node).Supported
		return &logicalSubqueryPlan{Expr: node, Range: node.Range, Step: node.Step, Offset: node.OriginalOffset, Timestamp: node.Timestamp, StartOrEnd: node.StartOrEnd, DelegatedLeafCompatible: delegatable, Child: child}, nil
	case *parser.AggregateExpr:
		result := plan.AnalyzeAggregateExpression(node)
		if !result.Supported {
			return nil, newPlanBuildError(node, result, "aggregate planning")
		}
		child, err := buildLogicalPlan(node.Expr)
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for aggregate %q", node.String())
		}
		paramNumber, paramString, err := aggregatePlanParam(node)
		if err != nil {
			return nil, withInternalContext(err, "building logical aggregation parameter for %q", node.String())
		}
		return &logicalAggregationPlan{Expr: node, Op: node.Op, Grouping: append([]string(nil), node.Grouping...), Without: node.Without, ParamNumber: paramNumber, ParamString: paramString, Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(expr)
	}
}

func buildLogicalDelegatedLeaf(expr parser.Expr) (logicalPlan, error) {
	expr = unwrapTransparentExpr(expr)
	result := plan.AnalyzeDelegatableExpression(expr)
	if !result.Supported {
		return nil, newPlanBuildError(expr, result, "delegated leaf planning")
	}
	return &logicalLeafExprPlan{Expr: expr}, nil
}

func buildLogicalCallPlan(call *parser.Call) (logicalPlan, error) {
	name := strings.ToLower(call.Func.Name)
	switch name {
	case "label_replace":
		if result := plan.AnalyzeLabelReplaceCall(call); !result.Supported {
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
		cfg, err := model.BuildLabelReplaceConfig(dst, repl, src, regexStr)
		if err != nil {
			return nil, withInternalContext(newBadDataErrorf("%s", err.Error()), "building logical label_replace %q", call.String())
		}
		return &logicalLabelReplacePlan{Expr: call, Config: cfg, Child: child}, nil
	case "label_join":
		if result := plan.AnalyzeLabelJoinCall(call); !result.Supported {
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
		cfg, err := model.BuildLabelJoinConfig(dst, sep, srcLabels)
		if err != nil {
			return nil, withInternalContext(newBadDataErrorf("%s", err.Error()), "building logical label_join %q", call.String())
		}
		return &logicalLabelJoinPlan{Expr: call, Config: cfg, Child: child}, nil
	case "histogram_quantile":
		if result := plan.AnalyzeHistogramQuantileCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		quantile, err := numberLiteralArgument(call.Args[0], "histogram_quantile quantile")
		if err != nil {
			return nil, withInternalContext(err, "building logical histogram_quantile %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[1])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for histogram_quantile %q", call.String())
		}
		return &logicalHistogramQuantilePlan{Expr: call, Quantile: quantile, Child: child}, nil
	case "histogram_fraction":
		if result := plan.AnalyzeHistogramFractionCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		lower, err := numberLiteralArgument(call.Args[0], "histogram_fraction lower bound")
		if err != nil {
			return nil, withInternalContext(err, "building logical histogram_fraction %q", call.String())
		}
		upper, err := numberLiteralArgument(call.Args[1], "histogram_fraction upper bound")
		if err != nil {
			return nil, withInternalContext(err, "building logical histogram_fraction %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[2])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for histogram_fraction %q", call.String())
		}
		return &logicalHistogramFractionPlan{Expr: call, Lower: lower, Upper: upper, Child: child}, nil
	case "histogram_count", "histogram_sum", "histogram_avg":
		if result := plan.AnalyzeHistogramProjectionCall(name, call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalHistogramProjectionPlan{Expr: call, Func: name, Child: child}, nil
	case "vector":
		if result := plan.AnalyzeVectorCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for vector %q", call.String())
		}
		return &logicalVectorPlan{Expr: call, Child: child}, nil
	case "pi", "time":
		return &logicalScalarBuiltinPlan{Expr: call, Func: name}, nil
	case "info":
		if result := plan.AnalyzeInfoCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for info %q", call.String())
		}
		var selectorMatchers []*promlabels.Matcher
		if len(call.Args) > 1 {
			selector, ok := unwrapTransparentExpr(call.Args[1]).(*parser.VectorSelector)
			if !ok {
				return nil, withInternalContext(newBadDataErrorf("info selector must be a label selector"), "building logical info %q", call.String())
			}
			selectorMatchers = clonePromMatchers(selector.LabelMatchers)
		}
		return &logicalInfoPlan{Expr: call, SelectorMatchers: selectorMatchers, Child: child}, nil
	case "scalar":
		if result := plan.AnalyzeScalarCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for scalar %q", call.String())
		}
		return &logicalScalarConvertPlan{Expr: call, Child: child}, nil
	case "round":
		if result := plan.AnalyzeRoundCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for round %q", call.String())
		}
		plan := &logicalRoundPlan{Expr: call, Child: child}
		if len(call.Args) > 1 {
			decimals, err := numberLiteralArgument(call.Args[1], "round decimals")
			if err != nil {
				return nil, withInternalContext(err, "building logical round %q", call.String())
			}
			plan.Decimals = &decimals
		}
		return plan, nil
	case "sort", "sort_desc", "sort_by_label", "sort_by_label_desc":
		if result := plan.AnalyzeSortCall(name, call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		labels := make([]string, 0, max(0, len(call.Args)-1))
		for _, arg := range call.Args[1:] {
			label, err := stringLiteralArgument(arg, name+" label")
			if err != nil {
				return nil, withInternalContext(err, "building logical %s %q", name, call.String())
			}
			labels = append(labels, label)
		}
		return &logicalSortPlan{Expr: call, Func: name, Labels: labels, Child: child}, nil
	case "rate", "irate":
		analyze := plan.AnalyzeRateCall
		if name == "irate" {
			analyze = plan.AnalyzeIrateCall
		}
		if result := analyze(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalRatePlan{Expr: call, Func: name, Child: child}, nil
	case "increase":
		if result := plan.AnalyzeIncreaseCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for increase %q", call.String())
		}
		return &logicalIncreasePlan{Expr: call, Child: child}, nil
	case "delta", "idelta":
		analyze := plan.AnalyzeDeltaCall
		if name == "idelta" {
			analyze = plan.AnalyzeIDeltaCall
		}
		if result := analyze(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalDeltaPlan{Expr: call, Func: name, Child: child}, nil
	case "changes":
		if result := plan.AnalyzeChangesCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for changes %q", call.String())
		}
		return &logicalChangesPlan{Expr: call, Child: child}, nil
	case "deriv":
		if result := plan.AnalyzeDerivCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for deriv %q", call.String())
		}
		return &logicalDerivPlan{Expr: call, Child: child}, nil
	case "last_over_time", "sum_over_time", "avg_over_time", "max_over_time", "min_over_time", "count_over_time", "stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "resets":
		if result := plan.AnalyzeRangeFunctionCall(name, call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalRangeFunctionPlan{Expr: call, Func: name, Child: child}, nil
	case "predict_linear":
		if result := plan.AnalyzePredictLinearCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		duration, err := numberLiteralArgument(call.Args[1], "predict_linear duration")
		if err != nil {
			return nil, withInternalContext(err, "building logical predict_linear %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for predict_linear %q", call.String())
		}
		return &logicalRangeFunctionPlan{Expr: call, Func: name, ParamNumber: cloneFloat64(duration), Child: child}, nil
	case "double_exponential_smoothing", "holt_winters":
		if result := plan.AnalyzeDoubleExponentialSmoothingCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		sf, err := numberLiteralArgument(call.Args[1], name+" smoothing factor")
		if err != nil {
			return nil, withInternalContext(err, "building logical %s %q", name, call.String())
		}
		tf, err := numberLiteralArgument(call.Args[2], name+" trend factor")
		if err != nil {
			return nil, withInternalContext(err, "building logical %s %q", name, call.String())
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
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
		if result := plan.AnalyzeQuantileOverTimeCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		quantile, err := numberLiteralArgument(call.Args[0], "quantile_over_time quantile")
		if err != nil {
			return nil, withInternalContext(err, "building logical quantile_over_time %q", call.String())
		}
		child, err := buildLogicalPlan(call.Args[1])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for quantile_over_time %q", call.String())
		}
		return &logicalQuantileOverTimePlan{Expr: call, Quantile: quantile, Child: child}, nil
	case "absent":
		if result := plan.AnalyzeAbsentCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for absent %q", call.String())
		}
		return &logicalAbsentPlan{Expr: call, OutputMetric: deriveAbsentOutputMetric(call.Args[0]), Child: child}, nil
	case "absent_over_time":
		if result := plan.AnalyzeAbsentOverTimeCall(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for absent_over_time %q", call.String())
		}
		return &logicalAbsentOverTimePlan{Expr: call, OutputMetric: deriveAbsentOutputMetric(call.Args[0]), Child: child}, nil
	default:
		return buildLogicalDelegatedLeaf(call)
	}
}
