package local

import (
	"time"
)

const (
	DefaultMaxRangePointsPerSeries         int64 = 50000
	DefaultRangeChunkPointsPerSeries       int64 = 5000
	DefaultNativeRangeChunkPointsPerSeries int64 = 289
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
	EnableNativeGridFunctions       bool
	EnableCumulativeAvgOverTime     bool
	MaxRangePointsPerSeries         int64
	RangeChunkPointsPerSeries       int64
	NativeRangeChunkPointsPerSeries int64
	NativeSubtreeRenderTagHint      bool
	NativeSubtreeRequireFullTags    bool
	NativeSubtreeRequiredTagLabels  []string
	nativePlanningDepth             int
}

func (ctx PlanContext) AllowsNativePlanning() bool {
	mode := NormalizeNativeLoweringMode(ctx.NativeLoweringMode)
	if !mode.EnablesNativePlanning() {
		return false
	}
	return !mode.ForcesLocalRoot() || ctx.nativePlanningDepth > 1
}

func DefaultPlanContext(mode EvalMode) PlanContext {
	return PlanContext{
		Mode:                            mode,
		ClickHouseVersion:               NormalizeClickHouseVersion(""),
		NativeLoweringMode:              NativeLoweringModePrefer,
		PreferNativeAggregationPushdown: false,
		MaxRangePointsPerSeries:         DefaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:       DefaultRangeChunkPointsPerSeries,
		NativeRangeChunkPointsPerSeries: DefaultNativeRangeChunkPointsPerSeries,
	}
}
