package renderer

import (
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"

	"github.com/prometheus/prometheus/promql/parser"
)

type PhysicalPlanPreferences = physical.PlanPreferences
type ExecutionPreference = physical.ExecutionPreference
type ThreadPreferenceMode = physical.ThreadPreferenceMode
type ThreadCapPolicy = physical.ThreadCapPolicy
type ThreadPreference = physical.ThreadPreference
type RangeInstantSelectorPreference = physical.RangeInstantSelectorPreference
type RangeWindowAggregateStrategy = physical.RangeWindowAggregateStrategy
type RangeWindowAggregatePreference = physical.RangeWindowAggregatePreference

const (
	ThreadPreferenceDefault = physical.ThreadPreferenceDefault
	ThreadPreferenceSet     = physical.ThreadPreferenceSet
	ThreadPreferenceNoCap   = physical.ThreadPreferenceNoCap

	ThreadCapPolicyDefault           = physical.ThreadCapPolicyDefault
	ThreadCapPolicyASOFGuardrail     = physical.ThreadCapPolicyASOFGuardrail
	ThreadCapPolicyBenchmarkControl  = physical.ThreadCapPolicyBenchmarkControl
	ThreadCapPolicyManualMeasurement = physical.ThreadCapPolicyManualMeasurement

	RangeWindowAggregateStrategyDefault               = physical.RangeWindowAggregateStrategyDefault
	RangeWindowAggregateStrategyWindowJoin            = physical.RangeWindowAggregateStrategyWindowJoin
	RangeWindowAggregateStrategyDirectAggregate       = physical.RangeWindowAggregateStrategyDirectAggregate
	RangeWindowAggregateStrategySparseDirectAggregate = physical.RangeWindowAggregateStrategySparseDirectAggregate
	RangeWindowAggregateStrategyCumulativeAvg         = physical.RangeWindowAggregateStrategyCumulativeAvg
)

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

func preferRangeInstantSelectorStrategy(prefs PhysicalPlanPreferences, strategy storage.RangeInstantSelectorStrategy) PhysicalPlanPreferences {
	return physical.PreferRangeInstantSelectorStrategy(prefs, strategy)
}

func preferRangeWindowAggregateStrategy(prefs PhysicalPlanPreferences, strategy RangeWindowAggregateStrategy) PhysicalPlanPreferences {
	return physical.PreferRangeWindowAggregateStrategy(prefs, strategy)
}

type RenderedQuery struct {
	SQL               string
	QueryParams       map[string]string
	QuerySettings     map[string]any
	PhysicalDecisions []physical.Decision
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

func appendRenderedQueryPhysicalDecisions(dst []physical.Decision, src ...physical.Decision) []physical.Decision {
	for _, decision := range src {
		if decision.Kind == "" || decision.Strategy == "" {
			continue
		}
		dst = append(dst, decision)
	}
	return dst
}

func preferASOFThreadGuardrail(prefs PhysicalPlanPreferences, reasonCode string) PhysicalPlanPreferences {
	return physical.PreferASOFThreadGuardrail(prefs, reasonCode)
}

func preferThreadCapPolicy(prefs PhysicalPlanPreferences, policy ThreadCapPolicy, reasonCode string) PhysicalPlanPreferences {
	return physical.PreferThreadCapPolicy(prefs, policy, reasonCode)
}

func preferNoThreadCap(prefs PhysicalPlanPreferences, reasonCode string) PhysicalPlanPreferences {
	return physical.PreferNoThreadCap(prefs, reasonCode)
}

func physicalSettings(prefs PhysicalPlanPreferences) map[string]any {
	return physical.Settings(prefs)
}

func withPhysicalSettings(rq RenderedQuery, prefs PhysicalPlanPreferences) RenderedQuery {
	if decision, ok := physical.ThreadPreferenceDecision(prefs.Execution.Threads); ok {
		rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, decision)
	}
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
