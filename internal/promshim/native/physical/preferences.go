package physical

import "github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"

// PlanPreferences carries physical-shape requests from parent renderers to the
// lower-level builders that validate and emit the corresponding SQL.
type PlanPreferences struct {
	RangeInstantSelector RangeInstantSelectorPreference
	RangeWindowAggregate RangeWindowAggregatePreference
	Execution            ExecutionPreference
}

// ExecutionPreference carries whole-query ClickHouse execution preferences.
// Unlike selector strategy preferences, these settings apply to the entire
// rendered SQL statement, so composite parent shapes can explicitly suppress a
// child preference that would be harmful for the final query.
type ExecutionPreference struct {
	Threads ThreadPreference
}

type ThreadPreferenceMode string

const (
	ThreadPreferenceDefault ThreadPreferenceMode = ""
	ThreadPreferenceSet     ThreadPreferenceMode = "set"
	ThreadPreferenceNoCap   ThreadPreferenceMode = "no_cap"
)

type ThreadCapPolicy string

const (
	ThreadCapPolicyDefault           ThreadCapPolicy = ""
	ThreadCapPolicyASOFGuardrail     ThreadCapPolicy = "asof_guardrail"
	ThreadCapPolicyBenchmarkControl  ThreadCapPolicy = "benchmark_control"
	ThreadCapPolicyManualMeasurement ThreadCapPolicy = "manual_measurement"
)

const (
	ThreadPreferenceReasonDirectRangeAggregation = "direct_range_aggregation_cpu_guardrail"
	ThreadPreferenceReasonFusedRateAggregation   = "fused_rate_aggregation_cpu_guardrail"
	ThreadPreferenceReasonSubqueryRateRows       = "subquery_rate_over_aggregate_regresses_with_thread_cap"
)

type ThreadPreference struct {
	Mode       ThreadPreferenceMode
	Policy     ThreadCapPolicy
	MaxThreads int
	ReasonCode string
}

type RangeInstantSelectorPreference struct {
	// Strategy lets parent renderers request a ClickHouse physical shape for
	// range queries over instant selectors. The storage layer validates
	// eligibility and falls back to ASOF when the requested shape is not safe for
	// the selector timing.
	Strategy storage.RangeInstantSelectorStrategy
}

type RangeWindowAggregatePreference struct {
	// Strategy lets parent renderers request the physical shape for range-window
	// aggregate evaluation over selector-backed range functions.
	Strategy RangeWindowAggregateStrategy
}

func PreferRangeInstantSelectorStrategy(prefs PlanPreferences, strategy storage.RangeInstantSelectorStrategy) PlanPreferences {
	prefs.RangeInstantSelector = RangeInstantSelectorPreference{Strategy: strategy}
	return prefs
}

func PreferRangeWindowAggregateStrategy(prefs PlanPreferences, strategy RangeWindowAggregateStrategy) PlanPreferences {
	prefs.RangeWindowAggregate = RangeWindowAggregatePreference{Strategy: strategy}
	return prefs
}

func PreferASOFThreadGuardrail(prefs PlanPreferences, reasonCode string) PlanPreferences {
	return PreferThreadCapPolicy(prefs, ThreadCapPolicyASOFGuardrail, reasonCode)
}

func PreferThreadCapPolicy(prefs PlanPreferences, policy ThreadCapPolicy, reasonCode string) PlanPreferences {
	if prefs.Execution.Threads.Mode == ThreadPreferenceNoCap {
		return prefs
	}
	maxThreads, ok := ThreadCapPolicyMaxThreads(policy)
	if !ok {
		return prefs
	}
	prefs.Execution.Threads = ThreadPreference{Mode: ThreadPreferenceSet, Policy: policy, MaxThreads: maxThreads, ReasonCode: reasonCode}
	return prefs
}

func PreferNoThreadCap(prefs PlanPreferences, reasonCode string) PlanPreferences {
	prefs.Execution.Threads = ThreadPreference{Mode: ThreadPreferenceNoCap, ReasonCode: reasonCode}
	return prefs
}

func ThreadCapPolicyMaxThreads(policy ThreadCapPolicy) (int, bool) {
	switch policy {
	case ThreadCapPolicyASOFGuardrail, ThreadCapPolicyBenchmarkControl, ThreadCapPolicyManualMeasurement:
		return 4, true
	default:
		return 0, false
	}
}

func Settings(prefs PlanPreferences) map[string]any {
	threads := prefs.Execution.Threads
	if threads.Mode != ThreadPreferenceSet || threads.MaxThreads <= 0 {
		return nil
	}
	return map[string]any{"max_threads": threads.MaxThreads}
}
