package native

import (
	"fmt"
	"sort"
	"strings"

	logicalpkg "ch-observability/internal/promshim/logical"

	"github.com/prometheus/prometheus/promql/parser"
)

type describedLogicalPlan interface {
	ExprString() string
	ValueType() parser.ValueType
}

func (a *Analysis) walk(node logicalpkg.Node) *LoweringInfo {
	info := a.walkInner(node)
	if info != nil {
		info.Shape = computeSelectorShape(a, node, info)
	}
	return info
}

func (a *Analysis) walkInner(node logicalpkg.Node) *LoweringInfo {
	if node == nil {
		return nil
	}
	if existing := a.byNode[node]; existing != nil {
		return existing
	}

	expr, outputKind := describeLogicalPlan(node)
	info := &LoweringInfo{Expr: expr, OutputKind: outputKind}
	a.byNode[node] = info

	switch n := node.(type) {
	case *logicalpkg.LeafExprPlan:
		info.NodeType = "leaf"
		selector, err := buildSelectorSource(n.Expr)
		if err != nil {
			info.NativeReason = fmt.Sprintf("selector source analysis failed: %v", err)
			return info
		}
		info.NativeLowerable = selector != nil
		if selector != nil {
			// Task 13c-9: Pre-compute InferredMatchers/PushedMatchers at Analyze
			// time so renderers consulting info.LeafSelector see the enriched
			// matchers without the optimizer having to mutate a cloned selector.
			populateSelectorInferredAndPushedMatchers(selector)
			info.NativeReason = "selector leaf can seed repo-owned native SQL source lowering"
			info.SubtreeShape = SubtreeShapeLeafSource
			info.DropsMetric = false
			info.LeafSelector = selector
			info.SourceExpr = &SourceExprView{
				Selector:     selector,
				ValueExpr:    "{value}",
				TagsExpr:     "{tags}",
				SourcePromQL: n.Expr,
				DropsMetric:  false,
			}
		} else {
			info.NativeReason = "delegatable leaf expression is not a selector-backed native source"
		}
		info.LabelLineage = leafLabelLineage()
		info.TimeRequirements = leafTimeRequirements(n.Expr)
		return info
	case *logicalpkg.ScalarLiteralPlan:
		info.NodeType = "scalar"
		info.NativeLowerable = true
		info.NativeReason = "scalar literal can lower to a native synthetic scalar series"
		info.SubtreeShape = SubtreeShapeSyntheticSeries
		info.DropsMetric = true
		info.SyntheticSeries = &SyntheticSeriesView{Func: "literal", Value: n.Value}
		return info
	case *logicalpkg.UnaryPlan:
		child := a.walk(n.Child)
		info.NodeType = "unary"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if literal, ok := syntheticLiteralValue(child); ok {
			if folded, ok := foldUnaryScalarLiteral(n.Op, literal); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-only unary expression can fold to a native synthetic scalar series"
				info.SubtreeShape = SubtreeShapeSyntheticSeries
				info.DropsMetric = true
				info.SyntheticSeries = &SyntheticSeriesView{Func: "literal", Value: folded}
				return info
			}
		}
		if child.NativeLowerable {
			childValueExpr := "{value}"
			if child.SourceExpr != nil {
				childValueExpr = child.SourceExpr.ValueExpr
			}
			valueExpr, dropsMetric, ok := applyUnarySourceTransform(n.Op, childValueExpr, child.DropsMetric)
			if ok {
				if dropsMetric {
					info.LabelLineage = withMetricNameState(info.LabelLineage, LabelLineageDropped)
				}
				info.NativeLowerable = true
				if child.SourceExpr != nil && (child.SourceExpr.Selector != nil || child.SourceExpr.SourcePromQL != nil) {
					info.NativeReason = "unary transform can be applied inside a native SQL source expression"
					info.SubtreeShape = SubtreeShapeUnarySourceExpr
					info.DropsMetric = dropsMetric
					clonedSelector := cloneSelectorSource(child.SourceExpr.Selector)
					info.SourceExpr = &SourceExprView{
						Selector:     clonedSelector,
						ValueExpr:    valueExpr,
						TagsExpr:     tagsExprForMetricDrop(dropsMetric),
						SourcePromQL: child.SourceExpr.SourcePromQL,
						DropsMetric:  dropsMetric,
					}
				} else if result, ok := applyUnaryValueTransform(n.Op, child.OutputKind, child.DropsMetric, child.LabelLineage); ok {
					info.NativeReason = "unary transform lowers via a native value-transform wrapper"
					info.LabelLineage = result.Lineage
					info.SubtreeShape = SubtreeShapeValueTransform
					info.OutputKind = result.OutputKind
					info.DropsMetric = result.DropsMetric
					view := result.View
					info.ValueTransform = &view
				}
				return info
			}
		}
		info.NativeReason = fmt.Sprintf("unary operator %q is not part of the current native source subset", n.Op.String())
		return info
	case *logicalpkg.BinaryPlan:
		lhs := a.walk(n.LHS)
		rhs := a.walk(n.RHS)
		info.NodeType = "binary"
		info.Children = []*LoweringInfo{lhs, rhs}
		info.TimeRequirements = combineTimeRequirements(lhs.TimeRequirements, rhs.TimeRequirements)
		info.LabelLineage = unknownLineage()

		if lhsLiteral, ok := syntheticLiteralValue(lhs); ok {
			if rhsLiteral, ok := syntheticLiteralValue(rhs); ok {
				if folded, ok := foldBinaryScalarLiteral(n.Op, n.ReturnBool, lhsLiteral, rhsLiteral); ok {
					info.NativeLowerable = true
					info.NativeReason = "scalar-only expression can fold to a native synthetic scalar series"
					info.SubtreeShape = SubtreeShapeSyntheticSeries
					info.DropsMetric = true
					info.SyntheticSeries = &SyntheticSeriesView{Func: "literal", Value: folded}
					return info
				}
			}
		}

		lhsScalar, lhsIsScalar := n.LHS.(*logicalpkg.ScalarLiteralPlan)
		rhsScalar, rhsIsScalar := n.RHS.(*logicalpkg.ScalarLiteralPlan)
		lhsLiteral, lhsLiteralOK := syntheticLiteralValue(lhs)
		rhsLiteral, rhsLiteralOK := syntheticLiteralValue(rhs)
		switch {
		case lhsLiteralOK && !lhsIsScalar && rhs.NativeLowerable && rhs.OutputKind == OutputKindInstantVector:
			if result, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, rhs.OutputKind, rhs.LabelLineage, lhsLiteral, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-expression/vector bool comparison lowers via a native value-transform wrapper"
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
			if result, ok := applyScalarValueTransform(n.Op, rhs.OutputKind, rhs.LabelLineage, lhsLiteral, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-expression/vector arithmetic lowers via a native value-transform wrapper"
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
			if result, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, rhs.OutputKind, rhs.LabelLineage, lhsLiteral, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-expression/vector comparison filters via a native value-transform wrapper"
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
		case rhsLiteralOK && !rhsIsScalar && lhs.NativeLowerable && lhs.OutputKind == OutputKindInstantVector:
			if result, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, lhs.OutputKind, lhs.LabelLineage, rhsLiteral, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/scalar-expression bool comparison lowers via a native value-transform wrapper"
				populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				return info
			}
			if result, ok := applyScalarValueTransform(n.Op, lhs.OutputKind, lhs.LabelLineage, rhsLiteral, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/scalar-expression arithmetic lowers via a native value-transform wrapper"
				populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				return info
			}
			if result, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, lhs.OutputKind, lhs.LabelLineage, rhsLiteral, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/scalar-expression comparison filters via a native value-transform wrapper"
				populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				return info
			}
		case lhsIsScalar && rhs.NativeLowerable:
			rhsValueExpr := "{value}"
			if rhs.SourceExpr != nil {
				rhsValueExpr = rhs.SourceExpr.ValueExpr
			}
			valueExpr, dropsMetric, ok := applyBinarySourceTransform(n.Op, rhsValueExpr, lhsScalar.Value, true)
			if ok {
				info.NativeLowerable = true
				info.LabelLineage = withMetricNameState(passthroughLabelLineage(rhs.LabelLineage), boolState(dropsMetric, LabelLineageDropped, rhs.LabelLineage.MetricName))
				if rhs.SourceExpr != nil && (rhs.SourceExpr.Selector != nil || rhs.SourceExpr.SourcePromQL != nil) {
					info.NativeReason = "scalar-vector arithmetic can be applied inside a native SQL source expression"
					clonedSelector := cloneSelectorSource(rhs.SourceExpr.Selector)
					tagsExpr := tagsExprForMetricDrop(dropsMetric)
					info.SubtreeShape = SubtreeShapeBinaryScalarSourceExpr
					info.DropsMetric = dropsMetric
					info.SourceExpr = &SourceExprView{
						Selector:     clonedSelector,
						ValueExpr:    valueExpr,
						TagsExpr:     tagsExpr,
						SourcePromQL: rhs.SourceExpr.SourcePromQL,
						DropsMetric:  dropsMetric,
					}
				} else if result, ok := applyScalarValueTransform(n.Op, rhs.OutputKind, rhs.LabelLineage, lhsScalar.Value, true); ok {
					info.NativeReason = "scalar-vector arithmetic lowers via a native value-transform wrapper"
					populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				}
				return info
			}
			if result, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, rhs.OutputKind, rhs.LabelLineage, lhsScalar.Value, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-vector bool comparison lowers via a native value-transform wrapper"
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
			if result, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, rhs.OutputKind, rhs.LabelLineage, lhsScalar.Value, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-vector comparison filters via a native value-transform wrapper"
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
		case rhsIsScalar && lhs.NativeLowerable:
			lhsValueExpr := "{value}"
			if lhs.SourceExpr != nil {
				lhsValueExpr = lhs.SourceExpr.ValueExpr
			}
			valueExpr, dropsMetric, ok := applyBinarySourceTransform(n.Op, lhsValueExpr, rhsScalar.Value, false)
			if ok {
				info.NativeLowerable = true
				info.LabelLineage = withMetricNameState(passthroughLabelLineage(lhs.LabelLineage), boolState(dropsMetric, LabelLineageDropped, lhs.LabelLineage.MetricName))
				if lhs.SourceExpr != nil && (lhs.SourceExpr.Selector != nil || lhs.SourceExpr.SourcePromQL != nil) {
					info.NativeReason = "vector-scalar arithmetic can be applied inside a native SQL source expression"
					clonedSelector := cloneSelectorSource(lhs.SourceExpr.Selector)
					tagsExpr := tagsExprForMetricDrop(dropsMetric)
					info.SubtreeShape = SubtreeShapeBinaryScalarSourceExpr
					info.DropsMetric = dropsMetric
					info.SourceExpr = &SourceExprView{
						Selector:     clonedSelector,
						ValueExpr:    valueExpr,
						TagsExpr:     tagsExpr,
						SourcePromQL: lhs.SourceExpr.SourcePromQL,
						DropsMetric:  dropsMetric,
					}
				} else if result, ok := applyScalarValueTransform(n.Op, lhs.OutputKind, lhs.LabelLineage, rhsScalar.Value, false); ok {
					info.NativeReason = "vector-scalar arithmetic lowers via a native value-transform wrapper"
					populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				}
				return info
			}
			if result, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, lhs.OutputKind, lhs.LabelLineage, rhsScalar.Value, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector-scalar bool comparison lowers via a native value-transform wrapper"
				populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				return info
			}
			if result, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, lhs.OutputKind, lhs.LabelLineage, rhsScalar.Value, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector-scalar comparison filters via a native value-transform wrapper"
				populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				return info
			}
		case isSyntheticScalarInfo(lhs) && rhs.NativeLowerable:
			if result, ok := applySyntheticScalarChildTransform(n.Op, n.ReturnBool, lhs.SyntheticSeries.Func, rhs.OutputKind, rhs.LabelLineage, true); ok {
				info.NativeLowerable = true
				if isComparisonBinaryOperator(n.Op) {
					if n.ReturnBool {
						info.NativeReason = "synthetic-scalar child bool comparison lowers via a native value-transform wrapper"
					} else {
						info.NativeReason = "synthetic-scalar/vector comparison filters via a native value-transform wrapper"
					}
				} else {
					info.NativeReason = "synthetic-scalar child arithmetic lowers via a native value-transform wrapper"
				}
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
		case isSyntheticScalarInfo(rhs) && lhs.NativeLowerable:
			if result, ok := applySyntheticScalarChildTransform(n.Op, n.ReturnBool, rhs.SyntheticSeries.Func, lhs.OutputKind, lhs.LabelLineage, false); ok {
				info.NativeLowerable = true
				if isComparisonBinaryOperator(n.Op) {
					if n.ReturnBool {
						info.NativeReason = "child/synthetic-scalar bool comparison lowers via a native value-transform wrapper"
					} else {
						info.NativeReason = "vector/synthetic-scalar comparison filters via a native value-transform wrapper"
					}
				} else {
					info.NativeReason = "child/synthetic-scalar arithmetic lowers via a native value-transform wrapper"
				}
				populateValueTransform(info, result, true, SubtreeShapeValueTransform)
				return info
			}
		case lhs.NativeLowerable && rhs.NativeLowerable && lhs.OutputKind == OutputKindInstantVector && rhs.OutputKind == OutputKindInstantVector:
			joinShape, ok := supportedNativeVectorJoinShape(n.VectorMatching)
			if ok && isSupportedNativeVectorJoinOp(n.Op, n.VectorMatching) {
				dropsMetric := nativeVectorJoinDropsMetricName(n.Op, n.ReturnBool)
				resultLineage := nativeVectorJoinLabelLineage(lhs.LabelLineage, rhs.LabelLineage, normalizeVectorMatching(n.VectorMatching), n.Op, n.ReturnBool)
				if dropsMetric {
					resultLineage = withMetricNameState(resultLineage, LabelLineageDropped)
				}
				info.NativeLowerable = true
				info.NativeReason = "vector-vector binary join can lower to native SQL for the supported matching subset"
				info.LabelLineage = resultLineage
				info.JoinShape = joinShape
				if n.VectorMatching != nil {
					info.JoinLabels = append([]string(nil), n.VectorMatching.MatchingLabels...)
				}
				info.SubtreeShape = SubtreeShapeBinaryVectorJoin
				info.DropsMetric = dropsMetric
				return info
			}
		}

		info.NativeReason = "binary expression currently lowers natively only for scalar/vector arithmetic or supported vector-vector joins over lowerable children"
		return info
	case *logicalpkg.AggregationPlan:
		child := a.walk(n.Child)
		info.NodeType = "aggregation"
		info.Children = []*LoweringInfo{child}
		if IsSelectionNativeAggregation(n.Op) {
			info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		} else if n.Op == parser.COUNT_VALUES {
			info.LabelLineage = countValuesLabelLineage(child.LabelLineage, n.Grouping, n.Without, n.ParamString)
		} else {
			info.LabelLineage = aggregationLabelLineage(child.LabelLineage, n.Grouping, n.Without)
		}
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)

		reason := "pushing aggregation into native ClickHouse SQL over a lowerable child avoids materializing the full child result in Go"
		if IsSelectionNativeAggregation(n.Op) {
			reason = "pushing selection aggregation into native ClickHouse SQL over a lowerable child preserves the selected series without local materialization"
		} else if n.Op == parser.COUNT_VALUES {
			reason = "count_values can lower to native SQL by synthesizing the destination label from sample values before grouped counting"
		}
		sourceInfo := child
		emitZeroOnEmpty := false
		if zeroFillInfo, ok := zeroFillAggregationSource(n.Op, n.Grouping, n.Without, n.Child, a); ok {
			sourceInfo = zeroFillInfo
			emitZeroOnEmpty = true
			reason = "sum(... or vector(0)) can lower by aggregating the native child and emitting zero when the aggregate would otherwise be empty"
		} else if sourceInfo == nil || !sourceInfo.NativeLowerable || sourceInfo.OutputKind != OutputKindInstantVector {
			sourceInfo = nil
		}
		eligible := true
		if !isSupportedNativeAggregation(n.Op) {
			eligible = false
			reason = "aggregation operator is not supported by native SQL pushdown"
		} else if sourceInfo == nil {
			eligible = false
			reason = "aggregation child is not pushdown-safe; native pushdown currently requires a native-lowerable instant-vector child"
		}
		if !eligible {
			sourceInfo = nil
		}
		var sourceView *AggregationSourceView
		if sourceInfo != nil && sourceInfo.SourceExpr != nil && sourceInfo.SourceExpr.SourcePromQL != nil {
			sourceView = &AggregationSourceView{
				SourcePromQL: sourceInfo.SourceExpr.SourcePromQL,
				ValueExpr:    sourceInfo.SourceExpr.ValueExpr,
				TagsExpr:     sourceInfo.SourceExpr.TagsExpr,
				DropsMetric:  sourceInfo.SourceExpr.DropsMetric,
			}
		}
		info.Aggregation = &AggregationSupport{Eligible: eligible, Reason: reason, SourceInfo: sourceInfo, SourceView: sourceView, EmitZeroOnEmpty: emitZeroOnEmpty}
		if eligible {
			info.SubtreeShape = SubtreeShapeAggregation
			info.DropsMetric = false
		}
		info.NativeLowerable = eligible
		info.NativeReason = reason
		return info
	case *logicalpkg.HistogramQuantilePlan:
		child := a.walk(n.Child)
		info.NodeType = "histogram_quantile"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(map[string]string{})
		if child != nil {
			for label, state := range child.LabelLineage.Known {
				if label == "__name__" || label == "le" {
					continue
				}
				info.LabelLineage.Known[label] = state
			}
			info.LabelLineage.Wildcard = child.LabelLineage.Wildcard
		}
		info.LabelLineage.MetricName = LabelLineageDropped
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "histogram_quantile can lower to native SQL over shared classic histogram materialization"
			info.SubtreeShape = SubtreeShapeHistogramFunction
			info.DropsMetric = true
			info.HistogramFunc = "histogram_quantile"
			return info
		}
		info.NativeReason = "histogram_quantile currently stays on the local execution path"
		return info
	case *logicalpkg.HistogramFractionPlan:
		child := a.walk(n.Child)
		info.NodeType = "histogram_fraction"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(map[string]string{})
		if child != nil {
			for label, state := range child.LabelLineage.Known {
				if label == "__name__" || label == "le" {
					continue
				}
				info.LabelLineage.Known[label] = state
			}
			info.LabelLineage.Wildcard = child.LabelLineage.Wildcard
		}
		info.LabelLineage.MetricName = LabelLineageDropped
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "histogram_fraction can lower to native SQL over shared classic histogram materialization"
			info.SubtreeShape = SubtreeShapeHistogramFunction
			info.DropsMetric = true
			info.HistogramFunc = "histogram_fraction"
			return info
		}
		info.NativeReason = "histogram_fraction currently stays on the local execution path"
		return info
	case *logicalpkg.HistogramProjectionPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(map[string]string{})
		if child != nil {
			for label, state := range child.LabelLineage.Known {
				if label == "__name__" || label == "le" {
					continue
				}
				info.LabelLineage.Known[label] = state
			}
			info.LabelLineage.Wildcard = child.LabelLineage.Wildcard
		}
		info.LabelLineage.MetricName = LabelLineageDropped
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if (n.Func == "histogram_count" || n.Func == "histogram_sum" || n.Func == "histogram_avg" || n.Func == "histogram_stddev" || n.Func == "histogram_stdvar") && child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL over shared classic histogram materialization", n.Func)
			info.SubtreeShape = SubtreeShapeHistogramProjection
			info.DropsMetric = true
			return info
		}
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path", n.Func)
		return info
	case *logicalpkg.HistogramQuantilesPlan:
		child := a.walk(n.Child)
		info.NodeType = "histogram_quantiles"
		children := make([]*LoweringInfo, 0, 1+len(n.ParamChildren))
		children = append(children, child)
		info.LabelLineage = mutateDestinationLabel(syntheticOutputLineage(map[string]string{}), n.Label, LabelLineageSynthetic)
		if child != nil {
			for label, state := range child.LabelLineage.Known {
				if label == "__name__" || label == "le" {
					continue
				}
				info.LabelLineage.Known[label] = state
			}
			info.LabelLineage.Wildcard = child.LabelLineage.Wildcard
		}
		info.LabelLineage.MetricName = LabelLineageDropped
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		lowerable := child != nil && child.NativeLowerable && child.OutputKind == OutputKindInstantVector
		for _, paramChild := range n.ParamChildren {
			paramInfo := a.walk(paramChild)
			children = append(children, paramInfo)
			info.TimeRequirements = combineTimeRequirements(info.TimeRequirements, paramInfo.TimeRequirements)
			if paramInfo == nil || !paramInfo.NativeLowerable || paramInfo.OutputKind != OutputKindScalar {
				lowerable = false
				continue
			}
		}
		info.Children = children
		if lowerable {
			info.NativeLowerable = true
			info.NativeReason = "histogram_quantiles can lower to native SQL over shared classic histogram materialization plus scalar quantile argument bindings"
			info.SubtreeShape = SubtreeShapeHistogramFunction
			info.DropsMetric = true
			info.HistogramFunc = "histogram_quantiles"
			return info
		}
		info.NativeReason = "histogram_quantiles currently requires a lowerable instant-vector histogram child and lowerable scalar quantile arguments to run fully in native SQL"
		return info
	case *logicalpkg.RangeFunctionPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		preservesName := RangeFunctionPreservesMetricName(n.Func)
		metricNameState := LabelLineageDropped
		if preservesName {
			metricNameState = child.LabelLineage.MetricName
		}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), metricNameState)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeAggregateOverTime(n.Func) && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("range function %q can lower to native SQL for the first aggregate-over-time subset", n.Func)
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = !preservesName
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		if n.Func == "predict_linear" && n.ParamNumber != nil && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "predict_linear can lower to native SQL using the shared windowed-arrays source and simpleLinearRegression"
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		if n.Func == "resets" && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "resets can lower to native SQL using pairwise comparisons over the shared windowed-arrays source"
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		if (n.Func == "double_exponential_smoothing" || n.Func == "holt_winters") && len(n.ParamNumbers) == 2 && n.ParamNumbers[0] != nil && n.ParamNumbers[1] != nil && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL using arrayFold over the shared windowed-arrays source", n.Func)
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = fmt.Sprintf("range function %q currently stays on the local execution path until native range lowering lands", n.Func)
		return info
	case *logicalpkg.VectorPlan:
		child := a.walk(n.Child)
		info.NodeType = "vector"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(map[string]string{})
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindScalar {
			info.NativeLowerable = true
			info.NativeReason = "vector() can lower by lifting a native scalar child into a single-series instant vector"
			// Inherit the child's shape (synthetic-series, value-transform,
			// leaf-source, etc.) so SubtreeShape mirrors the pre-retirement
			// Fragment.Kind produced by cloning the child.
			info.SubtreeShape = child.SubtreeShape
			info.DropsMetric = child.DropsMetric
			info.OutputKind = OutputKindInstantVector
			if child.SourceExpr != nil {
				view := *child.SourceExpr
				info.SourceExpr = &view
			}
			if child.SyntheticSeries != nil {
				view := *child.SyntheticSeries
				info.SyntheticSeries = &view
			}
			if child.ValueTransform != nil {
				view := *child.ValueTransform
				info.ValueTransform = &view
			}
			if child.LeafSelector != nil {
				info.LeafSelector = child.LeafSelector
			}
			return info
		}
		info.NativeReason = "vector() currently requires a lowerable scalar child to run fully in native SQL"
		return info
	case *logicalpkg.SortPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL by ordering the instant-vector child while preserving range-mode passthrough semantics", n.Func)
			info.SubtreeShape = SubtreeShapeSortTransform
			info.DropsMetric = false
			info.OutputKind = child.OutputKind
			return info
		}
		info.NativeReason = fmt.Sprintf("%s currently requires a lowerable instant-vector child to run fully in native SQL", n.Func)
		return info
	case *logicalpkg.ScalarConvertPlan:
		child := a.walk(n.Child)
		info.NodeType = "scalar"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(map[string]string{})
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "scalar() can lower to native SQL by counting child vector rows"
			info.SubtreeShape = SubtreeShapeScalarConvert
			info.DropsMetric = true
			info.OutputKind = OutputKindScalar
			return info
		}
		info.NativeReason = "scalar() currently stays on the local execution path"
		return info
	case *logicalpkg.InfoPlan:
		child := a.walk(n.Child)
		info.NodeType = "info"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		for _, label := range infoJoinCopyLabelNames(n.SelectorMatchers) {
			info.LabelLineage = mutateDestinationLabel(info.LabelLineage, label, LabelLineageSynthetic)
		}
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "info() can lower to native SQL using native selector matching plus merged info-series label joins"
			info.SubtreeShape = SubtreeShapeInfoJoin
			info.DropsMetric = false
			info.OutputKind = OutputKindInstantVector
			return info
		}
		info.NativeReason = "info() currently requires a lowerable instant-vector child to run fully in native SQL"
		return info
	case *logicalpkg.PointwiseFunctionPlan:
		info.NodeType = n.Func
		var child *LoweringInfo
		children := make([]*LoweringInfo, 0, 1+len(n.ParamChildren))
		if n.Child != nil {
			child = a.walk(n.Child)
			children = append(children, child)
			info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
			info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
			for _, paramChild := range n.ParamChildren {
				if paramChild == nil {
					continue
				}
				paramInfo := a.walk(paramChild)
				children = append(children, paramInfo)
				info.TimeRequirements = combineTimeRequirements(info.TimeRequirements, paramInfo.TimeRequirements)
			}
			info.Children = children
			if isClampPointwiseFunction(n.Func) {
				if ok := applyNativeClamp(info, n.Func, child, children[1:]); ok {
					info.NativeLowerable = true
					info.NativeReason = fmt.Sprintf("%s can lower to native SQL by joining scalar bound fragments onto the vector child", n.Func)
					return info
				}
			}
			if template, ok := NativePointwiseSourceTemplate(n.Func, n.ParamNumbers); ok && child.SourceExpr != nil && isSupportedAggregationSourceInfo(child) {
				info.NativeLowerable = true
				info.NativeReason = fmt.Sprintf("%s can lower to a native SQL source expression", n.Func)
				clonedSelector := cloneSelectorSource(child.SourceExpr.Selector)
				composedValueExpr := composePointwiseSourceTemplate(template, child.SourceExpr.ValueExpr)
				tagsExpr := tagsExprForMetricDrop(true)
				info.SubtreeShape = SubtreeShapeUnarySourceExpr
				info.DropsMetric = true
				info.OutputKind = child.OutputKind
				info.SourceExpr = &SourceExprView{
					Selector:     clonedSelector,
					ValueExpr:    composedValueExpr,
					TagsExpr:     tagsExpr,
					SourcePromQL: child.SourceExpr.SourcePromQL,
					DropsMetric:  true,
				}
				return info
			}
		} else if isSupportedNativeSyntheticDateFunction(n.Func) {
			info.LabelLineage = syntheticOutputLineage(map[string]string{})
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to a native synthetic series", n.Func)
			info.SubtreeShape = SubtreeShapeSyntheticSeries
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.SyntheticSeries = &SyntheticSeriesView{Func: n.Func}
			return info
		}
		if child == nil {
			info.LabelLineage = syntheticOutputLineage(map[string]string{})
		}
		info.NodeType = n.Func
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path", n.Func)
		return info
	case *logicalpkg.ScalarBuiltinPlan:
		info.NodeType = n.Func
		info.LabelLineage = syntheticOutputLineage(map[string]string{})
		if isSupportedNativeSyntheticScalarBuiltin(n.Func) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to a native synthetic scalar series", n.Func)
			info.SubtreeShape = SubtreeShapeSyntheticSeries
			info.DropsMetric = true
			info.OutputKind = OutputKindScalar
			info.SyntheticSeries = &SyntheticSeriesView{Func: n.Func}
			return info
		}
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path", n.Func)
		return info
	case *logicalpkg.RoundPlan:
		child := a.walk(n.Child)
		info.NodeType = "round"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		toNearest := 1.0
		if n.Decimals != nil {
			toNearest = *n.Decimals
		}
		if child.NativeLowerable {
			if result, ok := applyRoundValueTransform(child.OutputKind, child.LabelLineage, toNearest); ok {
				info.NativeLowerable = true
				info.NativeReason = "round() can lower via a native value-transform wrapper over a native instant-vector child"
				populateValueTransform(info, result, false, SubtreeShapeValueTransform)
				return info
			}
		}
		info.NativeReason = "round() currently stays on the local execution path"
		return info
	case *logicalpkg.RatePlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeCounterRangeFunction(n.Func) && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL for the initial counter/range subset", n.Func)
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path until native range lowering lands", n.Func)
		return info
	case *logicalpkg.IncreasePlan:
		child := a.walk(n.Child)
		info.NodeType = "increase"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeCounterRangeFunction("increase") && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "increase can lower to native SQL for the initial counter/range subset"
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = "increase currently stays on the local execution path until native range lowering lands"
		return info
	case *logicalpkg.DeltaPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeCounterRangeFunction(n.Func) && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL for the initial counter/range subset", n.Func)
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path until native range lowering lands", n.Func)
		return info
	case *logicalpkg.ChangesPlan:
		child := a.walk(n.Child)
		info.NodeType = "changes"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeCounterRangeFunction("changes") && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "changes can lower to native SQL for the initial counter/range subset"
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = "changes currently stays on the local execution path until native range lowering lands"
		return info
	case *logicalpkg.DerivPlan:
		child := a.walk(n.Child)
		info.NodeType = "deriv"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeCounterRangeFunction("deriv") && isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "deriv can lower to native SQL for the initial counter/range subset"
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = "deriv currently stays on the local execution path until native range lowering lands"
		return info
	case *logicalpkg.QuantileOverTimePlan:
		child := a.walk(n.Child)
		info.NodeType = "quantile_over_time"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "quantile_over_time can lower to native SQL using the shared windowed-arrays source and Prometheus-compatible quantile interpolation"
			info.SubtreeShape = SubtreeShapeRangeFunction
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.RangeFunctionSubquery = rangeFunctionSubqueryChild(child)
			return info
		}
		info.NativeReason = "quantile_over_time currently requires a lowerable matrix child to run fully in native SQL"
		return info
	case *logicalpkg.AbsentPlan:
		child := a.walk(n.Child)
		info.NodeType = "absent"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(n.OutputMetric)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "absent() can lower to native SQL by testing whether the lowerable child instant vector returns any rows"
			info.SubtreeShape = SubtreeShapeAbsent
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.AbsentFunc = "absent"
			return info
		}
		info.NativeReason = "absent() currently requires a lowerable instant-vector child to run fully in native SQL"
		return info
	case *logicalpkg.AbsentOverTimePlan:
		child := a.walk(n.Child)
		info.NodeType = "absent_over_time"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(n.OutputMetric)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeRangeChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "absent_over_time can lower to native SQL by testing whether the lowerable range child window contains any samples"
			info.SubtreeShape = SubtreeShapeAbsent
			info.DropsMetric = true
			info.OutputKind = OutputKindInstantVector
			info.AbsentFunc = "absent_over_time"
			return info
		}
		info.NativeReason = "absent_over_time currently requires a lowerable range-selector/subquery child to run fully in native SQL"
		return info
	case *logicalpkg.SubqueryPlan:
		child := a.walk(n.Child)
		info.NodeType = "subquery"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = subqueryTimeRequirements(child.TimeRequirements, n.Range, n.Offset)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector && isSupportedNativeSubqueryChild(child) {
			info.NativeLowerable = true
			info.NativeReason = "subquery step-grid execution can lower to native SQL for selector-backed instant-vector children"
			info.SubtreeShape = SubtreeShapeSubquery
			info.DropsMetric = false
			info.OutputKind = OutputKindRangeMatrix
			return info
		}
		info.NativeReason = "subquery step-grid execution currently stays on the local/delegated paths until native range lowering lands"
		return info
	case *logicalpkg.LabelReplacePlan:
		child := a.walk(n.Child)
		info.NodeType = "label_replace"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = mutateDestinationLabel(child.LabelLineage, n.Config.Dst, LabelLineageMutated)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "label_replace can lower to native SQL by mutating child labelsets with RE2-compatible capture replacement while preserving sample values"
			info.SubtreeShape = SubtreeShapeLabelTransform
			info.DropsMetric = false
			info.OutputKind = child.OutputKind
			// Forward the child's SourceExpr so aggregation pushdown can
			// capture SourcePromQL / ValueExpr / TagsExpr / DropsMetric
			// the same way the pre-retirement LabelTransformFragment did.
			if child.SourceExpr != nil {
				view := *child.SourceExpr
				info.SourceExpr = &view
			}
			return info
		}
		info.NativeReason = "label_replace currently requires a lowerable instant-vector child to run fully in native SQL"
		return info
	case *logicalpkg.LabelJoinPlan:
		child := a.walk(n.Child)
		info.NodeType = "label_join"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = mutateDestinationLabel(child.LabelLineage, n.Config.Dst, LabelLineageSynthetic)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.NativeLowerable && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "label_join can lower to native SQL by concatenating source label values on the child labelset while preserving sample values"
			info.SubtreeShape = SubtreeShapeLabelTransform
			info.DropsMetric = false
			info.OutputKind = child.OutputKind
			if child.SourceExpr != nil {
				view := *child.SourceExpr
				info.SourceExpr = &view
			}
			return info
		}
		info.NativeReason = "label_join currently requires a lowerable instant-vector child to run fully in native SQL"
		return info
	default:
		info.NodeType = fmt.Sprintf("%T", node)
		info.LabelLineage = unknownLineage()
		info.NativeReason = fmt.Sprintf("no native analysis rule is registered for %T", node)
		return info
	}
}

// populateValueTransform threads a valueTransformResult onto the given
// info, including the SubtreeShape/DropsMetric/OutputKind/ValueTransform/
// RuntimeValueTransform/LabelLineage side-map fields that tier-2/3 readers
// consume. `vectorChildOnLeft` identifies which BinaryPlan arm carries
// the non-scalar child whose SQL is wrapped by the transform (true when
// the vector child is n.LHS, false when it is n.RHS).
func populateValueTransform(info *LoweringInfo, result valueTransformResult, vectorChildOnLeft bool, shape SubtreeShape) {
	info.SubtreeShape = shape
	info.OutputKind = result.OutputKind
	info.DropsMetric = result.DropsMetric
	info.LabelLineage = result.Lineage
	view := result.View
	view.VectorChildOnLeft = vectorChildOnLeft
	info.ValueTransform = &view
	info.RuntimeValueTransform = result.Runtime
}

// computeSelectorShape derives the logical-side SelectorShape record
// for a node from the just-walked info and the node's structure. The
// shape mirrors (and is populated in parallel with) the selector-derived
// fields tier-2 helpers currently read via fragment.Selector /
// fragment.*.Child recursion. Later tasks (13a-d) switch consumers onto
// InfoFor(n).Shape; the Fragment fields stay authoritative until then.
func computeSelectorShape(a *Analysis, node logicalpkg.Node, info *LoweringInfo) SelectorShape {
	shape := SelectorShape{}
	if node == nil || info == nil {
		return shape
	}

	// Leaves carry the base selector directly.
	if leaf, ok := node.(*logicalpkg.LeafExprPlan); ok && leaf != nil {
		switch sel := leaf.Expr.(type) {
		case *parser.VectorSelector:
			shape.HasSelector = true
			shape.SelectorKind = SelectorKindInstantVector
			shape.SelectorLookback = DefaultInstantSelectorLookback
			shape.SelectorOffset = sel.OriginalOffset
			shape.SelectorTimestamp = cloneInt64Pointer(sel.Timestamp)
			shape.SelectorStartOrEnd = sel.StartOrEnd
			shape.OutputHasMetricName = selectorShapeOutputHasMetricName(info)
			shape.HasFixedTemporalAnchor = selectorShapeHasFixedAnchor(shape)
			return shape
		case *parser.MatrixSelector:
			vec, ok := sel.VectorSelector.(*parser.VectorSelector)
			if !ok {
				return shape
			}
			shape.HasSelector = true
			shape.SelectorKind = SelectorKindRangeVector
			shape.SelectorLookback = sel.Range
			shape.SelectorOffset = vec.OriginalOffset
			shape.SelectorTimestamp = cloneInt64Pointer(vec.Timestamp)
			shape.SelectorStartOrEnd = vec.StartOrEnd
			shape.OutputHasMetricName = selectorShapeOutputHasMetricName(info)
			shape.HasFixedTemporalAnchor = selectorShapeHasFixedAnchor(shape)
			return shape
		}
	}

	// Synthetic-series nodes (scalar literals, VectorPlan over a scalar
	// literal, ScalarBuiltinPlan, synthetic PointwiseFunctionPlan) carry
	// no base selector. Leave shape zero.
	if info.SubtreeShape == SubtreeShapeSyntheticSeries {
		return shape
	}

	// For composite nodes, inherit the base selector shape from the
	// descendant via the side-map. The chosen descendant matches the
	// fragment-side BaseSelectorSource/HasFixedTemporalAnchor walk
	// (first selector-carrying child wins for Kind/Lookback/Offset;
	// any descendant with a fixed anchor propagates).
	switch n := node.(type) {
	case *logicalpkg.SubqueryPlan:
		childShape := childSelectorShape(a, n.Child)
		shape = childShape
		// Subquery itself can pin evaluation via Timestamp/StartOrEnd.
		if n.Timestamp != nil || n.StartOrEnd == parser.START || n.StartOrEnd == parser.END {
			shape.HasFixedTemporalAnchor = true
		}
	case *logicalpkg.AggregationPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.RangeFunctionPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.RatePlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.IncreasePlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.DeltaPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.ChangesPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.DerivPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.QuantileOverTimePlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.ScalarConvertPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.InfoPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.AbsentPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.AbsentOverTimePlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.HistogramProjectionPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.HistogramQuantilePlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.HistogramFractionPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.HistogramQuantilesPlan:
		shape = childSelectorShape(a, n.Child)
		if !shape.HasSelector {
			for _, q := range n.ParamChildren {
				if cs := childSelectorShape(a, q); cs.HasSelector {
					shape = cs
					break
				}
			}
		}
		// Fixed anchor propagates across any descendant even if the
		// base selector came from the main child.
		for _, q := range n.ParamChildren {
			if cs := childSelectorShape(a, q); cs.HasFixedTemporalAnchor {
				shape.HasFixedTemporalAnchor = true
				break
			}
		}
	case *logicalpkg.SortPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.RoundPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.LabelReplacePlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.LabelJoinPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.PointwiseFunctionPlan:
		shape = childSelectorShape(a, n.Child)
		for _, q := range n.ParamChildren {
			if cs := childSelectorShape(a, q); cs.HasFixedTemporalAnchor {
				shape.HasFixedTemporalAnchor = true
			}
			if !shape.HasSelector {
				if cs := childSelectorShape(a, q); cs.HasSelector {
					shape = cs
				}
			}
		}
	case *logicalpkg.UnaryPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.VectorPlan:
		shape = childSelectorShape(a, n.Child)
	case *logicalpkg.BinaryPlan:
		lhs := childSelectorShape(a, n.LHS)
		rhs := childSelectorShape(a, n.RHS)
		switch {
		case lhs.HasSelector:
			shape = lhs
		case rhs.HasSelector:
			shape = rhs
		}
		if lhs.HasFixedTemporalAnchor || rhs.HasFixedTemporalAnchor {
			shape.HasFixedTemporalAnchor = true
		}
	}

	// Refresh OutputHasMetricName from the (possibly wrapped) fragment
	// so it reflects the current narrowing state. Only meaningful when
	// we successfully carried through a base selector.
	if shape.HasSelector {
		shape.OutputHasMetricName = selectorShapeOutputHasMetricNameInherited(info, shape)
	}
	return shape
}

func childSelectorShape(a *Analysis, child logicalpkg.Node) SelectorShape {
	info := a.InfoFor(child)
	if info == nil {
		return SelectorShape{}
	}
	return info.Shape
}

// selectorShapeOutputHasMetricName mirrors
// renderer.selectorOutputHasMetricName for the base-selector case (leaf
// with a direct info.LeafSelector). A nil info/selector defaults to true
// (selector-less paths render full tags).
func selectorShapeOutputHasMetricName(info *LoweringInfo) bool {
	if info == nil || info.LeafSelector == nil {
		return true
	}
	sel := info.LeafSelector
	if sel.RequireFullTags {
		return true
	}
	if len(sel.RequiredTagLabels) == 0 {
		return false
	}
	for _, label := range sel.RequiredTagLabels {
		if label == "__name__" {
			return true
		}
	}
	return false
}

// selectorShapeOutputHasMetricNameInherited determines OutputHasMetricName
// for composite nodes. When the composite's info still carries a selector
// pointer (via cloneSelectorSource in unary/binary source expressions via
// SourceExpr.Selector), we prefer its narrowing state. Otherwise we fall
// back to the child's computed value so ancestors without a direct
// selector pointer still reflect the chain's narrowing.
func selectorShapeOutputHasMetricNameInherited(info *LoweringInfo, childShape SelectorShape) bool {
	if info != nil && info.SourceExpr != nil && info.SourceExpr.Selector != nil {
		return selectorShapeOutputHasMetricNameFromSelector(info.SourceExpr.Selector)
	}
	if info != nil && info.LeafSelector != nil {
		return selectorShapeOutputHasMetricName(info)
	}
	if !childShape.HasSelector {
		return true
	}
	return childShape.OutputHasMetricName
}

func selectorShapeOutputHasMetricNameFromSelector(sel *SelectorSource) bool {
	if sel == nil {
		return true
	}
	if sel.RequireFullTags {
		return true
	}
	if len(sel.RequiredTagLabels) == 0 {
		return false
	}
	for _, label := range sel.RequiredTagLabels {
		if label == "__name__" {
			return true
		}
	}
	return false
}

func selectorShapeHasFixedAnchor(shape SelectorShape) bool {
	if !shape.HasSelector {
		return false
	}
	if shape.SelectorTimestamp != nil {
		return true
	}
	return shape.SelectorStartOrEnd == parser.START || shape.SelectorStartOrEnd == parser.END
}

func isClampPointwiseFunction(name string) bool {
	switch name {
	case "clamp", "clamp_min", "clamp_max":
		return true
	default:
		return false
	}
}

// applyNativeClamp populates info with the ClampTransform lowering
// outcome for a clamp/clamp_min/clamp_max PointwiseFunctionPlan. It
// reports whether the shape is lowerable. When ok, info.SubtreeShape,
// DropsMetric, and OutputKind are populated.
func applyNativeClamp(info *LoweringInfo, name string, child *LoweringInfo, params []*LoweringInfo) bool {
	if child == nil || !child.NativeLowerable || child.OutputKind != OutputKindInstantVector {
		return false
	}
	switch name {
	case "clamp":
		if len(params) != 2 || params[0] == nil || params[1] == nil ||
			!params[0].NativeLowerable || !params[1].NativeLowerable ||
			params[0].OutputKind != OutputKindScalar || params[1].OutputKind != OutputKindScalar {
			return false
		}
	case "clamp_min":
		if len(params) != 1 || params[0] == nil || !params[0].NativeLowerable || params[0].OutputKind != OutputKindScalar {
			return false
		}
	case "clamp_max":
		if len(params) != 1 || params[0] == nil || !params[0].NativeLowerable || params[0].OutputKind != OutputKindScalar {
			return false
		}
	default:
		return false
	}
	info.SubtreeShape = SubtreeShapeClampTransform
	info.DropsMetric = true
	info.OutputKind = child.OutputKind
	return true
}

func isVectorZeroLogicalPlan(node logicalpkg.Node) bool {
	vector, ok := node.(*logicalpkg.VectorPlan)
	if !ok || vector == nil {
		return false
	}
	scalar, ok := vector.Child.(*logicalpkg.ScalarLiteralPlan)
	if !ok || scalar == nil {
		return false
	}
	return scalar.Value == 0
}

func zeroFillAggregationSource(op parser.ItemType, grouping []string, without bool, child logicalpkg.Node, analysis *Analysis) (*LoweringInfo, bool) {
	if op != parser.SUM || without || len(grouping) != 0 {
		return nil, false
	}
	binary, ok := child.(*logicalpkg.BinaryPlan)
	if !ok || binary == nil || binary.Op != parser.LOR || binary.ReturnBool {
		return nil, false
	}
	switch {
	case isVectorZeroLogicalPlan(binary.LHS):
		info := analysis.InfoFor(binary.RHS)
		if info != nil && info.NativeLowerable && info.OutputKind == OutputKindInstantVector {
			return info, true
		}
	case isVectorZeroLogicalPlan(binary.RHS):
		info := analysis.InfoFor(binary.LHS)
		if info != nil && info.NativeLowerable && info.OutputKind == OutputKindInstantVector {
			return info, true
		}
	}
	return nil, false
}

// isSupportedNativeRangeChild reports whether the child info is a
// valid input to a range-function lowering: a native-lowerable
// range-matrix shape whose SubtreeShape is either a leaf source or a
// subquery.
func isSupportedNativeRangeChild(child *LoweringInfo) bool {
	if child == nil || !child.NativeLowerable || child.OutputKind != OutputKindRangeMatrix {
		return false
	}
	switch child.SubtreeShape {
	case SubtreeShapeLeafSource, SubtreeShapeSubquery:
		return true
	default:
		return false
	}
}

// isSupportedNativeSubqueryChild reports whether the child info is a
// valid input to a subquery step-grid lowering.
func isSupportedNativeSubqueryChild(child *LoweringInfo) bool {
	if child == nil || !child.NativeLowerable {
		return false
	}
	switch child.SubtreeShape {
	case SubtreeShapeLeafSource, SubtreeShapeUnarySourceExpr, SubtreeShapeBinaryScalarSourceExpr,
		SubtreeShapeAggregation, SubtreeShapeBinaryVectorJoin, SubtreeShapeRangeFunction,
		SubtreeShapeSortTransform, SubtreeShapeLabelTransform, SubtreeShapeClampTransform,
		SubtreeShapeValueTransform:
		return true
	default:
		return false
	}
}

// isSupportedAggregationSourceInfo reports whether the info's
// SubtreeShape marks it as a valid pointwise-source-template input.
func isSupportedAggregationSourceInfo(info *LoweringInfo) bool {
	if info == nil || !info.NativeLowerable {
		return false
	}
	switch info.SubtreeShape {
	case SubtreeShapeLeafSource, SubtreeShapeUnarySourceExpr, SubtreeShapeBinaryScalarSourceExpr, SubtreeShapeAggregation:
		return true
	default:
		return false
	}
}

func describeLogicalPlan(node logicalpkg.Node) (string, OutputKind) {
	described, ok := node.(describedLogicalPlan)
	if !ok {
		return "", OutputKindUnknown
	}
	return described.ExprString(), outputKindForValueType(described.ValueType())
}

func unknownLineage() LabelLineage {
	return LabelLineage{
		Known:      map[string]LabelLineageState{"__name__": LabelLineageUnknown},
		Wildcard:   LabelLineageUnknown,
		MetricName: LabelLineageUnknown,
	}
}

func boolState(condition bool, ifTrue, ifFalse LabelLineageState) LabelLineageState {
	if condition {
		return ifTrue
	}
	return ifFalse
}

func sortedKeys(values map[string]LabelLineageState) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// rangeFunctionSubqueryChild returns the subquery context view carried
// by the child info when the child is a subquery. Populated onto
// LoweringInfo.RangeFunctionSubquery at each of the seven range-function
// Analyze emission sites.
//
// After NativeFragment retirement we lower via the logical tree, so no
// Fragment subquery payload is attached to the info side-map; instead,
// range helpers look up the child's SubtreeShape and, when it is a
// subquery, walk the logical tree to recover the range/step/offset
// parameters. This helper therefore returns nil today — the branches
// that previously depended on the subquery payload (range_logical.go)
// now drive directly off the child logical node. Retained as a no-op
// to keep emission sites uniform; planner tests assert presence via
// SubtreeShape rather than pointer identity.
func rangeFunctionSubqueryChild(child *LoweringInfo) *SubqueryFragment {
	if child == nil || child.SubtreeShape != SubtreeShapeSubquery {
		return nil
	}
	// Sentinel non-nil value is sufficient; readers cover per-query
	// subquery context via the logical tree now. A zero-valued
	// SubqueryFragment is enough to signal "subquery child present".
	return &SubqueryFragment{}
}

func (info *LoweringInfo) String() string {
	if info == nil {
		return ""
	}
	parts := []string{info.NodeType, info.Expr}
	if info.NativeReason != "" {
		parts = append(parts, info.NativeReason)
	}
	if len(info.LabelLineage.Known) > 0 {
		labels := make([]string, 0, len(info.LabelLineage.Known))
		for _, key := range sortedKeys(info.LabelLineage.Known) {
			labels = append(labels, fmt.Sprintf("%s=%s", key, info.LabelLineage.Known[key]))
		}
		parts = append(parts, strings.Join(labels, ","))
	}
	return strings.Join(parts, " | ")
}

// populateSelectorInferredAndPushedMatchers pre-computes the
// InferredMatchers and PushedMatchers slices on selector at Analyze time.
// Historically these were produced by the optimizer's
// CommonMatcherInference and LabelPredicatePushdown passes, which mutated
// a cloned SelectorSource. Task 13c-9 lifts that work into Analyze so the
// enriched matchers are already visible through info.LeafSelector before
// any optimizer pass runs.
//
// A fresh matcherInterner scopes pointer-sharing to this single leaf so
// Matchers/InferredMatchers/PushedMatchers share pointers for equal
// matcher keys — matching the optimizer's historical interning behavior.
func populateSelectorInferredAndPushedMatchers(selector *SelectorSource) {
	if selector == nil {
		return
	}
	interner := newMatcherInterner()
	selector.Matchers = interner.internSlice(selector.Matchers)
	selector.InferredMatchers = interner.internSlice(inferSourceMatchers(selector))
	selector.PushedMatchers = mergeMatchers(interner, selector.Matchers, selector.InferredMatchers)
}
