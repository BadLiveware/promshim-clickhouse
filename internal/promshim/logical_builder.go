package promshim

import (
	"strings"

	"ch-observability/internal/promshim/model"
	"ch-observability/internal/promshim/plan"
	commonmodel "github.com/prometheus/common/model"
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
	case "rate", "irate":
		analyze := plan.AnalyzeRateCall
		if name == "irate" {
			analyze = plan.AnalyzeIrateCall
		}
		if result := analyze(call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		if !expressionContainsSubquery(call.Args[0]) {
			return buildLogicalDelegatedLeaf(call)
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
		if !expressionContainsSubquery(call.Args[0]) {
			return buildLogicalDelegatedLeaf(call)
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
		if !expressionContainsSubquery(call.Args[0]) {
			return buildLogicalDelegatedLeaf(call)
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
		if !expressionContainsSubquery(call.Args[0]) {
			return buildLogicalDelegatedLeaf(call)
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for deriv %q", call.String())
		}
		return &logicalDerivPlan{Expr: call, Child: child}, nil
	case "last_over_time", "sum_over_time", "avg_over_time", "max_over_time", "min_over_time", "count_over_time":
		if result := plan.AnalyzeRangeFunctionCall(name, call); !result.Supported {
			return nil, newPlanBuildError(call, result, "call planning")
		}
		child, err := buildLogicalPlan(call.Args[0])
		if err != nil {
			return nil, withInternalContext(err, "building logical child plan for %s %q", name, call.String())
		}
		return &logicalRangeFunctionPlan{Expr: call, Func: name, Child: child}, nil
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

func aggregatePlanParam(expr *parser.AggregateExpr) (*float64, string, error) {
	if expr == nil || expr.Param == nil {
		return nil, "", nil
	}
	switch expr.Op {
	case parser.TOPK, parser.BOTTOMK:
		literal, ok := unwrapTransparentExpr(expr.Param).(*parser.NumberLiteral)
		if !ok {
			return nil, "", newUnsupportedErrorf("aggregation operator %q currently requires a literal scalar parameter", strings.ToLower(expr.Op.String()))
		}
		value := literal.Val
		return &value, "", nil
	case parser.COUNT_VALUES:
		label, err := stringLiteralArgument(expr.Param, "count_values label parameter")
		if err != nil {
			return nil, "", err
		}
		if !commonmodel.UTF8Validation.IsValidLabelName(label) {
			return nil, "", newBadDataErrorf("invalid destination label name in count_values(): %s", label)
		}
		return nil, label, nil
	default:
		return nil, "", nil
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

func numberLiteralArgument(expr parser.Expr, description string) (float64, error) {
	expr = unwrapTransparentExpr(expr)
	literal, ok := expr.(*parser.NumberLiteral)
	if !ok {
		return 0, newBadDataErrorf("expected numeric literal for %s, got %T", description, expr)
	}
	return literal.Val, nil
}

func deriveAbsentOutputMetric(expr parser.Expr) map[string]string {
	expr = unwrapTransparentExpr(expr)

	var matchers []*promlabels.Matcher
	switch node := expr.(type) {
	case *parser.VectorSelector:
		matchers = node.LabelMatchers
	case *parser.MatrixSelector:
		vectorSelector, ok := node.VectorSelector.(*parser.VectorSelector)
		if !ok {
			return map[string]string{}
		}
		matchers = vectorSelector.LabelMatchers
	default:
		return map[string]string{}
	}

	result := make(map[string]string)
	has := make(map[string]bool, len(matchers))
	for _, matcher := range matchers {
		if matcher.Name == promlabels.MetricName {
			continue
		}
		if matcher.Type == promlabels.MatchEqual && !has[matcher.Name] {
			result[matcher.Name] = matcher.Value
			has[matcher.Name] = true
			continue
		}
		delete(result, matcher.Name)
	}
	return result
}

func cloneVectorMatching(vectorMatching *parser.VectorMatching) *parser.VectorMatching {
	if vectorMatching == nil {
		return nil
	}
	cloned := &parser.VectorMatching{
		Card:           vectorMatching.Card,
		MatchingLabels: append([]string(nil), vectorMatching.MatchingLabels...),
		On:             vectorMatching.On,
		Include:        append([]string(nil), vectorMatching.Include...),
	}
	if vectorMatching.FillValues.LHS != nil {
		lhs := *vectorMatching.FillValues.LHS
		cloned.FillValues.LHS = &lhs
	}
	if vectorMatching.FillValues.RHS != nil {
		rhs := *vectorMatching.FillValues.RHS
		cloned.FillValues.RHS = &rhs
	}
	return cloned
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

func expressionContainsSubquery(expr parser.Expr) bool {
	expr = unwrapTransparentExpr(expr)
	switch node := expr.(type) {
	case *parser.SubqueryExpr:
		return true
	case *parser.Call:
		for _, arg := range node.Args {
			if expressionContainsSubquery(arg) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		if node.Param != nil && expressionContainsSubquery(node.Param) {
			return true
		}
		return expressionContainsSubquery(node.Expr)
	case *parser.BinaryExpr:
		return expressionContainsSubquery(node.LHS) || expressionContainsSubquery(node.RHS)
	case *parser.UnaryExpr:
		return expressionContainsSubquery(node.Expr)
	case *parser.ParenExpr, *parser.StepInvariantExpr:
		return false
	case *parser.MatrixSelector:
		return expressionContainsSubquery(node.VectorSelector)
	default:
		return false
	}
}
