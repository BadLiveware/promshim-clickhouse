package storage

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	SettingsProfileNone              = "none"
	SettingsProfileDefaultSafe       = "default_safe"
	SettingsProfileRepeatedSelective = "repeated_selective"
	SettingsProfileTinyInstant       = "tiny_instant"
	SettingsProfileSimpleRange       = "simple_range"
	SettingsProfileLongRangeScan     = "long_range_scan"
	SettingsProfileAggregationHeavy  = "aggregation_heavy"
	SettingsProfileJoinHeavy         = "join_heavy"
	SettingsProfileSubtreePushdown   = "subtree_pushdown"
	SettingsProfileBenchmarkControl  = "benchmark_control"
)

const (
	benchmarkControlMaxThreads = 4

	settingAllowTimeSeries                 = "allow_experimental_time_series_table"
	settingQuoteDenormals                  = "output_format_json_quote_denormals"
	settingAsyncInsert                     = "async_insert"
	settingMaxExecutionTime                = "max_execution_time"
	settingTimeoutOverflowMode             = "timeout_overflow_mode"
	settingCancelHTTPReadonlyOnClientClose = "cancel_http_readonly_queries_on_client_close"
	settingReadonly                        = "readonly"
	settingMaxQuerySize                    = "max_query_size"
	settingMaxMemoryUsage                  = "max_memory_usage"
	settingMaxRowsToRead                   = "max_rows_to_read"
	settingMaxResultRows                   = "max_result_rows"
	settingUseQueryConditionCache          = "use_query_condition_cache"
	settingUseQueryCache                   = "use_query_cache"
	settingMaxThreads                      = "max_threads"
)

var supportedSettingsProfiles = map[string]struct{}{
	SettingsProfileNone:              {},
	SettingsProfileDefaultSafe:       {},
	SettingsProfileRepeatedSelective: {},
	SettingsProfileTinyInstant:       {},
	SettingsProfileSimpleRange:       {},
	SettingsProfileLongRangeScan:     {},
	SettingsProfileAggregationHeavy:  {},
	SettingsProfileJoinHeavy:         {},
	SettingsProfileSubtreePushdown:   {},
	SettingsProfileBenchmarkControl:  {},
}

var allowlistedSettings = map[string]struct{}{
	settingAllowTimeSeries:                 {},
	settingQuoteDenormals:                  {},
	settingAsyncInsert:                     {},
	settingMaxExecutionTime:                {},
	settingTimeoutOverflowMode:             {},
	settingCancelHTTPReadonlyOnClientClose: {},
	settingReadonly:                        {},
	settingMaxQuerySize:                    {},
	settingMaxMemoryUsage:                  {},
	settingMaxRowsToRead:                   {},
	settingMaxResultRows:                   {},
	settingUseQueryConditionCache:          {},
	settingUseQueryCache:                   {},
	settingMaxThreads:                      {},
}

// SettingsProfileConfig describes the bounded set of ClickHouse settings that
// promshim may add to its own statements. Zero-valued caps are intentionally
// treated as absent so safety limits can be introduced without silently changing
// route semantics for existing installations.
type SettingsProfileConfig struct {
	Name                string
	ClickHouseVersion   string
	RequestTimeout      time.Duration
	MaxQuerySizeBytes   int64
	MaxMemoryUsageBytes int64
	MaxRowsToRead       int64
	MaxResultRows       int64
}

type AppliedSetting struct {
	Name       string `json:"name"`
	Value      any    `json:"value"`
	ReasonCode string `json:"reasonCode"`
	Scope      string `json:"scope"`
	MinVersion string `json:"minVersion,omitempty"`
}

type SkippedSetting struct {
	Name       string `json:"name"`
	ReasonCode string `json:"reasonCode"`
	Scope      string `json:"scope"`
	MinVersion string `json:"minVersion,omitempty"`
}

type SettingsProfileExplain struct {
	Name        string           `json:"name"`
	Family      string           `json:"family,omitempty"`
	Candidate   string           `json:"candidate,omitempty"`
	Applied     []AppliedSetting `json:"applied,omitempty"`
	Skipped     []SkippedSetting `json:"skipped,omitempty"`
	ReasonCodes []string         `json:"reasonCodes,omitempty"`
}

type SettingsProfileResolution struct {
	Explain  SettingsProfileExplain
	Settings map[string]any
}

func NormalizeSettingsProfileName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return SettingsProfileDefaultSafe
	}
	if _, ok := supportedSettingsProfiles[name]; ok {
		return name
	}
	return SettingsProfileDefaultSafe
}

func ParseSettingsProfileName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return SettingsProfileDefaultSafe, nil
	}
	if _, ok := supportedSettingsProfiles[name]; ok {
		return name, nil
	}
	return "", fmt.Errorf("unsupported ClickHouse settings profile %q", raw)
}

func ResolveSettingsProfile(cfg SettingsProfileConfig, purpose QueryPurpose, family, candidate string) SettingsProfileResolution {
	cfg.Name = NormalizeSettingsProfileName(cfg.Name)
	resolution := SettingsProfileResolution{
		Explain:  SettingsProfileExplain{Name: cfg.Name, Family: family, Candidate: candidate},
		Settings: map[string]any{},
	}
	add := func(name string, value any, reasonCode, minVersion string) {
		resolution.Settings[name] = value
		resolution.Explain.Applied = append(resolution.Explain.Applied, AppliedSetting{Name: name, Value: value, ReasonCode: reasonCode, Scope: "query", MinVersion: minVersion})
		resolution.Explain.ReasonCodes = appendUnique(resolution.Explain.ReasonCodes, reasonCode)
	}
	skip := func(name, reasonCode, minVersion string) {
		resolution.Explain.Skipped = append(resolution.Explain.Skipped, SkippedSetting{Name: name, ReasonCode: reasonCode, Scope: "query", MinVersion: minVersion})
		resolution.Explain.ReasonCodes = appendUnique(resolution.Explain.ReasonCodes, reasonCode)
	}

	add(settingAllowTimeSeries, 1, "required_time_series_engine", "")
	add(settingQuoteDenormals, 1, "preserve_json_nan_inf", "")
	if cfg.Name == SettingsProfileNone {
		return resolution
	}

	if seconds := maxExecutionSeconds(cfg.RequestTimeout); seconds > 0 {
		add(settingMaxExecutionTime, seconds, "safety_timeout", "")
		add(settingTimeoutOverflowMode, "throw", "safety_timeout", "")
	} else {
		skip(settingMaxExecutionTime, "not_configured", "")
	}
	add(settingCancelHTTPReadonlyOnClientClose, 1, "cancel_on_client_close", "")
	add(settingReadonly, 2, "read_only_query_scope", "")

	if cfg.MaxQuerySizeBytes > 0 {
		add(settingMaxQuerySize, cfg.MaxQuerySizeBytes, "safety_query_size_cap", "")
	} else {
		skip(settingMaxQuerySize, "not_configured", "")
	}
	if cfg.MaxMemoryUsageBytes > 0 {
		add(settingMaxMemoryUsage, cfg.MaxMemoryUsageBytes, "safety_memory_cap", "")
	} else {
		skip(settingMaxMemoryUsage, "not_configured", "")
	}
	if cfg.MaxRowsToRead > 0 {
		add(settingMaxRowsToRead, cfg.MaxRowsToRead, "safety_read_cap", "")
	} else {
		skip(settingMaxRowsToRead, "requires_estimate_cap", "")
	}
	if cfg.MaxResultRows > 0 {
		add(settingMaxResultRows, cfg.MaxResultRows, "safety_result_cap", "")
	} else {
		skip(settingMaxResultRows, "requires_result_contract", "")
	}

	switch cfg.Name {
	case SettingsProfileRepeatedSelective:
		if clickHouseVersionAtLeast(cfg.ClickHouseVersion, "25.3") {
			skip(settingUseQueryConditionCache, "requires_measured_evidence", "25.3")
		} else {
			skip(settingUseQueryConditionCache, "version_unsupported", "25.3")
		}
	case SettingsProfileBenchmarkControl:
		add(settingMaxThreads, benchmarkControlMaxThreads, "benchmark_variance_thread_bound", "")
		skip(settingUseQueryConditionCache, "profile_gate_empty_until_measured", "25.3")
	case SettingsProfileTinyInstant, SettingsProfileSimpleRange, SettingsProfileLongRangeScan, SettingsProfileAggregationHeavy, SettingsProfileJoinHeavy, SettingsProfileSubtreePushdown:
		skip(settingUseQueryConditionCache, "profile_gate_empty_until_measured", "25.3")
	}

	// Result query cache is deliberately visible as rejected provenance because it
	// can violate PromQL freshness expectations and hide work during optimization
	// measurements.
	skip(settingUseQueryCache, "freshness_sensitive_not_default", "")
	_ = purpose
	return resolution
}

func MergeProfileSettings(settings map[string]any, overrides map[string]any) (map[string]any, error) {
	merged := make(map[string]any, len(settings)+len(overrides))
	for key, value := range settings {
		merged[key] = value
	}
	for key, value := range overrides {
		if _, ok := allowlistedSettings[key]; !ok {
			return nil, fmt.Errorf("ClickHouse setting %q is not in promshim allowlist", key)
		}
		merged[key] = value
	}
	return merged, nil
}

func maxExecutionSeconds(timeout time.Duration) int64 {
	if timeout <= 0 {
		return 0
	}
	return int64(math.Ceil(timeout.Seconds()))
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func clickHouseVersionAtLeast(version, minimum string) bool {
	return compareVersionParts(version, minimum) >= 0
}

func compareVersionParts(left, right string) int {
	lp := parseVersionParts(left)
	rp := parseVersionParts(right)
	maxLen := len(lp)
	if len(rp) > maxLen {
		maxLen = len(rp)
	}
	for i := 0; i < maxLen; i++ {
		lv, rv := 0, 0
		if i < len(lp) {
			lv = lp[i]
		}
		if i < len(rp) {
			rv = rp[i]
		}
		if lv < rv {
			return -1
		}
		if lv > rv {
			return 1
		}
	}
	return 0
}

func parseVersionParts(raw string) []int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "26.3"
	}
	parts := strings.Split(trimmed, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, value)
	}
	return out
}
