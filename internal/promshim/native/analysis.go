package native

import (
	"fmt"
	"sort"
	"strings"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"

	"github.com/prometheus/prometheus/promql/parser"
)

type describedLogicalPlan interface {
	ExprString() string
	ValueType() parser.ValueType
}

func (a *Analysis) walk(node planpkg.LogicalPlan) *LoweringInfo {
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
	case *planpkg.LogicalLeafExprPlan:
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
	case *planpkg.LogicalScalarLiteralPlan:
		info.NodeType = "scalar"
		info.NativeReason = "scalar literal can participate as a constant in native source expressions but is not a standalone native fragment yet"
		return info
	case *planpkg.LogicalUnaryPlan:
		child := a.walk(n.Child)
		info.NodeType = "unary"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if child.Fragment != nil {
			valueExpr, dropsMetric, ok := applyUnarySourceTransform(n.Op, child.Fragment.ValueExpr, child.Fragment.DropsMetric)
			if ok {
				if dropsMetric {
					info.LabelLineage = withMetricNameState(info.LabelLineage, LabelLineageDropped)
				}
				info.NativeLowerable = true
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
				return info
			}
		}
		info.NativeReason = fmt.Sprintf("unary operator %q is not part of the current native source subset", n.Op.String())
		return info
	case *planpkg.LogicalBinaryPlan:
		lhs := a.walk(n.LHS)
		rhs := a.walk(n.RHS)
		info.NodeType = "binary"
		info.Children = []*LoweringInfo{lhs, rhs}
		info.TimeRequirements = combineTimeRequirements(lhs.TimeRequirements, rhs.TimeRequirements)
		info.LabelLineage = unknownLineage()

		lhsScalar, lhsIsScalar := n.LHS.(*planpkg.LogicalScalarLiteralPlan)
		rhsScalar, rhsIsScalar := n.RHS.(*planpkg.LogicalScalarLiteralPlan)
		switch {
		case lhsIsScalar && rhs.Fragment != nil:
			valueExpr, dropsMetric, ok := applyBinarySourceTransform(n.Op, rhs.Fragment.ValueExpr, lhsScalar.Value, true)
			if ok {
				info.NativeLowerable = true
				info.NativeReason = "scalar-vector arithmetic can be applied inside a native SQL source expression"
				info.LabelLineage = withMetricNameState(passthroughLabelLineage(rhs.LabelLineage), boolState(dropsMetric, LabelLineageDropped, rhs.LabelLineage.MetricName))
				info.Fragment = &NativeFragment{
					Kind:         FragmentKindBinaryScalarSourceExpr,
					OutputKind:   rhs.Fragment.OutputKind,
					SourcePromQL: rhs.Fragment.SourcePromQL,
					Selector:     cloneSelectorSource(rhs.Fragment.Selector),
					ValueExpr:    valueExpr,
					TagsExpr:     tagsExprForMetricDrop(dropsMetric),
					DropsMetric:  dropsMetric,
				}
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
				info.NativeReason = "vector-scalar arithmetic can be applied inside a native SQL source expression"
				info.LabelLineage = withMetricNameState(passthroughLabelLineage(lhs.LabelLineage), boolState(dropsMetric, LabelLineageDropped, lhs.LabelLineage.MetricName))
				info.Fragment = &NativeFragment{
					Kind:         FragmentKindBinaryScalarSourceExpr,
					OutputKind:   lhs.Fragment.OutputKind,
					SourcePromQL: lhs.Fragment.SourcePromQL,
					Selector:     cloneSelectorSource(lhs.Fragment.Selector),
					ValueExpr:    valueExpr,
					TagsExpr:     tagsExprForMetricDrop(dropsMetric),
					DropsMetric:  dropsMetric,
				}
				return info
			}
			if frag, lineage, ok := applyComparisonFilterTransform(n.Op, n.ReturnBool, lhs.Fragment, lhs.LabelLineage, rhsScalar.Value, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector-scalar comparison filters via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case isSyntheticScalarFragment(lhs.Fragment) && rhs.Fragment != nil && rhs.OutputKind == OutputKindInstantVector:
			if frag, lineage, ok := applySyntheticScalarVectorTransform(n.Op, lhs.Fragment.Synthetic.Func, rhs.Fragment, rhs.LabelLineage, true); ok {
				info.NativeLowerable = true
				info.NativeReason = "synthetic-scalar/vector arithmetic lowers via a native value-transform wrapper"
				info.LabelLineage = lineage
				info.Fragment = frag
				return info
			}
		case isSyntheticScalarFragment(rhs.Fragment) && lhs.Fragment != nil && lhs.OutputKind == OutputKindInstantVector:
			if frag, lineage, ok := applySyntheticScalarVectorTransform(n.Op, rhs.Fragment.Synthetic.Func, lhs.Fragment, lhs.LabelLineage, false); ok {
				info.NativeLowerable = true
				info.NativeReason = "vector/synthetic-scalar arithmetic lowers via a native value-transform wrapper"
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
						VectorMatching: cloneVectorMatching(n.VectorMatching),
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
	case *planpkg.LogicalAggregationPlan:
		child := a.walk(n.Child)
		info.NodeType = "aggregation"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = aggregationLabelLineage(child.LabelLineage, n.Grouping, n.Without)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)

		reason := "pushing aggregation into native ClickHouse SQL over a lowerable child avoids materializing the full child result in Go"
		eligible := true
		if !isSupportedNativeAggregation(n.Op) {
			eligible = false
			reason = "aggregation operator is not supported by native SQL pushdown"
		} else if child.Fragment == nil || child.OutputKind != OutputKindInstantVector {
			eligible = false
			reason = "aggregation child is not pushdown-safe; native pushdown currently requires a native-lowerable instant-vector child"
		}
		info.Aggregation = &AggregationSupport{Eligible: eligible, Reason: reason, Source: child.Fragment}
		if eligible {
			info.Fragment = &NativeFragment{
				Kind:       FragmentKindAggregation,
				OutputKind: outputKind,
				Aggregation: &AggregationFragment{
					Op:          n.Op,
					Grouping:    append([]string(nil), n.Grouping...),
					Without:     n.Without,
					ParamNumber: cloneFloat64Pointer(n.ParamNumber),
					Source:      child.Fragment,
				},
			}
		}
		info.NativeLowerable = eligible
		info.NativeReason = reason
		return info
	case *planpkg.LogicalHistogramQuantilePlan:
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
	case *planpkg.LogicalHistogramFractionPlan:
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
	case *planpkg.LogicalHistogramProjectionPlan:
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
		if (n.Func == "histogram_count" || n.Func == "histogram_sum" || n.Func == "histogram_avg") && child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
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
	case *planpkg.LogicalRangeFunctionPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		preservesName := rangeFunctionPreservesMetricName(n.Func)
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
	case *planpkg.LogicalVectorPlan:
		child := a.walk(n.Child)
		info.NodeType = "vector"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "vector() currently stays on the local execution path"
		return info
	case *planpkg.LogicalSortPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path", n.Func)
		return info
	case *planpkg.LogicalScalarConvertPlan:
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
	case *planpkg.LogicalInfoPlan:
		child := a.walk(n.Child)
		info.NodeType = "info"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		for _, label := range infoJoinCopyLabelNames(n.SelectorMatchers) {
			info.LabelLineage = mutateDestinationLabel(info.LabelLineage, label, LabelLineageSynthetic)
		}
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		if metricName, ok := nativeInfoMetricName(n.SelectorMatchers); ok && child.Fragment != nil && child.OutputKind == OutputKindInstantVector {
			info.NativeLowerable = true
			info.NativeReason = "info() can lower to native SQL for the single-info-metric join subset"
			info.Fragment = &NativeFragment{Kind: FragmentKindInfoJoin, OutputKind: OutputKindInstantVector, InfoJoin: &InfoJoinFragment{Child: child.Fragment, InfoMetricName: metricName, SelectorMatchers: infoJoinDataLabelMatchers(n.SelectorMatchers), CopyLabelNames: infoJoinCopyLabelNames(n.SelectorMatchers), DropUnmatched: infoJoinDropUnmatched(n.SelectorMatchers)}}
			return info
		}
		info.NativeReason = "info() currently stays on the local execution path"
		return info
	case *planpkg.LogicalPointwiseFunctionPlan:
		info.NodeType = n.Func
		var child *LoweringInfo
		if n.Child != nil {
			child = a.walk(n.Child)
			info.Children = []*LoweringInfo{child}
			info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageDropped)
			info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
			if template, ok := nativePointwiseSourceTemplate(n.Func, n.ParamNumbers); ok && child.Fragment != nil && isSupportedAggregationSourceFragment(child.Fragment) {
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
	case *planpkg.LogicalScalarBuiltinPlan:
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
	case *planpkg.LogicalRoundPlan:
		child := a.walk(n.Child)
		info.NodeType = "round"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "round() currently stays on the local execution path"
		return info
	case *planpkg.LogicalRatePlan:
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
	case *planpkg.LogicalIncreasePlan:
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
	case *planpkg.LogicalDeltaPlan:
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
	case *planpkg.LogicalChangesPlan:
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
	case *planpkg.LogicalDerivPlan:
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
	case *planpkg.LogicalQuantileOverTimePlan:
		child := a.walk(n.Child)
		info.NodeType = "quantile_over_time"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "quantile_over_time intentionally stays on the local execution path; see keep-local note in .pi/native-sql-lowering-plan/13-keep-local-quantile-over-time.md"
		return info
	case *planpkg.LogicalAbsentPlan:
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
	case *planpkg.LogicalAbsentOverTimePlan:
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
	case *planpkg.LogicalSubqueryPlan:
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
	case *planpkg.LogicalLabelReplacePlan:
		child := a.walk(n.Child)
		info.NodeType = "label_replace"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = mutateDestinationLabel(child.LabelLineage, n.Config.Dst, LabelLineageMutated)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "label_replace currently stays on the local execution path"
		return info
	case *planpkg.LogicalLabelJoinPlan:
		child := a.walk(n.Child)
		info.NodeType = "label_join"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = mutateDestinationLabel(child.LabelLineage, n.Config.Dst, LabelLineageSynthetic)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "label_join currently stays on the local execution path"
		return info
	default:
		info.NodeType = fmt.Sprintf("%T", node)
		info.LabelLineage = unknownLineage()
		info.NativeReason = fmt.Sprintf("no native analysis rule is registered for %T", node)
		return info
	}
}

func describeLogicalPlan(node planpkg.LogicalPlan) (string, OutputKind) {
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
