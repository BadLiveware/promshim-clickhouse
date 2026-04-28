package renderer

import (
	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

const (
	threadCapReasonDirectRangeAggregation = "direct_range_aggregation_cpu_guardrail"
	threadCapReasonFusedRateAggregation   = "fused_rate_aggregation_cpu_guardrail"
	threadCapReasonSubqueryRateRows       = "subquery_rate_over_aggregate_regresses_with_thread_cap"
)

func directRangeAggregationThreadSettings(params RenderParams, source storage.AggregationSource) map[string]any {
	if params.Mode != native.RenderModeRange || source.Selector == nil {
		return nil
	}
	return physicalSettings(preferASOFThreadGuardrail(params.Physical, threadCapReasonDirectRangeAggregation))
}

func fusedRateAggregationThreadSettings(params RenderParams, agg *logicalpkg.AggregationPlan) map[string]any {
	if params.Mode != native.RenderModeRange || agg == nil {
		return nil
	}
	child, fn, ok := rangeFunctionChildNode(agg.Child)
	if !ok || fn != "rate" {
		return nil
	}
	if !isMatrixSelectorLeaf(child) {
		return nil
	}
	return physicalSettings(preferASOFThreadGuardrail(params.Physical, threadCapReasonFusedRateAggregation))
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

func isMatrixSelectorLeaf(node logicalpkg.Node) bool {
	leaf, ok := node.(*logicalpkg.LeafExprPlan)
	if !ok || leaf == nil {
		return false
	}
	_, ok = leaf.Expr.(*parser.MatrixSelector)
	return ok
}
