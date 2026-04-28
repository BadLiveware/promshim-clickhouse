package renderer

import (
	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

const (
	threadCapReasonDirectRangeAggregation = "direct_range_aggregation_cpu_guardrail"
	threadCapReasonFusedRateAggregation   = "fused_rate_aggregation_cpu_guardrail"
	threadCapReasonSubqueryRateRows       = "subquery_rate_over_aggregate_regresses_with_thread_cap"
)

func directRangeAggregationThreadSettings(params RenderParams, source storage.AggregationSource) (map[string]any, []physical.Decision) {
	if params.Mode != native.RenderModeRange || source.Selector == nil {
		return nil, nil
	}
	prefs := preferASOFThreadGuardrail(params.Physical, threadCapReasonDirectRangeAggregation)
	return physicalSettings(prefs), threadPreferenceDecisionsForPrefs(prefs)
}

func fusedRateAggregationThreadSettings(params RenderParams, agg *logicalpkg.AggregationPlan) (map[string]any, []physical.Decision) {
	if params.Mode != native.RenderModeRange || agg == nil {
		return nil, nil
	}
	child, fn, ok := rangeFunctionChildNode(agg.Child)
	if !ok || fn != "rate" {
		return nil, nil
	}
	if !isMatrixSelectorLeaf(child) {
		return nil, nil
	}
	prefs := preferASOFThreadGuardrail(params.Physical, threadCapReasonFusedRateAggregation)
	return physicalSettings(prefs), threadPreferenceDecisionsForPrefs(prefs)
}

func threadPreferenceDecisionsForPrefs(prefs PhysicalPlanPreferences) []physical.Decision {
	if decision, ok := physical.ThreadPreferenceDecision(prefs.Execution.Threads); ok {
		return []physical.Decision{decision}
	}
	return nil
}

func suppressThreadCapForSubqueryRangeFunction(params RenderParams, child *logicalpkg.SubqueryPlan, fn string) RenderParams {
	if child == nil || child.Child == nil || fn != "rate" {
		return params
	}
	if _, ok := child.Child.(*logicalpkg.AggregationPlan); !ok {
		return params
	}
	params.Physical = preferNoThreadCap(params.Physical, threadCapReasonSubqueryRateRows)
	return params
}

func suppressThreadCapForPlan(params RenderParams, node logicalpkg.Node) RenderParams {
	if containsSubqueryRateOverAggregation(node) {
		params.Physical = preferNoThreadCap(params.Physical, threadCapReasonSubqueryRateRows)
	}
	return params
}

func containsSubqueryRateOverAggregation(node logicalpkg.Node) bool {
	if node == nil {
		return false
	}
	if child, fn, ok := rangeFunctionChildNode(node); ok {
		if fn == "rate" {
			if sub, ok := child.(*logicalpkg.SubqueryPlan); ok && sub != nil {
				if _, ok := sub.Child.(*logicalpkg.AggregationPlan); ok {
					return true
				}
			}
		}
		return containsSubqueryRateOverAggregation(child)
	}
	switch n := node.(type) {
	case *logicalpkg.UnaryPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.BinaryPlan:
		return containsSubqueryRateOverAggregation(n.LHS) || containsSubqueryRateOverAggregation(n.RHS)
	case *logicalpkg.AggregationPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.HistogramQuantilePlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.HistogramFractionPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.HistogramProjectionPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.HistogramQuantilesPlan:
		if containsSubqueryRateOverAggregation(n.Child) {
			return true
		}
		for _, child := range n.ParamChildren {
			if containsSubqueryRateOverAggregation(child) {
				return true
			}
		}
	case *logicalpkg.VectorPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.RoundPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.SortPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.ScalarConvertPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.InfoPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.PointwiseFunctionPlan:
		if containsSubqueryRateOverAggregation(n.Child) {
			return true
		}
		for _, child := range n.ParamChildren {
			if containsSubqueryRateOverAggregation(child) {
				return true
			}
		}
	case *logicalpkg.SubqueryPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.AbsentPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.AbsentOverTimePlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.LabelReplacePlan:
		return containsSubqueryRateOverAggregation(n.Child)
	case *logicalpkg.LabelJoinPlan:
		return containsSubqueryRateOverAggregation(n.Child)
	}
	return false
}

func isMatrixSelectorLeaf(node logicalpkg.Node) bool {
	leaf, ok := node.(*logicalpkg.LeafExprPlan)
	if !ok || leaf == nil {
		return false
	}
	_, ok = leaf.Expr.(*parser.MatrixSelector)
	return ok
}
