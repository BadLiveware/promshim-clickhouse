package native

import (
	"fmt"
	"sort"
	"strings"

	planpkg "ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
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
		}

		info.NativeReason = "binary expression currently lowers natively only for scalar/vector arithmetic over a lowerable child source"
		return info
	case *planpkg.LogicalAggregationPlan:
		child := a.walk(n.Child)
		info.NodeType = "aggregation"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = aggregationLabelLineage(child.LabelLineage, n.Grouping, n.Without)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)

		reason := "pushing aggregation into native ClickHouse SQL over a lowerable child source avoids materializing the full child result in Go"
		eligible := true
		if !isSupportedNativeAggregation(n.Op) {
			eligible = false
			reason = "aggregation operator is not supported by native SQL pushdown"
		} else if child.Fragment == nil {
			eligible = false
			reason = "aggregation child is not pushdown-safe; native pushdown currently requires one delegatable leaf with only unary or scalar arithmetic transforms"
		}
		info.Aggregation = &AggregationSupport{Eligible: eligible, Reason: reason, Source: child.Fragment}
		if eligible {
			info.Fragment = &NativeFragment{
				Kind:       FragmentKindAggregation,
				OutputKind: outputKind,
				Aggregation: &AggregationFragment{
					Op:       n.Op,
					Grouping: append([]string(nil), n.Grouping...),
					Without:  n.Without,
					Source:   child.Fragment,
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
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "histogram_quantile currently stays on the local execution path"
		return info
	case *planpkg.LogicalHistogramFractionPlan:
		child := a.walk(n.Child)
		info.NodeType = "histogram_fraction"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "histogram_fraction currently stays on the local execution path"
		return info
	case *planpkg.LogicalHistogramProjectionPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path", n.Func)
		return info
	case *planpkg.LogicalRangeFunctionPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
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
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path until native range lowering lands", n.Func)
		return info
	case *planpkg.LogicalIncreasePlan:
		child := a.walk(n.Child)
		info.NodeType = "increase"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "increase currently stays on the local execution path until native range lowering lands"
		return info
	case *planpkg.LogicalDeltaPlan:
		child := a.walk(n.Child)
		info.NodeType = n.Func
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = fmt.Sprintf("%s currently stays on the local execution path until native range lowering lands", n.Func)
		return info
	case *planpkg.LogicalChangesPlan:
		child := a.walk(n.Child)
		info.NodeType = "changes"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "changes currently stays on the local execution path until native range lowering lands"
		return info
	case *planpkg.LogicalDerivPlan:
		child := a.walk(n.Child)
		info.NodeType = "deriv"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "deriv currently stays on the local execution path until native range lowering lands"
		return info
	case *planpkg.LogicalQuantileOverTimePlan:
		child := a.walk(n.Child)
		info.NodeType = "quantile_over_time"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = withMetricNameState(passthroughLabelLineage(child.LabelLineage), LabelLineageUnknown)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "quantile_over_time currently stays on the local execution path until native range lowering lands"
		return info
	case *planpkg.LogicalAbsentPlan:
		child := a.walk(n.Child)
		info.NodeType = "absent"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(n.OutputMetric)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "absent() currently stays on the local execution path"
		return info
	case *planpkg.LogicalAbsentOverTimePlan:
		child := a.walk(n.Child)
		info.NodeType = "absent_over_time"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = syntheticOutputLineage(n.OutputMetric)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
		info.NativeReason = "absent_over_time currently stays on the local execution path"
		return info
	case *planpkg.LogicalSubqueryPlan:
		child := a.walk(n.Child)
		info.NodeType = "subquery"
		info.Children = []*LoweringInfo{child}
		info.LabelLineage = passthroughLabelLineage(child.LabelLineage)
		info.TimeRequirements = subqueryTimeRequirements(child.TimeRequirements, n.Range, n.Offset)
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

func isSupportedNativeAggregation(op parser.ItemType) bool {
	switch op {
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG:
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

func tagsExprForMetricDrop(dropMetric bool) string {
	if !dropMetric {
		return "{tags}"
	}
	return "arrayFilter(tag -> tag.1 != '__name__', {tags})"
}

func wrapValueExpr(expr string) string {
	return "(" + expr + ")"
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
