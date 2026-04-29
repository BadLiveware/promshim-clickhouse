package physical

import "testing"

func TestThreadCapPolicySettings(t *testing.T) {
	prefs := PreferThreadCapPolicy(PlanPreferences{}, ThreadCapPolicyASOFGuardrail, "asof_cpu_guardrail")
	settings := Settings(prefs)
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
	prefs := PreferNoThreadCap(PlanPreferences{}, "subquery_rate_over_aggregate_regresses")
	prefs = PreferThreadCapPolicy(prefs, ThreadCapPolicyASOFGuardrail, "asof_cpu_guardrail")
	if settings := Settings(prefs); len(settings) != 0 {
		t.Fatalf("expected no settings after no-cap preference, got %#v", settings)
	}
	if prefs.Execution.Threads.Mode != ThreadPreferenceNoCap {
		t.Fatalf("thread mode = %q, want no_cap", prefs.Execution.Threads.Mode)
	}
	if prefs.Execution.Threads.ReasonCode != "subquery_rate_over_aggregate_regresses" {
		t.Fatalf("reason = %q", prefs.Execution.Threads.ReasonCode)
	}
}

func TestThreadPreferenceDecisionNoCapIncludesRejectedAlternative(t *testing.T) {
	decision, ok := ThreadPreferenceDecision(ThreadPreference{Mode: ThreadPreferenceNoCap, ReasonCode: ThreadPreferenceReasonSubqueryRateRows})
	if !ok {
		t.Fatal("expected no-cap decision")
	}
	if decision.Kind != "query_settings" || decision.Strategy != "no_thread_cap" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if len(decision.Rejected) != 1 || decision.Rejected[0].Strategy != "set_max_threads" {
		t.Fatalf("expected rejected set_max_threads alternative, got %#v", decision.Rejected)
	}
}
