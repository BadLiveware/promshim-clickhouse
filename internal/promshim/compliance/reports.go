package compliance

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type TesterReport struct {
	TotalResults int            `json:"totalResults"`
	Results      []TesterResult `json:"results"`
}

type TesterResult struct {
	TestCase          TesterCase `json:"testCase"`
	Diff              string     `json:"diff"`
	UnexpectedFailure string     `json:"unexpectedFailure"`
	UnexpectedSuccess bool       `json:"unexpectedSuccess"`
	Unsupported       bool       `json:"unsupported"`
}

type TesterCase struct {
	Query string `json:"query"`
}

type ExpectedFailures struct {
	Entries []ExpectedFailureEntry `json:"entries"`
}

type ExpectedFailureEntry struct {
	ID        string            `json:"id"`
	Query     string            `json:"query"`
	Modes     []string          `json:"modes"`
	MustMatch ExpectedMustMatch `json:"must_match"`
	Reason    string            `json:"reason"`
}

type ExpectedMustMatch struct {
	ErrContains      []string `json:"err_contains"`
	DiffContainsAll  []string `json:"diff_contains_all"`
	DiffContainsNone []string `json:"diff_contains_none"`
}

type ReconcileSummary struct {
	TotalFailures   int                   `json:"total_failures"`
	ExpectedCount   int                   `json:"expected_count"`
	UnexpectedCount int                   `json:"unexpected_count"`
	Expected        []ReconcileExpected   `json:"expected"`
	Unexpected      []ReconcileUnexpected `json:"unexpected"`
	AllowlistUnused []ReconcileUnused     `json:"allowlist_unused"`
}

type ReconcileExpected struct{ ID, Query, Reason string }
type ReconcileUnexpected struct {
	Query    string `json:"query"`
	HasDiff  bool   `json:"has_diff"`
	Err      string `json:"err"`
	DiffHead string `json:"diff_head"`
}
type ReconcileUnused struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

func LoadTesterReport(path string) (TesterReport, error) {
	var r TesterReport
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
func LoadExpectedFailures(path string) (ExpectedFailures, error) {
	var e ExpectedFailures
	b, err := os.ReadFile(path)
	if err != nil {
		return e, err
	}
	err = json.Unmarshal(b, &e)
	return e, err
}

func ReconcileExpectedFailures(report TesterReport, allow ExpectedFailures, mode string) ReconcileSummary {
	applicable := make([]ExpectedFailureEntry, 0, len(allow.Entries))
	for _, e := range allow.Entries {
		if entryApplies(e, mode) {
			applicable = append(applicable, e)
		}
	}
	failures := []TesterResult{}
	for _, r := range report.Results {
		if ResultFailed(r) {
			failures = append(failures, r)
		}
	}
	matchedIDs := map[string]bool{}
	summary := ReconcileSummary{TotalFailures: len(failures)}
	for _, f := range failures {
		var matched *ExpectedFailureEntry
		for i := range applicable {
			if expectedMatches(applicable[i], f) {
				matched = &applicable[i]
				break
			}
		}
		if matched == nil {
			summary.UnexpectedCount++
			summary.Unexpected = append(summary.Unexpected, ReconcileUnexpected{Query: f.TestCase.Query, HasDiff: f.Diff != "", Err: f.UnexpectedFailure, DiffHead: head(f.Diff, 240)})
		} else {
			summary.ExpectedCount++
			matchedIDs[matched.ID] = true
			summary.Expected = append(summary.Expected, ReconcileExpected{ID: matched.ID, Query: f.TestCase.Query, Reason: matched.Reason})
		}
	}
	for _, e := range applicable {
		if !matchedIDs[e.ID] {
			q := e.Query
			if q == "" {
				q = "(no query — matches by error)"
			}
			summary.AllowlistUnused = append(summary.AllowlistUnused, ReconcileUnused{ID: e.ID, Query: q})
		}
	}
	return summary
}

func ResultFailed(r TesterResult) bool {
	return r.Diff != "" || r.UnexpectedFailure != "" || r.UnexpectedSuccess || r.Unsupported
}
func entryApplies(e ExpectedFailureEntry, mode string) bool {
	if e.Modes == nil {
		return true
	}
	for _, m := range e.Modes {
		if m == mode {
			return true
		}
	}
	return false
}
func expectedMatches(e ExpectedFailureEntry, r TesterResult) bool {
	if e.Query != "" && r.TestCase.Query != e.Query {
		return false
	}
	for _, s := range e.MustMatch.ErrContains {
		if !strings.Contains(r.UnexpectedFailure, s) {
			return false
		}
	}
	for _, s := range e.MustMatch.DiffContainsAll {
		if !strings.Contains(r.Diff, s) {
			return false
		}
	}
	for _, s := range e.MustMatch.DiffContainsNone {
		if strings.Contains(r.Diff, s) {
			return false
		}
	}
	return true
}
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func ComplianceSummary(report TesterReport) map[string]int {
	out := map[string]int{"total": report.TotalResults}
	for _, r := range report.Results {
		switch {
		case !ResultFailed(r):
			out["passed"]++
		case r.Diff != "":
			out["diff_failure"]++
		case r.UnexpectedFailure != "":
			out["unexpected_failure"]++
		case r.UnexpectedSuccess:
			out["unexpected_success"]++
		case r.Unsupported:
			out["unsupported"]++
		}
	}
	return out
}

type FailureBucketSummary struct {
	Bucket  string   `json:"bucket"`
	Count   int      `json:"count"`
	Samples []string `json:"samples,omitempty"`
}

func ClassifyFailureBucket(r TesterResult) string {
	switch {
	case r.Unsupported:
		return "unsupported_501"
	case r.UnexpectedFailure != "":
		err := r.UnexpectedFailure
		switch {
		case strings.Contains(err, "not implemented"):
			return "unimplemented_function"
		case strings.Contains(err, "server error: 502"):
			return "server_error_502"
		case strings.Contains(err, "server error: 500"):
			return "server_error_500"
		case strings.HasPrefix(err, "Get \""):
			return "http_error_timeout"
		default:
			return "other_unexpected_failure"
		}
	case r.UnexpectedSuccess:
		return "unexpected_success"
	case r.Diff != "":
		q := r.TestCase.Query
		switch {
		case regexp.MustCompile(`^(topk|bottomk)\b`).MatchString(q):
			return "topk_bottomk_ordering"
		case strings.Contains(r.Diff, "NaN @["):
			return "staleness_nan_marker"
		case strings.Contains(r.Diff, "Inf") && strings.Contains(r.Diff, "NaN"):
			return "inf_nan_edge"
		case !regexp.MustCompile(`(^|\n)\+ `).MatchString(r.Diff):
			return "empty_on_test"
		case changedDiffLines(r.Diff) <= 2:
			return "small_value_divergence"
		default:
			return "other_diff"
		}
	default:
		return "passed"
	}
}

func FailureBuckets(report TesterReport) []FailureBucketSummary {
	counts := map[string]int{}
	samples := map[string][]string{}
	for _, r := range report.Results {
		b := ClassifyFailureBucket(r)
		counts[b]++
		if len(samples[b]) < 3 {
			samples[b] = append(samples[b], r.TestCase.Query)
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] == counts[keys[j]] {
			return keys[i] < keys[j]
		}
		return counts[keys[i]] > counts[keys[j]]
	})
	out := []FailureBucketSummary{}
	for _, k := range keys {
		out = append(out, FailureBucketSummary{Bucket: k, Count: counts[k], Samples: samples[k]})
	}
	return out
}
func changedDiffLines(diff string) int {
	n := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") {
			n++
		}
	}
	return n
}

type NativeGapSummary struct {
	Total                  int
	Passed                 int
	DiffFailure            int
	UnsupportedRoot        int
	UnexpectedFailureOther int
	UnsupportedShapes      []ShapeCount
}
type ShapeCount struct {
	Shape string
	Count int
}

func NativeGapReport(report TesterReport) NativeGapSummary {
	s := NativeGapSummary{Total: report.TotalResults}
	shapes := map[string]int{}
	for _, r := range report.Results {
		if r.Diff == "" && r.UnexpectedFailure == "" && !r.UnexpectedSuccess {
			s.Passed++
		}
		if r.Diff != "" {
			s.DiffFailure++
		}
		if strings.Contains(r.UnexpectedFailure, "requires a native_sql root plan") {
			s.UnsupportedRoot++
			shapes[NormalizeNativeGapShape(r.TestCase.Query)]++
		} else if r.UnexpectedFailure != "" {
			s.UnexpectedFailureOther++
		}
	}
	for shape, count := range shapes {
		s.UnsupportedShapes = append(s.UnsupportedShapes, ShapeCount{Shape: shape, Count: count})
	}
	sort.Slice(s.UnsupportedShapes, func(i, j int) bool {
		if s.UnsupportedShapes[i].Count == s.UnsupportedShapes[j].Count {
			return s.UnsupportedShapes[i].Shape < s.UnsupportedShapes[j].Shape
		}
		return s.UnsupportedShapes[i].Count > s.UnsupportedShapes[j].Count
	})
	return s
}

var nativeShapeNumberRe = regexp.MustCompile(`[0-9]+(\.[0-9]+)?`)
var nativeShapeMetricRe = regexp.MustCompile(`demo_[a-z_]+`)
var nativeShapeStringRe = regexp.MustCompile(`"[^"]*"`)
var nativeShapeMatchersRe = regexp.MustCompile(`\{[^}]*\}`)

func NormalizeNativeGapShape(query string) string {
	query = nativeShapeNumberRe.ReplaceAllString(query, "N")
	query = nativeShapeMetricRe.ReplaceAllString(query, "<metric>")
	query = nativeShapeStringRe.ReplaceAllString(query, "\"X\"")
	query = nativeShapeMatchersRe.ReplaceAllString(query, "")
	return query
}

func ReconcileIsClean(s ReconcileSummary) bool {
	return s.UnexpectedCount == 0 && len(s.AllowlistUnused) == 0
}
func FormatReconcileText(reportPath, allowPath, mode string, s ReconcileSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "report:    %s\nallowlist: %s\n", reportPath, allowPath)
	if mode != "" {
		fmt.Fprintf(&b, "mode:      %s\n", mode)
	}
	fmt.Fprintf(&b, "\nfailures: %d total / %d expected / %d unexpected\n", s.TotalFailures, s.ExpectedCount, s.UnexpectedCount)
	fmt.Fprintf(&b, "allowlist: %d applicable entr(y|ies) never matched\n\n", len(s.AllowlistUnused))
	if s.ExpectedCount > 0 {
		b.WriteString("== expected (matched allowlist) ==\n")
		grouped := map[string][]ReconcileExpected{}
		for _, e := range s.Expected {
			grouped[e.ID] = append(grouped[e.ID], e)
		}
		keys := make([]string, 0, len(grouped))
		for k := range grouped {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			g := grouped[k]
			fmt.Fprintf(&b, "[%s] %d match(es) — sample query: %s\n  reason: %s\n\n", k, len(g), g[0].Query, g[0].Reason)
		}
	}
	if s.UnexpectedCount > 0 {
		b.WriteString("== UNEXPECTED failures (potential regressions) ==\n")
		for _, u := range s.Unexpected {
			fmt.Fprintf(&b, "QUERY: %s", u.Query)
			if u.Err != "" {
				fmt.Fprintf(&b, "\n  ERR : %s", u.Err)
			}
			if u.HasDiff {
				fmt.Fprintf(&b, "\n  DIFF: %s...", u.DiffHead)
			}
			b.WriteString("\n\n")
		}
	}
	if len(s.AllowlistUnused) > 0 {
		b.WriteString("== UNMATCHED allowlist entries (stale allowlist -> regression of its own) ==\n")
		for _, u := range s.AllowlistUnused {
			fmt.Fprintf(&b, "[%s] %s\n", u.ID, u.Query)
		}
		b.WriteString("\n")
	}
	if ReconcileIsClean(s) {
		b.WriteString("RECONCILE: CLEAN\n")
	} else {
		b.WriteString("RECONCILE: REGRESSION\n")
	}
	return b.String()
}
