package promshim

import nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"

type planEstimate struct {
	RangeSeconds    float64 `json:"rangeSeconds,omitempty"`
	StepSeconds     float64 `json:"stepSeconds,omitempty"`
	PointsPerSeries int64   `json:"pointsPerSeries,omitempty"`
}

type ExplainNode struct {
	Kind                string                  `json:"kind"`
	Strategy            string                  `json:"strategy"`
	SelectedStrategy    string                  `json:"selectedStrategy,omitempty"`
	NativeScope         string                  `json:"nativeScope,omitempty"`
	Expr                string                  `json:"expr,omitempty"`
	Reason              string                  `json:"reason,omitempty"`
	FallbackReason      string                  `json:"fallbackReason,omitempty"`
	Estimate            *planEstimate           `json:"estimate,omitempty"`
	Lowering            *nativeplan.ExplainInfo `json:"lowering,omitempty"`
	RulesApplied        []string                `json:"rulesApplied,omitempty"`
	PushedPredicates    []string                `json:"pushedPredicates,omitempty"`
	InferredPredicates  []string                `json:"inferredPredicates,omitempty"`
	RequiredColumns     []string                `json:"requiredColumns,omitempty"`
	MaterializedColumns []string                `json:"materializedColumns,omitempty"`
	SemanticBarriers    []string                `json:"semanticBarriers,omitempty"`
	RenderedSQL         string                  `json:"renderedSQL,omitempty"`
	JoinShape           string                  `json:"joinShape,omitempty"`
	JoinLabels          []string                `json:"joinLabels,omitempty"`
	Children            []ExplainNode           `json:"children,omitempty"`
}

func estimateRangePointsPerSeries(ctx planContext) int64 {
	if ctx.Mode != evalModeRange || ctx.Step <= 0 || ctx.End.Before(ctx.Start) {
		return 0
	}
	return int64(ctx.End.Sub(ctx.Start)/ctx.Step) + 1
}

func estimateRangePlan(ctx planContext) *planEstimate {
	pointsPerSeries := estimateRangePointsPerSeries(ctx)
	if pointsPerSeries == 0 {
		return nil
	}
	return &planEstimate{
		RangeSeconds:    ctx.End.Sub(ctx.Start).Seconds(),
		StepSeconds:     ctx.Step.Seconds(),
		PointsPerSeries: pointsPerSeries,
	}
}

func explainPlan(plan queryPlan) ExplainNode {
	if plan == nil {
		return ExplainNode{}
	}
	explain := plan.explain()
	finalizeExplainNode(&explain)
	return explain
}

func explainPlanWithLowering(plan queryPlan, lowering *nativeplan.LoweringInfo) ExplainNode {
	explain := explainPlan(plan)
	annotateExplainNode(&explain, lowering)
	return explain
}

func annotateExplainNode(node *ExplainNode, lowering *nativeplan.LoweringInfo) {
	if node == nil {
		return
	}
	if lowering != nil {
		node.Lowering = lowering.ExplainInfo()
		for index := 0; index < len(node.Children) && index < len(lowering.Children); index++ {
			annotateExplainNode(&node.Children[index], lowering.Children[index])
		}
		return
	}
	for index := range node.Children {
		annotateExplainNode(&node.Children[index], nil)
	}
}

func finalizeExplainNode(node *ExplainNode) {
	if node == nil {
		return
	}
	node.SelectedStrategy = node.Strategy
	switch node.Strategy {
	case "native_sql":
		node.NativeScope = "subtree"
	case "delegated_promql":
		node.NativeScope = "delegated"
	default:
		node.NativeScope = "none"
	}
	if node.Strategy != "native_sql" {
		node.FallbackReason = node.Reason
	}
	for index := range node.Children {
		finalizeExplainNode(&node.Children[index])
	}
}
