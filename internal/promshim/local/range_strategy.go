package promshim

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
