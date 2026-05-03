package storage

import (
	"testing"
	"time"
)

func TestResolveDefaultSafeSettingsProfile(t *testing.T) {
	resolution := ResolveSettingsProfile(SettingsProfileConfig{
		Name:                SettingsProfileDefaultSafe,
		ClickHouseVersion:   "26.3",
		RequestTimeout:      1500 * time.Millisecond,
		MaxMemoryUsageBytes: 1234,
	}, QueryPurposeInstant, "selector_instant", "native_sql")

	if resolution.Explain.Name != SettingsProfileDefaultSafe {
		t.Fatalf("profile name = %q", resolution.Explain.Name)
	}
	if resolution.Explain.Family != "selector_instant" || resolution.Explain.Candidate != "native_sql" {
		t.Fatalf("profile association = family %q candidate %q", resolution.Explain.Family, resolution.Explain.Candidate)
	}
	checks := map[string]any{
		settingAllowTimeSeries:                 1,
		settingQuoteDenormals:                  1,
		settingMaxExecutionTime:                int64(2),
		settingTimeoutOverflowMode:             "throw",
		settingCancelHTTPReadonlyOnClientClose: 1,
		settingReadonly:                        2,
		settingMaxMemoryUsage:                  int64(1234),
	}
	for key, want := range checks {
		if got := resolution.Settings[key]; got != want {
			t.Fatalf("setting %s = %#v, want %#v", key, got, want)
		}
	}
	if !hasSkippedSetting(resolution.Explain.Skipped, settingMaxQuerySize, "not_configured") {
		t.Fatalf("expected not-configured query size skip, got %#v", resolution.Explain.Skipped)
	}
	if _, ok := resolution.Settings[settingUseQueryCache]; ok {
		t.Fatalf("query cache should not be applied by default_safe")
	}
	if !hasSkippedSetting(resolution.Explain.Skipped, settingUseQueryCache, "freshness_sensitive_not_default") {
		t.Fatalf("expected query-cache freshness skip, got %#v", resolution.Explain.Skipped)
	}
}

func TestRepeatedSelectiveProfileGatesConditionCache(t *testing.T) {
	oldVersion := ResolveSettingsProfile(SettingsProfileConfig{Name: SettingsProfileRepeatedSelective, ClickHouseVersion: "25.2", RequestTimeout: time.Second}, QueryPurposeInstant, "selector_instant", "native_sql")
	if !hasSkippedSetting(oldVersion.Explain.Skipped, settingUseQueryConditionCache, "version_unsupported") {
		t.Fatalf("expected version skip, got %#v", oldVersion.Explain.Skipped)
	}
	newVersion := ResolveSettingsProfile(SettingsProfileConfig{Name: SettingsProfileRepeatedSelective, ClickHouseVersion: "26.3", RequestTimeout: time.Second}, QueryPurposeInstant, "selector_instant", "native_sql")
	if !hasSkippedSetting(newVersion.Explain.Skipped, settingUseQueryConditionCache, "requires_measured_evidence") {
		t.Fatalf("expected evidence gate, got %#v", newVersion.Explain.Skipped)
	}
	if _, ok := newVersion.Settings[settingUseQueryConditionCache]; ok {
		t.Fatalf("condition cache should be named but not applied before evidence")
	}
}

func TestBenchmarkControlProfileBoundsThreads(t *testing.T) {
	resolution := ResolveSettingsProfile(SettingsProfileConfig{Name: SettingsProfileBenchmarkControl, ClickHouseVersion: "26.3", RequestTimeout: time.Second}, QueryPurposeRange, "range_1d", "native_sql")
	if got := resolution.Settings[settingMaxThreads]; got != benchmarkControlMaxThreads {
		t.Fatalf("max_threads = %#v, want %#v", got, benchmarkControlMaxThreads)
	}
	if !hasAppliedSetting(resolution.Explain.Applied, settingMaxThreads, "benchmark_variance_thread_bound") {
		t.Fatalf("expected max_threads applied provenance, got %#v", resolution.Explain.Applied)
	}
	if _, ok := resolution.Settings[settingUseQueryCache]; ok {
		t.Fatalf("benchmark_control must not enable result query cache")
	}
}

func TestDefaultSafeProfileCanRaiseQuerySize(t *testing.T) {
	resolution := ResolveSettingsProfile(SettingsProfileConfig{
		Name:                SettingsProfileDefaultSafe,
		ClickHouseVersion:   "26.3",
		RequestTimeout:      time.Second,
		MaxQuerySizeBytes:   1024 * 1024,
		MaxMemoryUsageBytes: 1234,
	}, QueryPurposeInstant, "selector_instant", "native_sql")

	if got := resolution.Settings[settingMaxQuerySize]; got != int64(1048576) {
		t.Fatalf("max_query_size = %#v, want %#v", got, int64(1048576))
	}
	if !hasAppliedSetting(resolution.Explain.Applied, settingMaxQuerySize, "safety_query_size_cap") {
		t.Fatalf("expected max_query_size applied provenance, got %#v", resolution.Explain.Applied)
	}
}

func TestMergeProfileSettingsRejectsUnknownSetting(t *testing.T) {
	_, err := MergeProfileSettings(map[string]any{settingReadonly: 2}, map[string]any{"unknown_setting": 1})
	if err == nil {
		t.Fatal("expected unknown setting error")
	}
}

func hasAppliedSetting(applied []AppliedSetting, name, reason string) bool {
	for _, setting := range applied {
		if setting.Name == name && setting.ReasonCode == reason {
			return true
		}
	}
	return false
}

func hasSkippedSetting(skipped []SkippedSetting, name, reason string) bool {
	for _, setting := range skipped {
		if setting.Name == name && setting.ReasonCode == reason {
			return true
		}
	}
	return false
}
