package local

const (
	nativeRangeMemoryClassHighOverlapWindow = "high_overlap_window"
	nativeRangeMemoryClassLowMemoryGrid     = "low_memory_grid"
)

type nativeRangeChunkDecision struct {
	MemoryClass             string `json:"memoryClass,omitempty"`
	Policy                  string `json:"policy,omitempty"`
	Reason                  string `json:"reason,omitempty"`
	Chunked                 bool   `json:"chunked"`
	ChunkPointsPerSeries    int64  `json:"chunkPointsPerSeries,omitempty"`
	RequestedChunkPoints    int64  `json:"requestedChunkPointsPerSeries,omitempty"`
	DurationChunkPoints     int64  `json:"durationChunkPointsPerSeries,omitempty"`
	MaxChunks               int64  `json:"maxChunks,omitempty"`
	EstimatedRangePoints    int64  `json:"estimatedRangePointsPerSeries,omitempty"`
	PreflightThreshold      int64  `json:"preflightThreshold,omitempty"`
	PreflightTimeoutMS      int64  `json:"preflightTimeoutMs,omitempty"`
	PreflightMaxMemoryBytes int64  `json:"preflightMaxMemoryBytes,omitempty"`
	PreflightMatched        int64  `json:"preflightMatchedSeries,omitempty"`
	PreflightCapped         bool   `json:"preflightCapped,omitempty"`
	PreflightError          string `json:"preflightError,omitempty"`
}

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
		decision := decideNativeRangeChunking(chunkKind, ctx, estimate)
		annotateNativeRangeChunkDecision(plan, decision)
		if decision.Chunked {
			return &chunkedRangePlan{
				Child:                plan,
				ChunkPointsPerSeries: decision.ChunkPointsPerSeries,
				Reason:               decision.Reason,
				Estimate:             estimate,
				Decision:             decision,
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

func annotateNativeRangeChunkDecision(plan Plan, decision *nativeRangeChunkDecision) {
	nativePlan, ok := plan.(*nativeSubtreePlan)
	if !ok || nativePlan == nil || decision == nil {
		return
	}
	nativePlan.NativeRangeChunkDecision = decision
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

func decideNativeRangeChunking(kind string, ctx PlanContext, estimate *planEstimate) *nativeRangeChunkDecision {
	decision := &nativeRangeChunkDecision{
		MemoryClass:          nativeRangeMemoryClass(kind),
		Policy:               nativeRangeChunkPolicy(kind, ctx),
		RequestedChunkPoints: ctx.NativeRangeChunkPointsPerSeries,
		DurationChunkPoints:  nativeRangeDurationChunkPointLimit(ctx),
		MaxChunks:            ctx.NativeRangeChunkMaxChunks,
	}
	if decision.Policy == "bounded_series_preflight" {
		decision.PreflightThreshold = ctx.NativeRangePreflightSeriesThreshold
		decision.PreflightTimeoutMS = ctx.NativeRangePreflightTimeout.Milliseconds()
		decision.PreflightMaxMemoryBytes = ctx.NativeRangePreflightMaxMemoryUsage
	}
	if estimate != nil {
		decision.EstimatedRangePoints = estimate.PointsPerSeries
	}

	chunkPoints := nativeRangeChunkPointsPerSeries(ctx, estimate)
	if ctx.NativeRangeChunkPointsPerSeries == DefaultNativeRangeChunkPointsPerSeries {
		switch kind {
		case "cumulative_avg":
			// For high-overlap cumulative avg_over_time, two chunks gave the best
			// measured latency/memory compromise on realistic 24h/1m workloads.
			if estimate != nil {
				chunkPoints = max(chunkPoints, ceilDivInt64(estimate.PointsPerSeries, 2))
			}
		case "native_grid_sum_aggregation":
			// Native-grid sum aggregation is already memory-light for 24h/1m rate
			// ranges; default chunking should only enforce the duration cap.
			chunkPoints = nativeRangeDurationChunkPointsPerSeries(ctx, estimate)
		}
	}
	decision.ChunkPointsPerSeries = chunkPoints
	decision.Chunked = chunkPoints > 0
	decision.Reason = nativeRangeChunkDecisionReason(kind, decision)
	return decision
}

func nativeRangeMemoryClass(kind string) string {
	switch kind {
	case "cumulative_avg":
		return nativeRangeMemoryClassHighOverlapWindow
	case "native_grid_sum_aggregation":
		return nativeRangeMemoryClassLowMemoryGrid
	default:
		return "unknown_native_range"
	}
}

func nativeRangeChunkPolicy(kind string, ctx PlanContext) string {
	if ctx.NativeRangeChunkPointsPerSeries != DefaultNativeRangeChunkPointsPerSeries {
		return "explicit_chunk_points"
	}
	if kind == "native_grid_sum_aggregation" {
		return "duration_cap_only"
	}
	if kind == "cumulative_avg" && ctx.NativeRangePreflightSeriesThreshold > 0 {
		return "bounded_series_preflight"
	}
	return "default_memory_guardrail"
}

func nativeRangeChunkDecisionReason(kind string, decision *nativeRangeChunkDecision) string {
	if decision == nil {
		return ""
	}
	if !decision.Chunked {
		if decision.Policy == "explicit_chunk_points" {
			if decision.RequestedChunkPoints <= 0 {
				return "native range chunking disabled by explicit chunk point override"
			}
			return "explicit native range chunk point cap does not require chunking"
		}
		if kind == "native_grid_sum_aggregation" {
			return "native-grid sum aggregation is memory-light; default policy leaves it unchunked within duration cap"
		}
		return "native range chunking not needed for estimated range"
	}
	if kind == "cumulative_avg" {
		return "chunking cumulative avg_over_time range SQL to cap ClickHouse peak memory"
	}
	if kind == "native_grid_sum_aggregation" && decision.Policy == "duration_cap_only" {
		return "chunking native-grid sum aggregation because range exceeds duration cap"
	}
	return "chunking native range SQL to cap ClickHouse peak memory"
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
