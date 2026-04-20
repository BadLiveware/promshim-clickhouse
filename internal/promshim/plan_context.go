package promshim

import (
	"time"
)

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

func defaultPlanContext(mode evalMode) planContext {
	return planContext{
		Mode:                            mode,
		PreferNativeAggregationPushdown: false,
		MaxRangePointsPerSeries:         defaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:       defaultRangeChunkPointsPerSeries,
	}
}
