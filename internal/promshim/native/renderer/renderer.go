package renderer

import (
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"

	"github.com/prometheus/prometheus/promql/parser"
)

type PhysicalPlanPreferences struct {
	RangeInstantSelector RangeInstantSelectorPreference
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

type RenderParams struct {
	Mode                native.RenderMode
	EvaluationTimeMS    int64
	StartMS             int64
	EndMS               int64
	StepMS              int64
	RequiredStartMS     int64
	RequiredEndMS       int64
	ResolveSourcePromQL func(parser.Expr) (string, error)
	// RequireFullTags indicates whether a parent renderer (histogram function or
	// projection, or a selection / count_values aggregation) has explicitly
	// declared that full tags are required from the underlying selector.
	// After 13c-14e native.SelectorSource no longer carries narrowing state,
	// so RenderParams is the single source of truth: true forces the storage
	// SelectorSource's RequireFullTags=true, overriding any
	// RequiredTagLabels narrowing. Default (false) means the storage
	// selector's own (zero-valued) narrowing state applies.
	RequireFullTags bool
	// RequiredTagLabels is the set of labels the parent requires from the
	// underlying selector. When RequireFullTags is false (indicating a grouping
	// aggregation child), this is a fresh copy of the child's Grouping labels.
	// Default (nil) means no explicit tag requirement from the parent; the
	// storage selector falls through to the full-tags base path.
	RequiredTagLabels []string
	Physical          PhysicalPlanPreferences
}

func physicalPreferencesForRangeInstantSelectorStrategy(strategy storage.RangeInstantSelectorStrategy) PhysicalPlanPreferences {
	return preferRangeInstantSelectorStrategy(PhysicalPlanPreferences{}, strategy)
}

func preferRangeInstantSelectorStrategy(prefs PhysicalPlanPreferences, strategy storage.RangeInstantSelectorStrategy) PhysicalPlanPreferences {
	prefs.RangeInstantSelector = RangeInstantSelectorPreference{Strategy: strategy}
	return prefs
}

type RenderedQuery struct {
	SQL           string
	QueryParams   map[string]string
	QuerySettings map[string]any
}

func mergeRenderedQueryParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func mergeRenderedQuerySettings(dst, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}

func preferASOFThreadGuardrail(prefs PhysicalPlanPreferences, reasonCode string) PhysicalPlanPreferences {
	return preferThreadCapPolicy(prefs, ThreadCapPolicyASOFGuardrail, reasonCode)
}

func preferThreadCapPolicy(prefs PhysicalPlanPreferences, policy ThreadCapPolicy, reasonCode string) PhysicalPlanPreferences {
	if prefs.Execution.Threads.Mode == ThreadPreferenceNoCap {
		return prefs
	}
	maxThreads, ok := threadCapPolicyMaxThreads(policy)
	if !ok {
		return prefs
	}
	prefs.Execution.Threads = ThreadPreference{Mode: ThreadPreferenceSet, Policy: policy, MaxThreads: maxThreads, ReasonCode: reasonCode}
	return prefs
}

func preferNoThreadCap(prefs PhysicalPlanPreferences, reasonCode string) PhysicalPlanPreferences {
	prefs.Execution.Threads = ThreadPreference{Mode: ThreadPreferenceNoCap, ReasonCode: reasonCode}
	return prefs
}

func threadCapPolicyMaxThreads(policy ThreadCapPolicy) (int, bool) {
	switch policy {
	case ThreadCapPolicyASOFGuardrail, ThreadCapPolicyBenchmarkControl, ThreadCapPolicyManualMeasurement:
		return 4, true
	default:
		return 0, false
	}
}

func physicalSettings(prefs PhysicalPlanPreferences) map[string]any {
	threads := prefs.Execution.Threads
	if threads.Mode != ThreadPreferenceSet || threads.MaxThreads <= 0 {
		return nil
	}
	return map[string]any{"max_threads": threads.MaxThreads}
}

func withPhysicalSettings(rq RenderedQuery, prefs PhysicalPlanPreferences) RenderedQuery {
	settings := physicalSettings(prefs)
	if len(settings) == 0 {
		return rq
	}
	merged := map[string]any{}
	mergeRenderedQuerySettings(merged, rq.QuerySettings)
	mergeRenderedQuerySettings(merged, settings)
	rq.QuerySettings = merged
	return rq
}

func trimRenderedQuerySQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if idx := strings.LastIndex(sql, schema.SettingsLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	if idx := strings.LastIndex(sql, schema.FormatLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	return sql
}
