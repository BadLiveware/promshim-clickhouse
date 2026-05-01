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
	if chunkKind, ok := autoNativeRangeChunkKind(plan); ok {
		chunkPoints := nativeRangeChunkPointsPerSeries(ctx, estimate)
		if ctx.NativeRangeChunkPointsPerSeries == DefaultNativeRangeChunkPointsPerSeries {
			switch chunkKind {
			case "cumulative_avg":
				// For high-overlap cumulative avg_over_time, two chunks gave the best
				// measured latency/memory compromise on realistic 24h/1m workloads.
				chunkPoints = max(chunkPoints, ceilDivInt64(estimate.PointsPerSeries, 2))
			case "native_grid_sum_aggregation":
				// Native-grid sum aggregation is already memory-light for 24h/1m rate
				// ranges; default chunking should only enforce the duration cap.
				chunkPoints = nativeRangeDurationChunkPointsPerSeries(ctx, estimate)
			}
		}
		if chunkPoints > 0 {
			return &chunkedRangePlan{
				Child:                plan,
				ChunkPointsPerSeries: chunkPoints,
				Reason:               nativeRangeChunkReason(chunkKind),
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

func autoNativeRangeChunkKind(plan Plan) (string, bool) {
	nativePlan, ok := plan.(*nativeSubtreePlan)
	if !ok || nativePlan == nil || nativePlan.OptimizationReport == nil {
		return "", false
	}
	for _, decision := range nativePlan.OptimizationReport.PhysicalDecisions {
		if decision.Kind == "fused_range_aggregation" && decision.Strategy == "native_grid_sum_aggregation" {
			return "native_grid_sum_aggregation", true
		}
		if decision.Kind == "range_window_aggregate" && decision.Strategy == "cumulative_avg" {
			return "cumulative_avg", true
		}
	}
	return "", false
}

func nativeRangeChunkReason(kind string) string {
	if kind == "cumulative_avg" {
		return "chunking cumulative avg_over_time range SQL to cap ClickHouse peak memory"
	}
	return "chunking native range SQL to cap ClickHouse peak memory for native-grid range aggregation"
}

func nativeRangeChunkPointsPerSeries(ctx PlanContext, estimate *planEstimate) int64 {
	if estimate == nil || estimate.PointsPerSeries <= 0 || ctx.NativeRangeChunkPointsPerSeries <= 0 {
		return 0
	}
	chunkPoints := ctx.NativeRangeChunkPointsPerSeries
	if durationPoints := nativeRangeDurationChunkPointLimit(ctx); durationPoints > 0 && durationPoints < chunkPoints {
		chunkPoints = durationPoints
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

func nativeRangeDurationChunkPointsPerSeries(ctx PlanContext, estimate *planEstimate) int64 {
	if estimate == nil || estimate.PointsPerSeries <= 0 {
		return 0
	}
	chunkPoints := nativeRangeDurationChunkPointLimit(ctx)
	if chunkPoints <= 0 {
		return 0
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

func nativeRangeDurationChunkPointLimit(ctx PlanContext) int64 {
	if ctx.NativeRangeChunkMaxDuration <= 0 || ctx.Step <= 0 {
		return 0
	}
	points := int64(ctx.NativeRangeChunkMaxDuration/ctx.Step) + 1
	if points < 1 {
		return 1
	}
	return points
}

func ceilDivInt64(n, d int64) int64 {
	if d <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
