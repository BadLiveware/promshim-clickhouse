package local

import (
	"time"
)

const (
	DefaultMaxRangePointsPerSeries   int64 = 50000
	DefaultRangeChunkPointsPerSeries int64 = 5000
)

type PlanContext struct {
	Mode                            EvalMode
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

func (ctx PlanContext) AllowsNativePlanning() bool {
	return NormalizeNativeLoweringMode(ctx.NativeLoweringMode).EnablesNativePlanning()
}

func DefaultPlanContext(mode EvalMode) PlanContext {
	return PlanContext{
		Mode:                            mode,
		ClickHouseVersion:               NormalizeClickHouseVersion(""),
		NativeLoweringMode:              NativeLoweringModePrefer,
		PreferNativeAggregationPushdown: false,
		MaxRangePointsPerSeries:         DefaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:       DefaultRangeChunkPointsPerSeries,
	}
}
