package local

import nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"

type nativeAggregationEligibility struct {
	Eligible bool
	Reason   string
	Source   nativeAggregationSource
}

func decideNativeAggregationPushdown(node *logicalAggregationPlan, ctx PlanContext) nativeAggregationEligibility {
	return decideNativeAggregationPushdownFromAnalysis(node, nativeplan.Analyze(node), ctx)
}

func decideNativeAggregationPushdownFromAnalysis(node *logicalAggregationPlan, analysis *nativeplan.Analysis, ctx PlanContext) nativeAggregationEligibility {
	if node == nil {
		return nativeAggregationEligibility{Reason: "aggregation pushdown requires an aggregation node"}
	}
	if !ctx.PreferNativeAggregationPushdown {
		return nativeAggregationEligibility{Reason: "native aggregation pushdown is disabled for this request context"}
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Aggregation == nil {
		return nativeAggregationEligibility{Reason: "native aggregation analysis metadata is unavailable for this node"}
	}
	if !info.Aggregation.Eligible {
		return nativeAggregationEligibility{Reason: info.Aggregation.Reason}
	}
	source, ok := nativeAggregationSourceFromLowering(info)
	if !ok {
		return nativeAggregationEligibility{Reason: "native aggregation analysis did not produce a source fragment"}
	}
	if result := analyzeDelegatedExprSupportForContext(source.PromQLLeaf, ctx); !result.Supported {
		return nativeAggregationEligibility{Reason: result.Reason}
	}
	return nativeAggregationEligibility{
		Eligible: true,
		Reason:   info.Aggregation.Reason,
		Source:   source,
	}
}
