package renderer

import "testing"

func TestThreadCapPoliciesResolveKnownValues(t *testing.T) {
	prefs := preferThreadCapPolicy(PhysicalPlanPreferences{}, ThreadCapPolicyASOFGuardrail, "asof_cpu_guardrail")
	settings := physicalSettings(prefs)
	if got := settings["max_threads"]; got != 4 {
		t.Fatalf("max_threads = %#v, want 4", got)
	}
	if prefs.Execution.Threads.Mode != ThreadPreferenceSet {
		t.Fatalf("thread mode = %q, want set", prefs.Execution.Threads.Mode)
	}
	if prefs.Execution.Threads.Policy != ThreadCapPolicyASOFGuardrail {
		t.Fatalf("thread policy = %q, want %q", prefs.Execution.Threads.Policy, ThreadCapPolicyASOFGuardrail)
	}
	if prefs.Execution.Threads.ReasonCode != "asof_cpu_guardrail" {
		t.Fatalf("reason = %q", prefs.Execution.Threads.ReasonCode)
	}
}

func TestNoThreadCapSuppressesLaterThreadCapPolicy(t *testing.T) {
	prefs := preferNoThreadCap(PhysicalPlanPreferences{}, "subquery_rate_over_aggregate_regresses")
	prefs = preferThreadCapPolicy(prefs, ThreadCapPolicyASOFGuardrail, "asof_cpu_guardrail")
	if settings := physicalSettings(prefs); len(settings) != 0 {
		t.Fatalf("expected no settings after no-cap preference, got %#v", settings)
	}
	if prefs.Execution.Threads.Mode != ThreadPreferenceNoCap {
		t.Fatalf("thread mode = %q, want no_cap", prefs.Execution.Threads.Mode)
	}
	if prefs.Execution.Threads.ReasonCode != "subquery_rate_over_aggregate_regresses" {
		t.Fatalf("reason = %q", prefs.Execution.Threads.ReasonCode)
	}
}

func TestUnknownThreadCapPolicyDoesNotEmitSetting(t *testing.T) {
	prefs := preferThreadCapPolicy(PhysicalPlanPreferences{}, ThreadCapPolicyDefault, "unknown")
	if settings := physicalSettings(prefs); len(settings) != 0 {
		t.Fatalf("expected no settings for default policy, got %#v", settings)
	}
}

func TestWithPhysicalSettingsCarriesResolvedThreadCap(t *testing.T) {
	prefs := preferThreadCapPolicy(PhysicalPlanPreferences{}, ThreadCapPolicyASOFGuardrail, "asof_cpu_guardrail")
	rendered := withPhysicalSettings(RenderedQuery{SQL: "SELECT 1", QuerySettings: map[string]any{"send_logs_level": "trace"}}, prefs)
	if got := rendered.QuerySettings["max_threads"]; got != 4 {
		t.Fatalf("max_threads setting = %#v, want 4", got)
	}
	if got := rendered.QuerySettings["send_logs_level"]; got != "trace" {
		t.Fatalf("existing setting = %#v, want trace", got)
	}
}

func TestFinalizeRenderedFragmentCarriesExtraSettings(t *testing.T) {
	rendered, err := finalizeRenderedFragment(renderedFragment{RawSQL: "SELECT 1", ExtraSettings: map[string]any{"max_threads": 4}})
	if err != nil {
		t.Fatalf("finalize rendered fragment: %v", err)
	}
	if got := rendered.QuerySettings["max_threads"]; got != 4 {
		t.Fatalf("max_threads setting = %#v, want 4", got)
	}
}
