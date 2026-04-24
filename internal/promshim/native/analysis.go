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
			info.NativeReason = "selector leaf can seed repo-owned native SQL source lowering"
			info.Fragment = &NativeFragment{
				Kind:         FragmentKindLeafSource,
				OutputKind:   outputKind,
				SourcePromQL: n.Expr,
				Selector:     selector,
				ValueExpr:    "{value}",
				TagsExpr:     "{tags}",
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
		info.Fragment = &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindScalar, DropsMetric: true, Synthetic: &SyntheticSeriesFragment{Func: "literal", Value: cloneFloat64Pointer(&n.Value)}}
		return info
	case *logicalpkg.UnaryPlan:
		child := a.walk(n.Child)
		info.NodeType = "unary"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if literal, ok := syntheticLiteralValue(child.Fragment); ok {
			if folded, ok := foldUnaryScalarLiteral(n.Op, literal); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-only unary expression can fold to a native synthetic scalar series"
				info.Fragment = &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindScalar, DropsMetric: true, Synthetic: &SyntheticSeriesFragment{Func: "literal", Value: cloneFloat64Pointer(&folded)}}
				return info
			}
		}
		if child.Fragment != nil {
			valueExpr, dropsMetric, ok := applyUnarySourceTransform(n.Op, child.Fragment.ValueExpr, child.Fragment.DropsMetric)
			if ok {
				if dropsMetric {
					info.LabelLineage = withMetricNameState(info.LabelLineage, LabelLineageDropped)
				}
				info.NativeLowerable = true
				if child.Fragment.Selector != nil || child.Fragment.SourcePromQL != nil {
					info.NativeReason = "unary transform can be applied inside a native SQL source expression"
					info.Fragment = &NativeFragment{
						Kind:         FragmentKindUnarySourceExpr,
						OutputKind:   child.Fragment.OutputKind,
						SourcePromQL: child.Fragment.SourcePromQL,
						Selector:     cloneSelectorSource(child.Fragment.Selector),
						ValueExpr:    valueExpr,
						TagsExpr:     tagsExprForMetricDrop(dropsMetric),
						DropsMetric:  dropsMetric,
					}
				} else if frag, lineage, ok := applyUnaryValueTransform(n.Op, child.Fragment, child.LabelLineage); ok {
					info.NativeReason = "unary transform lowers via a native value-transform wrapper"
					info.LabelLineage = lineage
					info.Fragment = frag
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

		if lhsLiteral, ok := syntheticLiteralValue(lhs.Fragment); ok {
			if rhsLiteral, ok := syntheticLiteralValue(rhs.Fragment); ok {
				if folded, ok := foldBinaryScalarLiteral(n.Op, n.ReturnBool, lhsLiteral, rhsLiteral); ok {
					info.NativeLowerable = true
					info.NativeReason = "scalar-only expression can fold to a native synthetic scalar series"
					info.Fragment = &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindScalar, DropsMetric: true, Synthetic: &SyntheticSeriesFragment{Func: "literal", Value: cloneFloat64Pointer(&folded)}}
					return info
				}
			}
		}

		lhsScalar, lhsIsScalar := n.LHS.(*logicalpkg.ScalarLiteralPlan)
		rhsScalar, rhsIsScalar := n.RHS.(*logicalpkg.ScalarLiteralPlan)
		lhsLiteral, lhsLiteralOK := syntheticLiteralValue(lhs.Fragment)
		rhsLiteral, rhsLiteralOK := syntheticLiteralValue(rhs.Fragment)
		switch {
		case lhsLiteralOK && !lhsIsScalar && rhs.Fragment != nil && rhs.OutputKind == OutputKindInstantVector:
			if frag, lineage, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, rhs.Fragment, rhs.LabelLineage, lhsLiteral, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-expression/vector bool comparison lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
			if frag, lineage, ok := applyScalarValueTransform(n.Op, rhs.Fragment, rhs.LabelLineage, lhsLiteral, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-expression/vector arithmetic lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
			if frag, lineage, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, rhs.Fragment, rhs.LabelLineage, lhsLiteral, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-expression/vector comparison filters via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case rhsLiteralOK && !rhsIsScalar && lhs.Fragment != nil && lhs.OutputKind == OutputKindInstantVector:
			if frag, lineage, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, lhs.Fragment, lhs.LabelLineage, rhsLiteral, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/scalar-expression bool comparison lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
			if frag, lineage, ok := applyScalarValueTransform(n.Op, lhs.Fragment, lhs.LabelLineage, rhsLiteral, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/scalar-expression arithmetic lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
			if frag, lineage, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, lhs.Fragment, lhs.LabelLineage, rhsLiteral, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/scalar-expression comparison filters via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case lhsIsScalar && rhs.Fragment != nil:
			valueExpr, dropsMetric, ok := applyBinarySourceTransform(n.Op, rhs.Fragment.ValueExpr, lhsScalar.Value, true)
			if ok {
				info.NativeLowerable = true
				info.LabelLineage = withMetricNameState(passthroughLabelLineage(rhs.LabelLineage), boolState(dropsMetric, LabelLineageDropped, rhs.LabelLineage.MetricName))
				if rhs.Fragment.Selector != nil || rhs.Fragment.SourcePromQL != nil {
					info.NativeReason = "scalar-vector arithmetic can be applied inside a native SQL source expression"
					info.Fragment = &NativeFragment{
						Kind:         FragmentKindBinaryScalarSourceExpr,
						OutputKind:   rhs.Fragment.OutputKind,
						SourcePromQL: rhs.Fragment.SourcePromQL,
						Selector:     cloneSelectorSource(rhs.Fragment.Selector),
						ValueExpr:    valueExpr,
						TagsExpr:     tagsExprForMetricDrop(dropsMetric),
						DropsMetric:  dropsMetric,
					}
				} else if frag, lineage, ok := applyScalarValueTransform(n.Op, rhs.Fragment, rhs.LabelLineage, lhsScalar.Value, true); ok {
					info.NativeReason = "scalar-vector arithmetic lowers via a native value-transform wrapper"
					info.LabelLineage = lineage
					info.Fragment = frag
				}
				return info
			}
			if frag, lineage, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, rhs.Fragment, rhs.LabelLineage, lhsScalar.Value, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-vector bool comparison lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
			if frag, lineage, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, rhs.Fragment, rhs.LabelLineage, lhsScalar.Value, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-vector comparison filters via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case rhsIsScalar && lhs.Fragment != nil:
			valueExpr, dropsMetric, ok := applyBinarySourceTransform(n.Op, lhs.Fragment.ValueExpr, rhsScalar.Value, false)
			if ok {
				info.NativeLowerable = true
				info.LabelLineage = withMetricNameState(passthroughLabelLineage(lhs.LabelLineage), boolState(dropsMetric, LabelLineageDropped, lhs.LabelLineage.MetricName))
				if lhs.Fragment.Selector != nil || lhs.Fragment.SourcePromQL != nil {
					info.NativeReason = "vector-scalar arithmetic can be applied inside a native SQL source expression"
					info.Fragment = &NativeFragment{
						Kind:         FragmentKindBinaryScalarSourceExpr,
						OutputKind:   lhs.Fragment.OutputKind,
						SourcePromQL: lhs.Fragment.SourcePromQL,
						Selector:     cloneSelectorSource(lhs.Fragment.Selector),
						ValueExpr:    valueExpr,
						TagsExpr:     tagsExprForMetricDrop(dropsMetric),
						DropsMetric:  dropsMetric,
					}
				} else if frag, lineage, ok := applyScalarValueTransform(n.Op, lhs.Fragment, lhs.LabelLineage, rhsScalar.Value, false); ok {
					info.NativeReason = "vector-scalar arithmetic lowers via a native value-transform wrapper"
					info.LabelLineage = lineage
					info.Fragment = frag
				}
				return info
			}
			if frag, lineage, ok := applyComparisonBoolTransform(n.Op, n.ReturnBool, lhs.Fragment, lhs.LabelLineage, rhsScalar.Value, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector-scalar bool comparison lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
			if frag, lineage, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, lhs.Fragment, lhs.LabelLineage, rhsScalar.Value, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector-scalar comparison filters via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case isSyntheticScalarFragment(lhs.Fragment) && rhs.Fragment != nil:
			if frag, lineage, ok := applySyntheticScalarChildTransform(n.Op, n.ReturnBool, lhs.Fragment.Synthetic.Func, rhs.Fragment, rhs.LabelLineage, true); ok {
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
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case isSyntheticScalarFragment(rhs.Fragment) && lhs.Fragment != nil:
			if frag, lineage, ok := applySyntheticScalarChildTransform(n.Op, n.ReturnBool, rhs.Fragment.Synthetic.Func, lhs.Fragment, lhs.LabelLineage, false); ok {
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
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case lhs.Fragment != nil && rhs.Fragment != nil && lhs.OutputKind == OutputKindInstantVector && rhs.OutputKind == OutputKindInstantVector:
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
				info.Fragment = &NativeFragment{
					Kind:        FragmentKindBinaryVectorJoin,
					OutputKind:  outputKind,
					DropsMetric: dropsMetric,
					BinaryJoin: &BinaryJoinFragment{
						Op:             n.Op,
						ReturnBool:     n.ReturnBool,
						VectorMatching: CloneVectorMatching(n.VectorMatching),
						JoinShape:      joinShape,
						LHS:            lhs.Fragment,
						RHS:            rhs.Fragment,
					},
				}
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
		sourceFragment := child.Fragment
		emitZeroOnEmpty := false
		if zeroFillSource, ok := zeroFillAggregationSource(n.Op, n.Grouping, n.Without, n.Child, a); ok {
			sourceFragment = zeroFillSource
			emitZeroOnEmpty = true
			reason = "sum(... or vector(0)) can lower by aggregating the native child and emitting zero when the aggregate would otherwise be empty"
		} else if sourceFragment == nil || child.OutputKind != OutputKindInstantVector {
			sourceFragment = nil
		}
		eligible := true
		if !isSupportedNativeAggregation(n.Op) {
			eligible = false
			reason = "aggregation operator is not supported by native SQL pushdown"
		} else if sourceFragment == nil {
			eligible = false
			reason = "aggregation child is not pushdown-safe; native pushdown currently requires a native-lowerable instant-vector child"
		}
		info.Aggregation = &AggregationSupport{Eligible: eligible, Reason: reason, Source: sourceFragment}
		if eligible {
			info.Fragment = &NativeFragment{
				Kind:       FragmentKindAggregation,
				OutputKind: outputKind,
				Aggregation: &AggregationFragment{
					Op:              n.Op,
					Grouping:        append([]string(nil), n.Grouping...),
					Without:         n.Without,
					ParamNumber:     cloneFloat64Pointer(n.ParamNumber),
					ParamString:     n.ParamString,
					Source:          sourceFragment,
					EmitZeroOnEmpty: emitZeroOnEmpty,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "histogram_quantile can lower to native SQL over shared classic histogram materialization"
			info.Fragment = &NativeFragment{Kind: FragmentKindHistogramFunction, OutputKind: OutputKindInstantVector, DropsMetric: true, HistogramFunction: &HistogramFunctionFragment{Func: "histogram_quantile", Quantile: cloneFloat64Pointer(&n.Quantile), Child: child.Fragment}}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "histogram_fraction can lower to native SQL over shared classic histogram materialization"
			info.Fragment = &NativeFragment{Kind: FragmentKindHistogramFunction, OutputKind: OutputKindInstantVector, DropsMetric: true, HistogramFunction: &HistogramFunctionFragment{Func: "histogram_fraction", Lower: cloneFloat64Pointer(&n.Lower), Upper: cloneFloat64Pointer(&n.Upper), Child: child.Fragment}}
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
		if (n.Func == "histogram_count" || n.Func == "histogram_sum" || n.Func == "histogram_avg" || n.Func == "histogram_stddev" || n.Func == "histogram_stdvar") && child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL over shared classic histogram materialization", n.Func)
			info.Fragment = &NativeFragment{
				Kind:                FragmentKindHistogramProjection,
				OutputKind:          OutputKindInstantVector,
				DropsMetric:         true,
				HistogramProjection: &HistogramProjectionFragment{Func: n.Func, Child: child.Fragment},
			}
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
		quantileFragments := make([]*NativeFragment, 0, len(n.ParamChildren))
		lowerable := child != nil && child.Fragment != nil && child.OutputKind == OutputKindInstantVector
		for _, paramChild := range n.ParamChildren {
			paramInfo := a.walk(paramChild)
			children = append(children, paramInfo)
			info.TimeRequirements = combineTimeRequirements(info.TimeRequirements, paramInfo.TimeRequirements)
			if paramInfo == nil || paramInfo.Fragment == nil || paramInfo.OutputKind != OutputKindScalar {
				lowerable = false
				continue
			}
			quantileFragments = append(quantileFragments, paramInfo.Fragment)
		}
		info.Children = children
		if lowerable {
			info.NativeLowerable = true
			info.NativeReason = "histogram_quantiles can lower to native SQL over shared classic histogram materialization plus scalar quantile argument bindings"
			info.Fragment = &NativeFragment{Kind: FragmentKindHistogramFunction, OutputKind: OutputKindInstantVector, DropsMetric: true, HistogramFunction: &HistogramFunctionFragment{Func: "histogram_quantiles", Label: n.Label, Quantiles: quantileFragments, Child: child.Fragment}}
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
		if isSupportedNativeAggregateOverTime(n.Func) && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("range function %q can lower to native SQL for the first aggregate-over-time subset", n.Func)
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: !preservesName,
				RangeFunction: &RangeFunctionFragment{
					Func:        n.Func,
					ParamNumber: cloneFloat64Pointer(n.ParamNumber),
					Child:       child.Fragment,
				},
			}
			return info
		}
		if n.Func == "predict_linear" && n.ParamNumber != nil && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "predict_linear can lower to native SQL using the shared windowed-arrays source and simpleLinearRegression"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:        n.Func,
					ParamNumber: cloneFloat64Pointer(n.ParamNumber),
					Child:       child.Fragment,
				},
			}
			return info
		}
		if n.Func == "resets" && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "resets can lower to native SQL using pairwise comparisons over the shared windowed-arrays source"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:        n.Func,
					ParamNumber: cloneFloat64Pointer(n.ParamNumber),
					Child:       child.Fragment,
				},
			}
			return info
		}
		if (n.Func == "double_exponential_smoothing" || n.Func == "holt_winters") && len(n.ParamNumbers) == 2 && n.ParamNumbers[0] != nil && n.ParamNumbers[1] != nil && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL using arrayFold over the shared windowed-arrays source", n.Func)
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:         n.Func,
					ParamNumbers: cloneFloat64Pointers(n.ParamNumbers),
					Child:        child.Fragment,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindScalar {
			fragment := CloneFragment(child.Fragment)
			fragment.OutputKind = OutputKindInstantVector
			info.NativeLowerable = true
			info.NativeReason = "vector() can lower by lifting a native scalar child into a single-series instant vector"
			info.Fragment = fragment
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL by ordering the instant-vector child while preserving range-mode passthrough semantics", n.Func)
			info.Fragment = &NativeFragment{Kind: FragmentKindSortTransform, OutputKind: child.Fragment.OutputKind, SortTransform: &SortTransformFragment{Func: n.Func, Labels: append([]string(nil), n.Labels...), Child: child.Fragment}}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "scalar() can lower to native SQL by counting child vector rows"
			info.Fragment = &NativeFragment{Kind: FragmentKindScalarConvert, OutputKind: OutputKindScalar, DropsMetric: true, ScalarConvert: &ScalarConvertFragment{Child: child.Fragment}}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			metricName, selectorMatchers := nativeInfoSelector(n.SelectorMatchers)
			info.NativeLowerable = true
			info.NativeReason = "info() can lower to native SQL using native selector matching plus merged info-series label joins"
			info.Fragment = &NativeFragment{Kind: FragmentKindInfoJoin, OutputKind: OutputKindInstantVector, InfoJoin: &InfoJoinFragment{Child: child.Fragment, InfoMetricName: metricName, SelectorMatchers: selectorMatchers, CopyLabelNames: infoJoinCopyLabelNames(n.SelectorMatchers), DropUnmatched: infoJoinDropUnmatched(n.SelectorMatchers)}}
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
				if frag, ok := buildNativeClampFragment(n.Func, child, children[1:]); ok {
					info.NativeLowerable = true
					info.NativeReason = fmt.Sprintf("%s can lower to native SQL by joining scalar bound fragments onto the vector child", n.Func)
					info.Fragment = frag
					return info
				}
			}
			if template, ok := NativePointwiseSourceTemplate(n.Func, n.ParamNumbers); ok && child.Fragment != nil && isSupportedAggregationSourceFragment(child.Fragment) {
				info.NativeLowerable = true
				info.NativeReason = fmt.Sprintf("%s can lower to a native SQL source expression", n.Func)
				info.Fragment = &NativeFragment{Kind: FragmentKindUnarySourceExpr, OutputKind: child.Fragment.OutputKind, SourcePromQL: child.Fragment.SourcePromQL, Selector: cloneSelectorSource(child.Fragment.Selector), ValueExpr: composePointwiseSourceTemplate(template, child.Fragment.ValueExpr), TagsExpr: tagsExprForMetricDrop(true), DropsMetric: true}
				return info
			}
		} else if isSupportedNativeSyntheticDateFunction(n.Func) {
			info.LabelLineage = syntheticOutputLineage(map[string]string{})
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to a native synthetic series", n.Func)
			info.Fragment = &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindInstantVector, DropsMetric: true, Synthetic: &SyntheticSeriesFragment{Func: n.Func}}
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
			info.Fragment = &NativeFragment{Kind: FragmentKindSyntheticSeries, OutputKind: OutputKindScalar, DropsMetric: true, Synthetic: &SyntheticSeriesFragment{Func: n.Func}}
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
		if frag, lineage, ok := applyRoundValueTransform(child.Fragment, child.LabelLineage, toNearest); ok {
			info.NativeLowerable = true
			info.NativeReason = "round() can lower via a native value-transform wrapper over a native instant-vector child"
			info.LabelLineage = lineage
			info.Fragment = frag
			return info
		}
		info.NativeReason = "round() currently stays on the local execution path"
		return info
	case *logicalpkg.RatePlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if isSupportedNativeCounterRangeFunction(n.Func) && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL for the initial counter/range subset", n.Func)
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:  n.Func,
					Child: child.Fragment,
				},
			}
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
		if isSupportedNativeCounterRangeFunction("increase") && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "increase can lower to native SQL for the initial counter/range subset"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:  "increase",
					Child: child.Fragment,
				},
			}
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
		if isSupportedNativeCounterRangeFunction(n.Func) && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = fmt.Sprintf("%s can lower to native SQL for the initial counter/range subset", n.Func)
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:  n.Func,
					Child: child.Fragment,
				},
			}
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
		if isSupportedNativeCounterRangeFunction("changes") && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "changes can lower to native SQL for the initial counter/range subset"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:  "changes",
					Child: child.Fragment,
				},
			}
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
		if isSupportedNativeCounterRangeFunction("deriv") && child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "deriv can lower to native SQL for the initial counter/range subset"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:  "deriv",
					Child: child.Fragment,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "quantile_over_time can lower to native SQL using the shared windowed-arrays source and Prometheus-compatible quantile interpolation"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindRangeFunction,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				RangeFunction: &RangeFunctionFragment{
					Func:        "quantile_over_time",
					ParamNumber: cloneFloat64Pointer(&n.Quantile),
					Child:       child.Fragment,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "absent() can lower to native SQL by testing whether the lowerable child instant vector returns any rows"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindAbsent,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				Absent: &AbsentFragment{
					Func:         "absent",
					OutputMetric: cloneStringMap(n.OutputMetric),
					Child:        child.Fragment,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindRangeMatrix && isSupportedNativeRangeChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "absent_over_time can lower to native SQL by testing whether the lowerable range child window contains any samples"
			info.Fragment = &NativeFragment{
				Kind:        FragmentKindAbsent,
				OutputKind:  OutputKindInstantVector,
				DropsMetric: true,
				Absent: &AbsentFragment{
					Func:         "absent_over_time",
					OutputMetric: cloneStringMap(n.OutputMetric),
					Child:        child.Fragment,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector && isSupportedNativeSubqueryChildFragment(child.Fragment) {
			info.NativeLowerable = true
			info.NativeReason = "subquery step-grid execution can lower to native SQL for selector-backed instant-vector children"
			info.Fragment = &NativeFragment{
				Kind:       FragmentKindSubquery,
				OutputKind: OutputKindRangeMatrix,
				Subquery: &SubqueryFragment{
					Range:      n.Range,
					Step:       n.Step,
					Offset:     n.Offset,
					Timestamp:  cloneInt64Pointer(n.Timestamp),
					StartOrEnd: n.StartOrEnd,
					Child:      child.Fragment,
				},
			}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "label_replace can lower to native SQL by mutating child labelsets with RE2-compatible capture replacement while preserving sample values"
			info.Fragment = &NativeFragment{Kind: FragmentKindLabelTransform, OutputKind: child.Fragment.OutputKind, SourcePromQL: child.Fragment.SourcePromQL, LabelTransform: &LabelTransformFragment{Func: "label_replace", Dst: n.Config.Dst, Repl: n.Config.Repl, Regex: n.Config.Regex.String(), RegexSubexpNames: append([]string(nil), n.Config.Regex.SubexpNames()...), Src: n.Config.Src, Child: child.Fragment}}
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
		if child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "label_join can lower to native SQL by concatenating source label values on the child labelset while preserving sample values"
			info.Fragment = &NativeFragment{Kind: FragmentKindLabelTransform, OutputKind: child.Fragment.OutputKind, SourcePromQL: child.Fragment.SourcePromQL, LabelTransform: &LabelTransformFragment{Func: "label_join", Dst: n.Config.Dst, Separator: n.Config.Separator, SrcLabels: append([]string(nil), n.Config.SrcLabels...), Child: child.Fragment}}
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
			shape.OutputHasMetricName = selectorShapeOutputHasMetricName(info.Fragment)
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
			shape.OutputHasMetricName = selectorShapeOutputHasMetricName(info.Fragment)
			shape.HasFixedTemporalAnchor = selectorShapeHasFixedAnchor(shape)
			return shape
		}
	}

	// Synthetic-series nodes (scalar literals, VectorPlan over a scalar
	// literal, ScalarBuiltinPlan, synthetic PointwiseFunctionPlan) carry
	// no base selector. Leave shape zero.
	if info.Fragment != nil && info.Fragment.Kind == FragmentKindSyntheticSeries {
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
		shape.OutputHasMetricName = selectorShapeOutputHasMetricNameInherited(info.Fragment, shape)
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
// renderer.selectorOutputHasMetricName for the base-selector case
// (leaf with a direct fragment.Selector). A nil fragment/selector
// defaults to true (selector-less paths render full tags).
func selectorShapeOutputHasMetricName(fragment *NativeFragment) bool {
	if fragment == nil || fragment.Selector == nil {
		return true
	}
	sel := fragment.Selector
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
// for composite nodes. When the composite's fragment still carries a
// Selector pointer (via CloneSelectorSource in unary/binary source
// expressions), we prefer its narrowing state. Otherwise we fall back
// to the child's computed value so ancestors without a direct
// selector pointer still reflect the chain's narrowing.
func selectorShapeOutputHasMetricNameInherited(fragment *NativeFragment, childShape SelectorShape) bool {
	if fragment != nil && fragment.Selector != nil {
		return selectorShapeOutputHasMetricName(fragment)
	}
	if !childShape.HasSelector {
		return true
	}
	return childShape.OutputHasMetricName
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

func buildNativeClampFragment(name string, child *LoweringInfo, params []*LoweringInfo) (*NativeFragment, bool) {
	if child == nil || child.Fragment == nil || child.OutputKind != OutputKindInstantVector {
		return nil, false
	}
	var minFragment, maxFragment *NativeFragment
	switch name {
	case "clamp":
		if len(params) != 2 || params[0] == nil || params[1] == nil || params[0].Fragment == nil || params[1].Fragment == nil || params[0].OutputKind != OutputKindScalar || params[1].OutputKind != OutputKindScalar {
			return nil, false
		}
		minFragment = params[0].Fragment
		maxFragment = params[1].Fragment
	case "clamp_min":
		if len(params) != 1 || params[0] == nil || params[0].Fragment == nil || params[0].OutputKind != OutputKindScalar {
			return nil, false
		}
		minFragment = params[0].Fragment
	case "clamp_max":
		if len(params) != 1 || params[0] == nil || params[0].Fragment == nil || params[0].OutputKind != OutputKindScalar {
			return nil, false
		}
		maxFragment = params[0].Fragment
	default:
		return nil, false
	}
	return &NativeFragment{Kind: FragmentKindClampTransform, OutputKind: child.Fragment.OutputKind, DropsMetric: true, ClampTransform: &ClampTransformFragment{Func: name, Child: child.Fragment, Min: minFragment, Max: maxFragment}}, true
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

func zeroFillAggregationSource(op parser.ItemType, grouping []string, without bool, child logicalpkg.Node, analysis *Analysis) (*NativeFragment, bool) {
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
		if info != nil && info.Fragment != nil && info.OutputKind == OutputKindInstantVector {
			return info.Fragment, true
		}
	case isVectorZeroLogicalPlan(binary.RHS):
		info := analysis.InfoFor(binary.LHS)
		if info != nil && info.Fragment != nil && info.OutputKind == OutputKindInstantVector {
			return info.Fragment, true
		}
	}
	return nil, false
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
