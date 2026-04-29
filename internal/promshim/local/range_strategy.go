package local

func applyRangeExecutionStrategy(plan Plan, ctx PlanContext) (Plan, error) {
	if ctx.Mode != EvalModeRange {
		return plan, nil
	}
	estimate := estimateRangePlan(ctx)
	if estimate == nil {
		return plan, nil
	}
	if ctx.MaxRangePointsPerSeries > 0 && estimate.PointsPerSeries > ctx.MaxRangePointsPerSeries {
		return nil, NewBadDataErrorf("range query would evaluate %d points per series, exceeding configured limit %d; reduce the time range or increase the step", estimate.PointsPerSeries, ctx.MaxRangePointsPerSeries)
	}
	if ctx.NativeRangeChunkPointsPerSeries > 0 && estimate.PointsPerSeries > ctx.NativeRangeChunkPointsPerSeries && shouldAutoChunkNativeRangePlan(plan) {
		return &chunkedRangePlan{
			Child:                plan,
			ChunkPointsPerSeries: ctx.NativeRangeChunkPointsPerSeries,
			Reason:               "chunking native range SQL to cap ClickHouse peak memory for native-grid range aggregation",
			Estimate:             estimate,
		}, nil
	}
	if ctx.RangeChunkPointsPerSeries > 0 && estimate.PointsPerSeries > ctx.RangeChunkPointsPerSeries && shouldChunkLocalRangePlan(plan) {
		return &chunkedRangePlan{
			Child:                plan,
			ChunkPointsPerSeries: ctx.RangeChunkPointsPerSeries,
			Reason:               "chunking local range execution to cap intermediate materialization per request chunk",
			Estimate:             estimate,
		}, nil
	}
	return plan, nil
}

func shouldChunkLocalRangePlan(plan Plan) bool {
	switch plan.(type) {
	case *localAggregationPlan, *localUnaryPlan, *localBinaryPlan, *localLabelReplacePlan, *localLabelJoinPlan:
		return true
	default:
		return false
	}
}

func shouldAutoChunkNativeRangePlan(plan Plan) bool {
	nativePlan, ok := plan.(*nativeSubtreePlan)
	if !ok || nativePlan == nil || nativePlan.OptimizationReport == nil {
		return false
	}
	for _, decision := range nativePlan.OptimizationReport.PhysicalDecisions {
		if decision.Kind == "fused_range_aggregation" && decision.Strategy == "native_grid_sum_aggregation" {
			return true
		}
	}
	return false
}
