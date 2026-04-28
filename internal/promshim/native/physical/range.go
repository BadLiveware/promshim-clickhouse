package physical

import "github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"

type Alternative struct {
	Strategy string `json:"strategy"`
	Reason   string `json:"reason,omitempty"`
}

type Decision struct {
	Kind     string        `json:"kind"`
	Strategy string        `json:"strategy"`
	Reason   string        `json:"reason,omitempty"`
	Guards   []string      `json:"guards,omitempty"`
	Rejected []Alternative `json:"rejected,omitempty"`
}

type RangeInstantSelectorInput struct {
	RequestedStrategy storage.RangeInstantSelectorStrategy
	SelectorKind      storage.SelectorKind
	LookbackMS        int64
	StepMS            int64
}

type RangeInstantSelectorDecision struct {
	Strategy storage.RangeInstantSelectorStrategy
	Reason   string
	Guards   []string
	Rejected []Alternative
}

func ChooseRangeInstantSelector(input RangeInstantSelectorInput) RangeInstantSelectorDecision {
	if input.RequestedStrategy == storage.RangeInstantSelectorStrategyBucketedArgMax {
		if input.SelectorKind == storage.SelectorKindInstantVector && CanUseSparseRangeInstantSelectorSamples(input.LookbackMS, input.StepMS) {
			return RangeInstantSelectorDecision{
				Strategy: storage.RangeInstantSelectorStrategyBucketedArgMax,
				Reason:   "requested bucketed argMax is eligible for sparse instant-selector timing",
				Guards:   []string{"requested_bucketed_argmax", "instant_vector_selector", "sparse_step_phase_filter"},
			}
		}
		return RangeInstantSelectorDecision{
			Strategy: storage.RangeInstantSelectorStrategyASOFJoin,
			Reason:   "requested bucketed argMax is not eligible; falling back to ASOF join",
			Guards:   []string{"asof_default"},
			Rejected: []Alternative{{Strategy: string(storage.RangeInstantSelectorStrategyBucketedArgMax), Reason: "requires instant-vector selector with step greater than lookback"}},
		}
	}
	return RangeInstantSelectorDecision{
		Strategy: storage.RangeInstantSelectorStrategyASOFJoin,
		Reason:   "ASOF join is the default range instant selector strategy",
		Guards:   []string{"asof_default"},
	}
}

func CanUseSparseRangeInstantSelectorSamples(lookbackMS, stepMS int64) bool {
	return lookbackMS > 0 && stepMS > lookbackMS
}

type RangeWindowAggregateStrategy string

const (
	RangeWindowAggregateStrategyDefault               RangeWindowAggregateStrategy = ""
	RangeWindowAggregateStrategyWindowJoin            RangeWindowAggregateStrategy = "window_join"
	RangeWindowAggregateStrategyDirectAggregate       RangeWindowAggregateStrategy = "direct_aggregate"
	RangeWindowAggregateStrategySparseDirectAggregate RangeWindowAggregateStrategy = "sparse_direct_aggregate"
	RangeWindowAggregateStrategyCumulativeAvg         RangeWindowAggregateStrategy = "cumulative_avg"
)

type RangeWindowAggregateInput struct {
	Func                        string
	LookbackMS                  int64
	OffsetMS                    int64
	StepMS                      int64
	EnableCumulativeAvgOverTime bool
	Preferences                 PlanPreferences
}

type RangeWindowAggregateDecision struct {
	Strategy RangeWindowAggregateStrategy
	Reason   string
	Guards   []string
	Rejected []Alternative
}

func ChooseRangeWindowAggregate(input RangeWindowAggregateInput) RangeWindowAggregateDecision {
	switch input.Preferences.RangeWindowAggregate.Strategy {
	case RangeWindowAggregateStrategyWindowJoin:
		return RangeWindowAggregateDecision{
			Strategy: RangeWindowAggregateStrategyWindowJoin,
			Reason:   "explicit range-window aggregate preference requested window join",
			Guards:   []string{"preference_window_join"},
		}
	case RangeWindowAggregateStrategyDirectAggregate, RangeWindowAggregateStrategySparseDirectAggregate:
		if SupportsDirectSelectorWindowAggregate(input.Func) {
			if CanUseSparseDirectAggregateBuckets(input.Func, input.LookbackMS, input.OffsetMS, input.StepMS) {
				return RangeWindowAggregateDecision{
					Strategy: RangeWindowAggregateStrategySparseDirectAggregate,
					Reason:   "explicit range-window aggregate preference requested direct aggregate and sparse buckets are eligible",
					Guards:   []string{"preference_direct_aggregate", "function_supports_direct_aggregate", "zero_offset", "non_overlap_window"},
				}
			}
			return RangeWindowAggregateDecision{
				Strategy: RangeWindowAggregateStrategyDirectAggregate,
				Reason:   "explicit range-window aggregate preference requested direct aggregate",
				Guards:   []string{"preference_direct_aggregate", "function_supports_direct_aggregate"},
			}
		}
	case RangeWindowAggregateStrategyCumulativeAvg:
		if input.EnableCumulativeAvgOverTime && input.Func == "avg_over_time" {
			return RangeWindowAggregateDecision{
				Strategy: RangeWindowAggregateStrategyCumulativeAvg,
				Reason:   "explicit range-window aggregate preference requested cumulative avg",
				Guards:   []string{"preference_cumulative_avg", "cumulative_avg_enabled", "avg_over_time"},
			}
		}
	}

	var rejected []Alternative
	if (input.Preferences.RangeWindowAggregate.Strategy == RangeWindowAggregateStrategyDirectAggregate || input.Preferences.RangeWindowAggregate.Strategy == RangeWindowAggregateStrategySparseDirectAggregate) && !SupportsDirectSelectorWindowAggregate(input.Func) {
		rejected = append(rejected, Alternative{Strategy: string(RangeWindowAggregateStrategyDirectAggregate), Reason: "function does not support direct selector-window aggregate"})
	}
	if input.Preferences.RangeWindowAggregate.Strategy == RangeWindowAggregateStrategyCumulativeAvg && !(input.EnableCumulativeAvgOverTime && input.Func == "avg_over_time") {
		rejected = append(rejected, Alternative{Strategy: string(RangeWindowAggregateStrategyCumulativeAvg), Reason: "requires enabled avg_over_time cumulative path"})
	}

	lowOverlap := PreferDirectSelectorWindowJoin(input.LookbackMS, input.StepMS)
	if input.EnableCumulativeAvgOverTime && input.Func == "avg_over_time" && !lowOverlap {
		return RangeWindowAggregateDecision{
			Strategy: RangeWindowAggregateStrategyCumulativeAvg,
			Reason:   "enabled avg_over_time cumulative path is preferred for high-overlap windows",
			Guards:   []string{"cumulative_avg_enabled", "avg_over_time", "high_overlap_window"},
			Rejected: rejected,
		}
	}
	if SupportsDirectSelectorWindowAggregate(input.Func) && PreferDirectSelectorWindowAggregate(input.Func, input.LookbackMS, input.StepMS) {
		if CanUseSparseDirectAggregateBuckets(input.Func, input.LookbackMS, input.OffsetMS, input.StepMS) {
			return RangeWindowAggregateDecision{
				Strategy: RangeWindowAggregateStrategySparseDirectAggregate,
				Reason:   "function and timing are eligible for sparse direct selector-window aggregation",
				Guards:   []string{"function_supports_direct_aggregate", "zero_offset", "non_overlap_window"},
				Rejected: rejected,
			}
		}
		return RangeWindowAggregateDecision{
			Strategy: RangeWindowAggregateStrategyDirectAggregate,
			Reason:   "function and timing are eligible for direct selector-window aggregation",
			Guards:   []string{"function_supports_direct_aggregate", "direct_aggregate_timing"},
			Rejected: rejected,
		}
	}
	if lowOverlap {
		return RangeWindowAggregateDecision{
			Strategy: RangeWindowAggregateStrategyWindowJoin,
			Reason:   "low-overlap window is eligible for direct window join",
			Guards:   []string{"low_overlap_window"},
			Rejected: rejected,
		}
	}
	return RangeWindowAggregateDecision{
		Strategy: RangeWindowAggregateStrategyDefault,
		Reason:   "fall back to existing windowed-array range function evaluation",
		Guards:   []string{"windowed_array_fallback"},
		Rejected: rejected,
	}
}

func PreferDirectSelectorWindowJoin(lookbackMS, stepMS int64) bool {
	if lookbackMS <= 0 || stepMS <= 0 {
		return false
	}
	// The direct window-join path duplicates raw points into every overlapping
	// step bucket. That is a good trade when overlap is shallow, but high fan-out
	// stays on the older materialize-then-window path.
	overlapSlots := ((lookbackMS + stepMS - 1) / stepMS) + 1
	return overlapSlots <= 4
}

func PreferDirectSelectorWindowAggregate(fn string, lookbackMS, stepMS int64) bool {
	if lookbackMS <= 0 || stepMS <= 0 {
		return false
	}
	if fn == "avg_over_time" {
		return true
	}
	if fn == "max_over_time" {
		return lookbackMS <= stepMS
	}
	return PreferDirectSelectorWindowJoin(lookbackMS, stepMS)
}

func SupportsDirectSelectorWindowAggregate(fn string) bool {
	switch fn {
	case "avg_over_time", "max_over_time":
		return true
	default:
		return false
	}
}

func CanUseSparseDirectAggregateBuckets(fn string, lookbackMS, offsetMS, stepMS int64) bool {
	switch fn {
	case "avg_over_time", "max_over_time", "rate":
		return lookbackMS > 0 && stepMS > 0 && offsetMS == 0 && lookbackMS <= stepMS
	default:
		return false
	}
}

func CanUseSparseDirectRateBuckets(fn string, lookbackMS, offsetMS, stepMS int64) bool {
	return fn == "rate" && lookbackMS > 0 && stepMS > 0 && offsetMS == 0 && lookbackMS <= stepMS
}

func CanUseNativeGridRangeFunction(fn string, lookbackMS, offsetMS int64) bool {
	if lookbackMS <= 0 || offsetMS != 0 {
		return false
	}
	switch fn {
	case "rate", "irate", "delta", "idelta", "last_over_time":
		// Very short windows have compliance-sensitive empty-window behavior in
		// Prometheus. Keep them on promshim's SQL kernel until targeted fixtures
		// prove ClickHouse's grid functions are identical there too.
		return lookbackMS >= 60_000
	default:
		return false
	}
}
