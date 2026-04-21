package promshim

import nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"

type planEstimate struct {
	RangeSeconds    float64 `json:"rangeSeconds,omitempty"`
	StepSeconds     float64 `json:"stepSeconds,omitempty"`
	PointsPerSeries int64   `json:"pointsPerSeries,omitempty"`
}

type ExplainNode struct {
	Kind     string                  `json:"kind"`
	Strategy string                  `json:"strategy"`
	Expr     string                  `json:"expr,omitempty"`
	Reason   string                  `json:"reason,omitempty"`
	Estimate *planEstimate           `json:"estimate,omitempty"`
	Lowering *nativeplan.ExplainInfo `json:"lowering,omitempty"`
	Children []ExplainNode           `json:"children,omitempty"`
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
	return plan.explain()
}

func explainPlanWithLowering(plan queryPlan, lowering *nativeplan.LoweringInfo) ExplainNode {
	explain := explainPlan(plan)
	annotateExplainNode(&explain, lowering)
	return explain
}

func annotateExplainNode(node *ExplainNode, lowering *nativeplan.LoweringInfo) {
	if node == nil || lowering == nil {
		return
	}
	node.Lowering = lowering.ExplainInfo()
	for index := 0; index < len(node.Children) && index < len(lowering.Children); index++ {
		annotateExplainNode(&node.Children[index], lowering.Children[index])
	}
}
