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
	if shouldAutoChunkNativeRangePlan(plan) {
		chunkPoints := nativeRangeChunkPointsPerSeries(ctx, estimate)
		if chunkPoints > 0 {
			return &chunkedRangePlan{
				Child:                plan,
				ChunkPointsPerSeries: chunkPoints,
				Reason:               "chunking native range SQL to cap ClickHouse peak memory for native-grid range aggregation",
				Estimate:             estimate,
			}, nil
		}
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

func nativeRangeChunkPointsPerSeries(ctx PlanContext, estimate *planEstimate) int64 {
	if estimate == nil || estimate.PointsPerSeries <= 0 || ctx.NativeRangeChunkPointsPerSeries <= 0 {
		return 0
	}
	chunkPoints := ctx.NativeRangeChunkPointsPerSeries
	if ctx.NativeRangeChunkMaxDuration > 0 && ctx.Step > 0 {
		durationPoints := int64(ctx.NativeRangeChunkMaxDuration/ctx.Step) + 1
		if durationPoints < 1 {
			durationPoints = 1
		}
		if durationPoints < chunkPoints {
			chunkPoints = durationPoints
		}
	}
	if chunkPoints < 1 {
		chunkPoints = 1
	}
	if ctx.NativeRangeChunkMaxChunks > 0 {
		minChunkPoints := ceilDivInt64(estimate.PointsPerSeries, ctx.NativeRangeChunkMaxChunks)
		if minChunkPoints > chunkPoints {
			chunkPoints = minChunkPoints
		}
	}
	if chunkPoints >= estimate.PointsPerSeries {
		return 0
	}
	return chunkPoints
}

func ceilDivInt64(n, d int64) int64 {
	if d <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
