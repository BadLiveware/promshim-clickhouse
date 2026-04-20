package promshim

import "github.com/prometheus/prometheus/promql/parser"

type nativeAggregationEligibility struct {
	Eligible bool
	Reason   string
	Source   nativeAggregationSource
}

func decideNativeAggregationPushdown(node *logicalAggregationPlan, ctx planContext) nativeAggregationEligibility {
	if node == nil {
		return nativeAggregationEligibility{Reason: "aggregation pushdown requires an aggregation node"}
	}
	if !ctx.PreferNativeAggregationPushdown {
		return nativeAggregationEligibility{Reason: "native aggregation pushdown is disabled for this request context"}
	}
	if !isNativeAggregationOperatorSupported(node.Op) {
		return nativeAggregationEligibility{Reason: "aggregation operator is not supported by native SQL pushdown"}
	}
	source, ok := buildNativeAggregationSource(node.Child)
	if !ok {
		return nativeAggregationEligibility{Reason: "aggregation child is not pushdown-safe; native pushdown currently requires one delegatable leaf with only unary or scalar arithmetic transforms"}
	}
	return nativeAggregationEligibility{
		Eligible: true,
		Reason:   "pushing aggregation into native ClickHouse SQL over a delegatable leaf-compatible child to avoid materializing the full child result in Go",
		Source:   source,
	}
}

func isNativeAggregationOperatorSupported(op parser.ItemType) bool {
	switch op {
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG:
		return true
	default:
		return false
	}
}
