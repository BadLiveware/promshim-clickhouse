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
	ClickHouseVersion               string
	NativeLoweringMode              NativeLoweringMode
	PreferNativeAggregationPushdown bool
	MaxRangePointsPerSeries         int64
	RangeChunkPointsPerSeries       int64
}

func (ctx planContext) allowsNativePlanning() bool {
	return normalizeNativeLoweringMode(ctx.NativeLoweringMode).enablesNativePlanning()
}

func defaultPlanContext(mode evalMode) planContext {
	return planContext{
		Mode:                            mode,
		ClickHouseVersion:               normalizeClickHouseVersion(""),
		NativeLoweringMode:              NativeLoweringModePrefer,
		PreferNativeAggregationPushdown: false,
		MaxRangePointsPerSeries:         defaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:       defaultRangeChunkPointsPerSeries,
	}
}
