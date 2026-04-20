package promshim

import "time"

type planContext struct {
	Mode                            evalMode
	EvaluationTime                  time.Time
	Start                           time.Time
	End                             time.Time
	Step                            time.Duration
	PreferNativeAggregationPushdown bool
	MaxRangePointsPerSeries         int64
	RangeChunkPointsPerSeries       int64
}

type planEstimate struct {
	RangeSeconds    float64 `json:"rangeSeconds,omitempty"`
	StepSeconds     float64 `json:"stepSeconds,omitempty"`
	PointsPerSeries int64   `json:"pointsPerSeries,omitempty"`
}

type ExplainNode struct {
	Kind     string        `json:"kind"`
	Strategy string        `json:"strategy"`
	Expr     string        `json:"expr,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Estimate *planEstimate `json:"estimate,omitempty"`
	Children []ExplainNode `json:"children,omitempty"`
}

func defaultPlanContext(mode evalMode) planContext {
	return planContext{
		Mode:                            mode,
		PreferNativeAggregationPushdown: false,
		MaxRangePointsPerSeries:         defaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:       defaultRangeChunkPointsPerSeries,
	}
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

func applyRangeExecutionStrategy(plan queryPlan, ctx planContext) (queryPlan, error) {
	if ctx.Mode != evalModeRange {
		return plan, nil
	}
	estimate := estimateRangePlan(ctx)
	if estimate == nil {
		return plan, nil
	}
	if ctx.MaxRangePointsPerSeries > 0 && estimate.PointsPerSeries > ctx.MaxRangePointsPerSeries {
		return nil, newBadDataErrorf("range query would evaluate %d points per series, exceeding configured limit %d; reduce the time range or increase the step", estimate.PointsPerSeries, ctx.MaxRangePointsPerSeries)
	}
	if ctx.RangeChunkPointsPerSeries > 0 && estimate.PointsPerSeries > ctx.RangeChunkPointsPerSeries && shouldChunkRangePlan(plan) {
		return &chunkedRangePlan{
			Child:                plan,
			ChunkPointsPerSeries: ctx.RangeChunkPointsPerSeries,
			Reason:               "chunking local range execution to cap intermediate materialization per request chunk",
			Estimate:             estimate,
		}, nil
	}
	return plan, nil
}

func shouldChunkRangePlan(plan queryPlan) bool {
	switch plan.(type) {
	case *localAggregationPlan, *localUnaryPlan, *localBinaryPlan, *localLabelReplacePlan, *localLabelJoinPlan:
		return true
	default:
		return false
	}
}
