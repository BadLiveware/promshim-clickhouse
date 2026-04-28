package compliance

import "testing"

func TestReconcileExpectedFailuresMatchesAndFlagsUnused(t *testing.T) {
	report := TesterReport{Results: []TesterResult{{TestCase: TesterCase{Query: "topk without(instance) (2, demo_memory_usage_bytes)"}, Diff: "demo.promlabs.com:\n173015040 @["}}}
	allow := ExpectedFailures{Entries: []ExpectedFailureEntry{{ID: "topk-tie-break-ordering", Query: "topk without(instance) (2, demo_memory_usage_bytes)", MustMatch: ExpectedMustMatch{DiffContainsAll: []string{"demo.promlabs.com:", "173015040 @["}, DiffContainsNone: []string{"NaN @["}}, Reason: "tie"}}}
	summary := ReconcileExpectedFailures(report, allow, "prefer")
	if !ReconcileIsClean(summary) || summary.ExpectedCount != 1 || summary.UnexpectedCount != 0 || len(summary.AllowlistUnused) != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestReconcileExpectedFailuresFlagsUnexpectedAndUnused(t *testing.T) {
	report := TesterReport{Results: []TesterResult{{TestCase: TesterCase{Query: "rate(x[5m])"}, UnexpectedFailure: "server error: 500"}}}
	allow := ExpectedFailures{Entries: []ExpectedFailureEntry{{ID: "stale", Query: "topk(2, x)", Reason: "old"}}}
	summary := ReconcileExpectedFailures(report, allow, "prefer")
	if ReconcileIsClean(summary) || summary.UnexpectedCount != 1 || len(summary.AllowlistUnused) != 1 {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestFailureBuckets(t *testing.T) {
	cases := []struct {
		r    TesterResult
		want string
	}{
		{TesterResult{ToleranceApplied: &AppliedTolerance{ID: "small-drift"}}, "accepted_tolerance"},
		{TesterResult{Unsupported: true}, "unsupported_501"},
		{TesterResult{UnexpectedFailure: "not implemented: foo"}, "unimplemented_function"},
		{TesterResult{UnexpectedFailure: "server error: 502"}, "server_error_502"},
		{TesterResult{UnexpectedSuccess: true}, "unexpected_success"},
		{TesterResult{TestCase: TesterCase{Query: "topk(2, x)"}, Diff: "+ a\n- b"}, "topk_bottomk_ordering"},
		{TesterResult{Diff: "NaN @[123]"}, "staleness_nan_marker"},
		{TesterResult{Diff: "- only"}, "empty_on_test"},
		{TesterResult{Diff: "+ a\n- b"}, "small_value_divergence"},
	}
	for _, tc := range cases {
		if got := ClassifyFailureBucket(tc.r); got != tc.want {
			t.Fatalf("ClassifyFailureBucket(%#v) = %s, want %s", tc.r, got, tc.want)
		}
	}
}

func TestNativeGapReport(t *testing.T) {
	report := TesterReport{TotalResults: 8, Results: []TesterResult{
		{},
		{ToleranceApplied: &AppliedTolerance{ID: "small-drift"}},
		{ToleranceApplied: &AppliedTolerance{ID: "cleared-drift"}, Diff: "+ tolerated\n- tolerated"},
		{TestCase: TesterCase{Query: `sum(rate(demo_requests_total{job="api"}[5m]))`}, UnexpectedFailure: "requires a native_sql root plan"},
		{Diff: "+ a\n- b"},
		{UnexpectedFailure: "bad_data"},
		{UnexpectedSuccess: true},
		{Diff: "+ x\n- y", UnexpectedFailure: "server error: 500"},
	}}
	summary := NativeGapReport(report)
	if summary.Passed != 1 || summary.AcceptedTolerance != 2 || summary.UnsupportedRoot != 1 || summary.DiffFailure != 2 || summary.UnexpectedFailureOther != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(summary.UnsupportedShapes) != 1 || summary.UnsupportedShapes[0].Shape != `sum(rate(<metric>[Nm]))` {
		t.Fatalf("shapes = %#v", summary.UnsupportedShapes)
	}
}
