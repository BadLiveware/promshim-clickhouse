package native

import (
	"fmt"
	"sort"
	"strings"

	planpkg "ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/model/labels"
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

func isSupportedNativeSyntheticScalarBuiltin(name string) bool {
	switch name {
	case "pi", "time":
		return true
	default:
		return false
	}
}

func isSupportedNativeSyntheticDateFunction(name string) bool {
	switch name {
	case "minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return true
	default:
		return false
	}
}

// nativeInfoMetricName returns the single info metric name that the native
// renderer can safely join today. We lower the default target_info convention
// and explicit equality matchers; broader regex/negative-name matching stays on
// the local path because it can span multiple info metrics that need merge
// semantics beyond the current native join shape.
func nativeInfoMetricName(matchers []*labels.Matcher) (string, bool) {
	nameMatchers := make([]*labels.Matcher, 0)
	for _, matcher := range matchers {
		if matcher != nil && matcher.Name == labels.MetricName {
			nameMatchers = append(nameMatchers, matcher)
		}
	}
	if len(nameMatchers) == 0 {
		return "target_info", true
	}
	if len(nameMatchers) == 1 && nameMatchers[0].Type == labels.MatchEqual {
		return nameMatchers[0].Value, true
	}
	return "", false
}

func infoJoinDataLabelMatchers(matchers []*labels.Matcher) []*labels.Matcher {
	out := make([]*labels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == labels.MetricName {
			continue
		}
		out = append(out, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return out
}

func infoJoinCopyLabelNames(matchers []*labels.Matcher) []string {
	if len(matchers) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == labels.MetricName {
			continue
		}
		if _, ok := seen[matcher.Name]; ok {
			continue
		}
		seen[matcher.Name] = struct{}{}
		out = append(out, matcher.Name)
	}
	sort.Strings(out)
	return out
}

func infoJoinDropUnmatched(matchers []*labels.Matcher) bool {
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == labels.MetricName {
			continue
		}
		if !matcher.Matches("") {
			return true
		}
	}
	return false
}

func nativePointwiseSourceTemplate(name string, paramNumbers []*float64) (string, bool) {
	switch name {
	case "abs":
		return "abs({value})", true
	case "ceil":
		return "ceil({value})", true
	case "floor":
		return "floor({value})", true
	case "sgn":
		return "sign({value})", true
	case "exp":
		return "exp({value})", true
	case "ln":
		return "log({value})", true
	case "log2":
		return "log2({value})", true
	case "log10":
		return "log10({value})", true
	case "sqrt":
		return "sqrt({value})", true
	case "sin":
		return "sin({value})", true
	case "cos":
		return "cos({value})", true
	case "tan":
		return "tan({value})", true
	case "asin":
		return "asin({value})", true
	case "acos":
		return "acos({value})", true
	case "atan":
		return "atan({value})", true
	case "sinh":
		return "sinh({value})", true
	case "cosh":
		return "cosh({value})", true
	case "tanh":
		return "tanh({value})", true
	case "asinh":
		return "asinh({value})", true
	case "acosh":
		return "acosh({value})", true
	case "atanh":
		return "atanh({value})", true
	case "deg":
		return "degrees({value})", true
	case "rad":
		return "radians({value})", true
	case "timestamp":
		return "toFloat64(toUnixTimestamp64Milli({timestamp})) / 1000.0", true
	case "minute":
		return "toFloat64(toMinute(toDateTime(toInt64({value}), 'UTC')))", true
	case "hour":
		return "toFloat64(toHour(toDateTime(toInt64({value}), 'UTC')))", true
	case "day_of_week":
		return "toFloat64(modulo(toDayOfWeek(toDateTime(toInt64({value}), 'UTC')), 7))", true
	case "day_of_month":
		return "toFloat64(toDayOfMonth(toDateTime(toInt64({value}), 'UTC')))", true
	case "day_of_year":
		return "toFloat64(toDayOfYear(toDateTime(toInt64({value}), 'UTC')))", true
	case "days_in_month":
		return "toFloat64(toDaysInMonth(toDateTime(toInt64({value}), 'UTC')))", true
	case "month":
		return "toFloat64(toMonth(toDateTime(toInt64({value}), 'UTC')))", true
	case "year":
		return "toFloat64(toYear(toDateTime(toInt64({value}), 'UTC')))", true
	case "clamp":
		if len(paramNumbers) == 2 && paramNumbers[0] != nil && paramNumbers[1] != nil {
			if *paramNumbers[0] > *paramNumbers[1] {
				return "", false
			}
			return fmt.Sprintf("greatest(%s, least(%s, {value}))", storage.NativeFloatLiteral(*paramNumbers[0]), storage.NativeFloatLiteral(*paramNumbers[1])), true
		}
	case "clamp_min":
		if len(paramNumbers) == 1 && paramNumbers[0] != nil {
			return fmt.Sprintf("greatest({value}, %s)", storage.NativeFloatLiteral(*paramNumbers[0])), true
		}
	case "clamp_max":
		if len(paramNumbers) == 1 && paramNumbers[0] != nil {
			return fmt.Sprintf("least({value}, %s)", storage.NativeFloatLiteral(*paramNumbers[0])), true
		}
	}
	return "", false
}

func isSupportedNativeAggregateOverTime(name string) bool {
	switch name {
	case "last_over_time", "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "count_over_time",
		"stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time":
		return true
	default:
		return false
	}
}

// rangeFunctionPreservesMetricName reports whether the range function returns a
// sample from the original series (preserving __name__) rather than a derived
// aggregation. Only last_over_time has this property among the supported range
// functions; everything else (rate/increase/delta/*_over_time/deriv/...) drops
// the metric name because the returned value is a new quantity.
func rangeFunctionPreservesMetricName(name string) bool {
	return name == "last_over_time"
}

func isSupportedNativeCounterRangeFunction(name string) bool {
	switch name {
	case "rate", "irate", "increase", "delta", "idelta", "changes", "deriv":
		return true
	default:
		return false
	}
}

func isSupportedNativeAggregation(op parser.ItemType) bool {
	switch op {
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG, parser.STDDEV, parser.STDVAR, parser.QUANTILE, parser.GROUP:
		return true
	default:
		return false
	}
}

func IsSupportedNativeRangeModeForDirectSelector(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindLeafSource && child.Selector != nil && child.Selector.Kind == SelectorKindRangeVector
}

func IsSupportedNativeRangeModeForAggregateOverTimeSubquery(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	if !isSupportedNativeAggregateOverTime(fragment.RangeFunction.Func) {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil
}

func IsSupportedNativeRangeModeForWindowedArraysSubquery(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	if !isSupportedNativeAggregateOverTime(fragment.RangeFunction.Func) && fragment.RangeFunction.Func != "predict_linear" && fragment.RangeFunction.Func != "resets" && fragment.RangeFunction.Func != "double_exponential_smoothing" && fragment.RangeFunction.Func != "holt_winters" {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil
}

func IsSupportedNativeRangeModeForCounterSubquery(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	if !isSupportedNativeCounterRangeFunction(fragment.RangeFunction.Func) {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil
}

func isSupportedNativeRangeChildFragment(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindSubquery:
		return true
	default:
		return false
	}
}

func isSupportedNativeSubqueryChildFragment(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr, FragmentKindAggregation, FragmentKindBinaryVectorJoin, FragmentKindRangeFunction, FragmentKindValueTransform:
		return true
	default:
		return false
	}
}

func isSupportedAggregationSourceFragment(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr, FragmentKindAggregation:
		return true
	default:
		return false
	}
}

func isSupportedNativeVectorJoinOp(op parser.ItemType, matching *parser.VectorMatching) bool {
	if isSetOperator(op) {
		return false
	}
	normalized := normalizeVectorMatching(matching)
	if normalized.Card == parser.CardManyToMany {
		return false
	}
	switch op {
	case parser.ADD, parser.SUB, parser.MUL, parser.DIV, parser.MOD, parser.POW,
		parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func supportedNativeVectorJoinShape(matching *parser.VectorMatching) (string, bool) {
	normalized := normalizeVectorMatching(matching)
	switch normalized.Card {
	case parser.CardOneToOne:
		return JoinShapeOneToOne, true
	case parser.CardManyToOne:
		return JoinShapeManyToOne, true
	case parser.CardOneToMany:
		return JoinShapeOneToMany, true
	default:
		return "", false
	}
}

func nativeVectorJoinDropsMetricName(op parser.ItemType, returnBool bool) bool {
	return !isComparisonBinaryOperator(op) || returnBool
}

func nativeVectorJoinLabelLineage(lhs, rhs LabelLineage, matching *parser.VectorMatching, op parser.ItemType, returnBool bool) LabelLineage {
	normalized := normalizeVectorMatching(matching)
	result := passthroughLabelLineage(lhs)
	if normalized.Card == parser.CardOneToMany {
		result = passthroughLabelLineage(rhs)
	}
	if normalized.Card == parser.CardOneToOne {
		if normalized.On {
			kept := map[string]LabelLineageState{}
			for _, label := range normalized.MatchingLabels {
				if label == labels.MetricName {
					continue
				}
				if state, ok := result.Known[label]; ok {
					kept[label] = state
				}
			}
			result.Known = kept
			result.Wildcard = LabelLineageDropped
		} else {
			for _, label := range normalized.MatchingLabels {
				delete(result.Known, label)
			}
		}
	}
	for _, label := range normalized.Include {
		if label == labels.MetricName {
			continue
		}
		if state, ok := rhs.Known[label]; ok {
			result.Known[label] = state
		} else {
			result.Known[label] = LabelLineageCopied
		}
	}
	if nativeVectorJoinDropsMetricName(op, returnBool) {
		result.MetricName = LabelLineageDropped
		result.Known[labels.MetricName] = LabelLineageDropped
	}
	return result
}

func isComparisonBinaryOperator(op parser.ItemType) bool {
	switch op {
	case parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func isSetOperator(op parser.ItemType) bool {
	switch op {
	case parser.LAND, parser.LOR, parser.LUNLESS:
		return true
	default:
		return false
	}
}

func applyUnarySourceTransform(op parser.ItemType, valueExpr string, childDropsMetric bool) (string, bool, bool) {
	switch op {
	case parser.ADD:
		return valueExpr, childDropsMetric, true
	case parser.SUB:
		return "-" + wrapValueExpr(valueExpr), true, true
	default:
		return "", false, false
	}
}

func applyBinarySourceTransform(op parser.ItemType, valueExpr string, scalar float64, scalarOnLeft bool) (string, bool, bool) {
	valueExpr = wrapValueExpr(valueExpr)
	scalarExpr := storage.NativeFloatLiteral(scalar)

	switch op {
	case parser.ADD:
		if scalarOnLeft {
			return scalarExpr + " + " + valueExpr, true, true
		}
		return valueExpr + " + " + scalarExpr, true, true
	case parser.SUB:
		if scalarOnLeft {
			return scalarExpr + " - " + valueExpr, true, true
		}
		return valueExpr + " - " + scalarExpr, true, true
	case parser.MUL:
		if scalarOnLeft {
			return scalarExpr + " * " + valueExpr, true, true
		}
		return valueExpr + " * " + scalarExpr, true, true
	case parser.DIV:
		if scalarOnLeft {
			return scalarExpr + " / " + valueExpr, true, true
		}
		return valueExpr + " / " + scalarExpr, true, true
	case parser.MOD:
		if scalarOnLeft {
			return "modulo(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "modulo(" + valueExpr + ", " + scalarExpr + ")", true, true
	case parser.POW:
		if scalarOnLeft {
			return "pow(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "pow(" + valueExpr + ", " + scalarExpr + ")", true, true
	default:
		return "", false, false
	}
}

func isSyntheticScalarFragment(fragment *NativeFragment) bool {
	if fragment == nil || fragment.Synthetic == nil {
		return false
	}
	if fragment.Kind != FragmentKindSyntheticSeries {
		return false
	}
	return fragment.OutputKind == OutputKindScalar && isSupportedNativeSyntheticScalarBuiltin(fragment.Synthetic.Func)
}

func syntheticScalarValueTemplate(name string) (string, bool) {
	switch name {
	case "time":
		return "toFloat64(toUnixTimestamp64Milli({timestamp})) / 1000.0", true
	case "pi":
		return "toFloat64(3.141592653589793)", true
	default:
		return "", false
	}
}

func applySyntheticScalarVectorTransform(op parser.ItemType, syntheticFunc string, vectorFragment *NativeFragment, vectorLineage LabelLineage, scalarOnLeft bool) (*NativeFragment, LabelLineage, bool) {
	if vectorFragment == nil {
		return nil, LabelLineage{}, false
	}
	scalarExpr, ok := syntheticScalarValueTemplate(syntheticFunc)
	if !ok {
		return nil, LabelLineage{}, false
	}
	template, dropsMetric, ok := buildBinaryTemplateForScalarExpr(op, scalarExpr, "{value}", scalarOnLeft)
	if !ok {
		return nil, LabelLineage{}, false
	}
	lineage := withMetricNameState(passthroughLabelLineage(vectorLineage), boolState(dropsMetric, LabelLineageDropped, vectorLineage.MetricName))
	return &NativeFragment{
		Kind:       FragmentKindValueTransform,
		OutputKind: OutputKindInstantVector,
		ValueTransform: &ValueTransformFragment{
			Child:       vectorFragment,
			ValueExpr:   template,
			DropsMetric: dropsMetric,
		},
		DropsMetric: dropsMetric,
	}, lineage, true
}

func applyComparisonFilterTransform(op parser.ItemType, returnBool bool, vectorFragment *NativeFragment, vectorLineage LabelLineage, scalar float64, scalarOnLeft bool) (*NativeFragment, LabelLineage, bool) {
	if vectorFragment == nil {
		return nil, LabelLineage{}, false
	}
	if !isComparisonBinaryOperator(op) {
		return nil, LabelLineage{}, false
	}
	if returnBool {
		return nil, LabelLineage{}, false
	}
	if vectorFragment.OutputKind != OutputKindInstantVector {
		return nil, LabelLineage{}, false
	}
	filter, ok := comparisonFilterTemplate(op, scalar, scalarOnLeft)
	if !ok {
		return nil, LabelLineage{}, false
	}
	lineage := passthroughLabelLineage(vectorLineage)
	return &NativeFragment{
		Kind:       FragmentKindValueTransform,
		OutputKind: OutputKindInstantVector,
		ValueTransform: &ValueTransformFragment{
			Child:      vectorFragment,
			ValueExpr:  "{value}",
			FilterExpr: filter,
		},
	}, lineage, true
}

func comparisonFilterTemplate(op parser.ItemType, scalar float64, scalarOnLeft bool) (string, bool) {
	opSym, ok := comparisonOperatorSymbol(op)
	if !ok {
		return "", false
	}
	scalarExpr := storage.NativeFloatLiteral(scalar)
	if scalarOnLeft {
		return scalarExpr + " " + opSym + " ({value})", true
	}
	return "({value}) " + opSym + " " + scalarExpr, true
}

func comparisonOperatorSymbol(op parser.ItemType) (string, bool) {
	switch op {
	case parser.EQLC:
		return "=", true
	case parser.NEQ:
		return "!=", true
	case parser.GTR:
		return ">", true
	case parser.LSS:
		return "<", true
	case parser.GTE:
		return ">=", true
	case parser.LTE:
		return "<=", true
	default:
		return "", false
	}
}

func buildBinaryTemplateForScalarExpr(op parser.ItemType, scalarExpr, valueExpr string, scalarOnLeft bool) (string, bool, bool) {
	left := scalarExpr
	right := "(" + valueExpr + ")"
	if !scalarOnLeft {
		left = "(" + valueExpr + ")"
		right = scalarExpr
	}
	switch op {
	case parser.ADD:
		return left + " + " + right, true, true
	case parser.SUB:
		return left + " - " + right, true, true
	case parser.MUL:
		return left + " * " + right, true, true
	case parser.DIV:
		return left + " / " + right, true, true
	case parser.MOD:
		return "modulo(" + left + ", " + right + ")", true, true
	case parser.POW:
		return "pow(" + left + ", " + right + ")", true, true
	default:
		return "", false, false
	}
}

func tagsExprForMetricDrop(dropMetric bool) string {
	if !dropMetric {
		return "{tags}"
	}
	return "arrayFilter(tag -> tag.1 != '__name__', {tags})"
}

func wrapValueExpr(expr string) string {
	return "(" + expr + ")"
}

func composePointwiseSourceTemplate(template, childValueExpr string) string {
	if childValueExpr == "" || childValueExpr == "{value}" {
		return template
	}
	return strings.NewReplacer("{value}", wrapValueExpr(childValueExpr)).Replace(template)
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
