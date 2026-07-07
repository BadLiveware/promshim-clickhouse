package local

import (
	"time"
)

const (
	DefaultMaxRangePointsPerSeries             int64 = 50000
	DefaultRangeChunkPointsPerSeries           int64 = 5000
	DefaultNativeRangeChunkPointsPerSeries     int64 = 289
	DefaultNativeRangeChunkMaxDuration               = 24 * time.Hour
	DefaultNativeRangeChunkMaxChunks           int64 = 12
	DefaultNativeRangePreflightSeriesThreshold int64 = 1000
	DefaultNativeRangePreflightTimeout               = 50 * time.Millisecond
	DefaultNativeRangePreflightMaxMemoryUsage  int64 = 64 << 20
	// DefaultEvaluationInterval mirrors Prometheus's
	// --query.default-evaluation-interval default. It fills the step of
	// subqueries that omit one (`expr[15m:]`) and is a server-side
	// constant: it must never depend on the request's step parameter.
	DefaultEvaluationInterval = time.Minute
)

type PlanContext struct {
	Mode                                EvalMode
	EvaluationTime                      time.Time
	Start                               time.Time
	End                                 time.Time
	Step                                time.Duration
	ClickHouseVersion                   string
	NativeLoweringMode                  NativeLoweringMode
	PreferNativeAggregationPushdown     bool
	EnableNativeGridFunctions           bool
	EnableCumulativeAvgOverTime         bool
	DefaultEvaluationInterval           time.Duration
	MaxRangePointsPerSeries             int64
	RangeChunkPointsPerSeries           int64
	NativeRangeChunkPointsPerSeries     int64
	NativeRangeChunkMaxDuration         time.Duration
	NativeRangeChunkMaxChunks           int64
	NativeRangePreflightSeriesThreshold int64
	NativeRangePreflightTimeout         time.Duration
	NativeRangePreflightMaxMemoryUsage  int64
	NativeSubtreeRenderTagHint          bool
	NativeSubtreeRequireFullTags        bool
	NativeSubtreeRequiredTagLabels      []string
	nativePlanningDepth                 int
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
		Mode:                                mode,
		ClickHouseVersion:                   NormalizeClickHouseVersion(""),
		NativeLoweringMode:                  NativeLoweringModePrefer,
		PreferNativeAggregationPushdown:     false,
		DefaultEvaluationInterval:           DefaultEvaluationInterval,
		MaxRangePointsPerSeries:             DefaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:           DefaultRangeChunkPointsPerSeries,
		NativeRangeChunkPointsPerSeries:     DefaultNativeRangeChunkPointsPerSeries,
		NativeRangeChunkMaxDuration:         DefaultNativeRangeChunkMaxDuration,
		NativeRangeChunkMaxChunks:           DefaultNativeRangeChunkMaxChunks,
		NativeRangePreflightSeriesThreshold: DefaultNativeRangePreflightSeriesThreshold,
		NativeRangePreflightTimeout:         DefaultNativeRangePreflightTimeout,
		NativeRangePreflightMaxMemoryUsage:  DefaultNativeRangePreflightMaxMemoryUsage,
	}
}
