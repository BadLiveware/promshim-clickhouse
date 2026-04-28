package compliance

import (
	"encoding/xml"
	"fmt"
	"strings"
)

type JUnitPolicy struct {
	Mode           string
	Allowlist      ExpectedFailures
	NativeIsInform bool
}

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Suites   []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Skipped   int             `xml:"skipped,attr"`
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

func JUnitXML(report TesterReport, policy JUnitPolicy) ([]byte, error) {
	mode := policy.Mode
	if mode == "" {
		mode = "default"
	}
	suite := junitTestSuite{Name: "promql-compliance-" + mode}
	for _, result := range report.Results {
		caseResult := junitTestCase{
			ClassName: "promshim.compliance." + mode,
			Name:      result.TestCase.Query,
		}
		if !ResultFailed(result) {
			suite.TestCases = append(suite.TestCases, caseResult)
			continue
		}

		kind, detail := resultFailureDetail(result)
		if policy.NativeIsInform || policy.Mode == "native" {
			caseResult.Skipped = &junitSkipped{Message: "native informational gap: " + kind, Text: detail}
			caseResult.SystemOut = "Native-mode compliance is informational; gaps stay visible but do not gate CI."
			suite.Skipped++
		} else if expected := matchingExpectedFailure(policy.Allowlist, policy.Mode, result); expected != nil {
			caseResult.Skipped = &junitSkipped{Message: "expected compliance failure: " + expected.ID, Text: detail}
			caseResult.SystemOut = expected.Reason
			suite.Skipped++
		} else {
			caseResult.Failure = &junitFailure{Message: kind, Type: kind, Text: detail}
			suite.Failures++
		}
		suite.TestCases = append(suite.TestCases, caseResult)
	}
	suite.Tests = len(suite.TestCases)
	tests := junitTestSuites{
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Skipped:  suite.Skipped,
		Suites:   []junitTestSuite{suite},
	}
	out, err := xml.MarshalIndent(tests, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

func ComplianceMarkdown(report TesterReport, policy JUnitPolicy, reportPath string) string {
	mode := policy.Mode
	if mode == "" {
		mode = "default"
	}
	summary := ComplianceSummary(report)
	passed := summary["passed"]
	failed := report.TotalResults - passed
	var b strings.Builder
	fmt.Fprintf(&b, "## PromQL compliance (%s)\n\n", mode)
	if reportPath != "" {
		fmt.Fprintf(&b, "Report: `%s`\n\n", reportPath)
	}
	fmt.Fprintf(&b, "| Total | Passed | Failed or visible gaps | Diff failures | Unexpected failures | Unexpected successes | Unsupported |\n")
	fmt.Fprintf(&b, "|---:|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&b, "| %d | %d | %d | %d | %d | %d | %d |\n\n",
		report.TotalResults,
		passed,
		failed,
		summary["diff_failure"],
		summary["unexpected_failure"],
		summary["unexpected_success"],
		summary["unsupported"],
	)
	if mode == "native" || policy.NativeIsInform {
		gap := NativeGapReport(report)
		fmt.Fprintf(&b, "Native mode is informational: gaps are reported but do not fail CI.\n\n")
		fmt.Fprintf(&b, "| Passing on native | Diff failures | Unsupported root | Other errors |\n")
		fmt.Fprintf(&b, "|---:|---:|---:|---:|\n")
		fmt.Fprintf(&b, "| %d | %d | %d | %d |\n\n", gap.Passed, gap.DiffFailure, gap.UnsupportedRoot, gap.UnexpectedFailureOther)
	} else {
		reconcile := ReconcileExpectedFailures(report, policy.Allowlist, policy.Mode)
		status := "REGRESSION"
		if ReconcileIsClean(reconcile) {
			status = "CLEAN"
		}
		fmt.Fprintf(&b, "Allowlist reconcile: **%s** (%d expected, %d unexpected, %d unused allowlist entries).\n\n",
			status, reconcile.ExpectedCount, reconcile.UnexpectedCount, len(reconcile.AllowlistUnused))
	}
	fmt.Fprintf(&b, "### Buckets\n\n")
	fmt.Fprintf(&b, "| Count | Bucket | Samples |\n")
	fmt.Fprintf(&b, "|---:|---|---|\n")
	for _, bucket := range FailureBuckets(report) {
		fmt.Fprintf(&b, "| %d | `%s` | %s |\n", bucket.Count, bucket.Bucket, markdownSamples(bucket.Samples))
	}
	b.WriteString("\n")
	return b.String()
}

func matchingExpectedFailure(allow ExpectedFailures, mode string, result TesterResult) *ExpectedFailureEntry {
	for i := range allow.Entries {
		entry := &allow.Entries[i]
		if entryApplies(*entry, mode) && expectedMatches(*entry, result) {
			return entry
		}
	}
	return nil
}

func resultFailureDetail(result TesterResult) (string, string) {
	switch {
	case result.Diff != "":
		return "diff_failure", result.Diff
	case result.UnexpectedFailure != "":
		return "unexpected_failure", result.UnexpectedFailure
	case result.UnexpectedSuccess:
		return "unexpected_success", "query unexpectedly succeeded"
	case result.Unsupported:
		return "unsupported", "query is unsupported"
	default:
		return "passed", ""
	}
}

func markdownSamples(samples []string) string {
	if len(samples) == 0 {
		return ""
	}
	escaped := make([]string, 0, len(samples))
	for _, sample := range samples {
		escaped = append(escaped, "`"+strings.ReplaceAll(sample, "`", "\\`")+"`")
	}
	return strings.Join(escaped, "<br>")
}
