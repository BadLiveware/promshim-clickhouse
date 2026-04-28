package compliance

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestJUnitXMLMarksAllowlistedPreferFailureSkipped(t *testing.T) {
	report := TesterReport{TotalResults: 2, Results: []TesterResult{
		{TestCase: TesterCase{Query: "up"}},
		{TestCase: TesterCase{Query: "topk(1, up)"}, Diff: "- old\n+ new"},
	}}
	allow := ExpectedFailures{Entries: []ExpectedFailureEntry{{
		ID:    "known-topk-ordering",
		Query: "topk(1, up)",
		MustMatch: ExpectedMustMatch{DiffContainsAll: []string{
			"old",
			"new",
		}},
		Reason: "known ordering difference",
	}}}

	out, err := JUnitXML(report, JUnitPolicy{Mode: "prefer", Allowlist: allow})
	if err != nil {
		t.Fatalf("JUnitXML returned error: %v", err)
	}
	var parsed junitTestSuites
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal junit xml: %v\n%s", err, out)
	}
	if parsed.Tests != 2 || parsed.Failures != 0 || parsed.Skipped != 1 {
		t.Fatalf("unexpected suite counts: tests=%d failures=%d skipped=%d", parsed.Tests, parsed.Failures, parsed.Skipped)
	}
	if got := string(out); !strings.Contains(got, "expected compliance failure: known-topk-ordering") {
		t.Fatalf("expected allowlist skip reason in junit XML, got:\n%s", got)
	}
}

func TestJUnitXMLMarksAcceptedToleranceSkipped(t *testing.T) {
	report := TesterReport{TotalResults: 1, Results: []TesterResult{
		{TestCase: TesterCase{Query: "demo_memory_usage_bytes % 1.2345"}, ToleranceApplied: &AppliedTolerance{ID: "native-modulo-small-float-drift", Query: "demo_memory_usage_bytes % 1.2345", Margin: 0.000001, Reason: "small drift"}},
	}}
	out, err := JUnitXML(report, JUnitPolicy{Mode: "prefer"})
	if err != nil {
		t.Fatalf("JUnitXML returned error: %v", err)
	}
	var parsed junitTestSuites
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal junit xml: %v\n%s", err, out)
	}
	if parsed.Tests != 1 || parsed.Failures != 0 || parsed.Skipped != 1 {
		t.Fatalf("unexpected suite counts: tests=%d failures=%d skipped=%d", parsed.Tests, parsed.Failures, parsed.Skipped)
	}
	if got := string(out); !strings.Contains(got, "accepted tolerance: native-modulo-small-float-drift") {
		t.Fatalf("expected accepted tolerance in junit XML, got:\n%s", got)
	}
}

func TestJUnitXMLMarksUnexpectedPreferFailureFailed(t *testing.T) {
	report := TesterReport{TotalResults: 1, Results: []TesterResult{
		{TestCase: TesterCase{Query: "rate(up[5m])"}, UnexpectedFailure: "server error: 500"},
	}}
	out, err := JUnitXML(report, JUnitPolicy{Mode: "prefer"})
	if err != nil {
		t.Fatalf("JUnitXML returned error: %v", err)
	}
	var parsed junitTestSuites
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal junit xml: %v\n%s", err, out)
	}
	if parsed.Tests != 1 || parsed.Failures != 1 || parsed.Skipped != 0 {
		t.Fatalf("unexpected suite counts: tests=%d failures=%d skipped=%d", parsed.Tests, parsed.Failures, parsed.Skipped)
	}
	if got := string(out); !strings.Contains(got, "server error: 500") {
		t.Fatalf("expected failure detail in junit XML, got:\n%s", got)
	}
}

func TestJUnitXMLMarksNativeFailuresSkipped(t *testing.T) {
	report := TesterReport{TotalResults: 1, Results: []TesterResult{
		{TestCase: TesterCase{Query: "histogram_quantile(0.9, up)"}, UnexpectedFailure: "requires a native_sql root plan"},
	}}
	out, err := JUnitXML(report, JUnitPolicy{Mode: "native"})
	if err != nil {
		t.Fatalf("JUnitXML returned error: %v", err)
	}
	var parsed junitTestSuites
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal junit xml: %v\n%s", err, out)
	}
	if parsed.Tests != 1 || parsed.Failures != 0 || parsed.Skipped != 1 {
		t.Fatalf("unexpected suite counts: tests=%d failures=%d skipped=%d", parsed.Tests, parsed.Failures, parsed.Skipped)
	}
	if got := string(out); !strings.Contains(got, "native informational gap") {
		t.Fatalf("expected native informational skip in junit XML, got:\n%s", got)
	}
}
