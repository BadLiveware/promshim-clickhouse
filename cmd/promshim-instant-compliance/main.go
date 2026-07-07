// Command promshim-instant-compliance runs a curated set of instant PromQL
// queries against reference Prometheus and promshim at a fixed evaluation
// timestamp and reports value divergences within Prometheus's default float
// tolerance.
//
// The upstream prometheus/compliance tester only issues query_range requests,
// so promshim's instant fast paths went differentially unvalidated. This driver
// closes that gap: it is invoked by harness/compliance/scripts/run-compliance.sh
// for both the prefer pass (gated: --gate exits non-zero on divergence) and the
// native pass (informational: no --gate).
//
// Gaps stay visible. A divergence here is a real shim bug or a genuine
// reference-side difference — it is NOT allowlisted in expected-failures.json.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v2"
)

type queryCorpus struct {
	Queries []string `yaml:"queries"`
	// KnownDivergences are queries with a pre-existing, separately-tracked
	// reference-vs-shim divergence that is OUT OF SCOPE for the current change.
	// They are still evaluated and reported every run (so the divergence stays
	// visible and any *change* in it surfaces), but they do not gate. This is
	// NOT expected-failures.json and must never be used to hide an in-scope shim
	// bug — every entry requires a `note` pointing at the tracked issue.
	KnownDivergences []knownDivergence `yaml:"known_divergences"`
}

type knownDivergence struct {
	Query string `yaml:"query"`
	Note  string `yaml:"note"`
}

type sample struct {
	Metric map[string]string
	Value  float64
}

type caseResult struct {
	Query      string `json:"query"`
	Status     string `json:"status"` // pass | diff | error
	Detail     string `json:"detail,omitempty"`
	Reference  string `json:"reference,omitempty"`
	Test       string `json:"test,omitempty"`
	MaxAbsDiff string `json:"maxAbsDiff,omitempty"`
}

type report struct {
	Mode           string       `json:"mode"`
	EvalTime       string       `json:"evalTime"`
	ReferenceURL   string       `json:"referenceUrl"`
	TestURL        string       `json:"testUrl"`
	ToleranceFrac  float64      `json:"toleranceFraction"`
	ToleranceAbs   float64      `json:"toleranceAbsolute"`
	Total          int          `json:"total"`
	Passed         int          `json:"passed"`
	Diffs          int          `json:"diffs"`
	Errors         int          `json:"errors"`
	Results        []caseResult `json:"results"`
	KnownResults   []caseResult `json:"knownDivergenceResults,omitempty"`
	KnownDiverging int          `json:"knownDivergingObserved"`
	// KnownStale counts known_divergences entries that no longer diverge (now
	// pass). A stale entry is a lie: it claims a tracked divergence that is
	// gone, so it silently suppresses nothing while pretending to. In gate mode
	// this fails the gate to force the operator to delete the entry (or promote
	// it to a gating `queries:` entry now that it passes).
	KnownStale int `json:"knownDivergenceStale"`
}

func main() {
	corpusPath := flag.String("corpus", "instant-queries.yml", "instant query corpus YAML")
	referenceURL := flag.String("reference-url", "http://localhost:29090", "reference Prometheus base URL")
	testURL := flag.String("test-url", "http://localhost:29091", "promshim base URL")
	evalTime := flag.String("eval-time", "", "RFC3339 evaluation timestamp (pinned fixture end_time)")
	mode := flag.String("mode", "", "mode label recorded on the report (prefer|native)")
	jsonOut := flag.String("json-out", "", "optional path to write the JSON report")
	tolFrac := flag.Float64("tolerance-fraction", 1e-5, "fractional value tolerance (Prometheus default)")
	tolAbs := flag.Float64("tolerance-absolute", 1e-9, "absolute value tolerance floor")
	gate := flag.Bool("gate", false, "exit non-zero when any query diverges or errors")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request HTTP timeout")
	flag.Parse()

	if strings.TrimSpace(*evalTime) == "" {
		fmt.Fprintln(os.Stderr, "error: --eval-time is required")
		os.Exit(2)
	}

	corpus, err := loadCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load corpus: %v\n", err)
		os.Exit(2)
	}

	client := &http.Client{Timeout: *timeout}
	rep := report{
		Mode:          *mode,
		EvalTime:      *evalTime,
		ReferenceURL:  *referenceURL,
		TestURL:       *testURL,
		ToleranceFrac: *tolFrac,
		ToleranceAbs:  *tolAbs,
	}
	compare := func(query string) caseResult {
		return compareQuery(client, *referenceURL, *testURL, query, *evalTime, *tolFrac, *tolAbs)
	}
	evaluateCorpus(&rep, corpus, compare)

	label := ""
	if rep.Mode != "" {
		label = " [mode=" + rep.Mode + "]"
	}
	fmt.Printf(">> Instant differential coverage%s: %d queries, %d passed, %d diff, %d error\n",
		label, rep.Total, rep.Passed, rep.Diffs, rep.Errors)
	for _, r := range rep.Results {
		if r.Status == "pass" {
			continue
		}
		fmt.Printf("   [%s] %s\n        %s\n", strings.ToUpper(r.Status), r.Query, r.Detail)
	}
	if len(rep.KnownResults) > 0 {
		fmt.Printf(">> Known (tracked, non-gating) divergences: %d queries, %d still diverging, %d STALE (now passing)\n", len(rep.KnownResults), rep.KnownDiverging, rep.KnownStale)
		for _, r := range rep.KnownResults {
			if r.Status == "pass" {
				fmt.Printf("   [KNOWN:STALE] %s\n        no longer diverges — delete this known_divergences entry or promote it to queries: (%s)\n", r.Query, r.Detail)
				continue
			}
			fmt.Printf("   [KNOWN:%s] %s\n        %s\n", strings.ToUpper(r.Status), r.Query, r.Detail)
		}
	}

	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, rep); err != nil {
			fmt.Fprintf(os.Stderr, "error: write json report: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf(">> Instant report written to: %s\n", *jsonOut)
	}

	if *gate && gateFailed(rep) {
		fmt.Fprintf(os.Stderr, ">> Instant differential coverage FAILED%s: %d diff, %d error, %d stale known_divergences\n", label, rep.Diffs, rep.Errors, rep.KnownStale)
		os.Exit(1)
	}
}

// evaluateCorpus runs the gating queries and the non-gating known_divergences
// through compare and tallies the report. compare is injected so the loop is
// testable without a live HTTP stack.
func evaluateCorpus(rep *report, corpus queryCorpus, compare func(query string) caseResult) {
	for _, query := range corpus.Queries {
		result := compare(query)
		rep.Results = append(rep.Results, result)
		rep.Total++
		switch result.Status {
		case "pass":
			rep.Passed++
		case "diff":
			rep.Diffs++
		default:
			rep.Errors++
		}
	}

	// Known, separately-tracked divergences are evaluated for visibility but
	// never gate on the divergence itself. They are reported every run so a
	// change is noticed; an entry that has started passing is STALE and gates
	// (see gateFailed) so the allowlist cannot silently rot.
	for _, kd := range corpus.KnownDivergences {
		result := compare(kd.Query)
		if result.Detail == "" {
			result.Detail = kd.Note
		} else {
			result.Detail = kd.Note + " | observed: " + result.Detail
		}
		rep.KnownResults = append(rep.KnownResults, result)
		if result.Status == "pass" {
			rep.KnownStale++
		} else {
			rep.KnownDiverging++
		}
	}
}

// gateFailed reports whether a gated run should exit non-zero: any real
// divergence or error in the gating corpus, or any stale known_divergences
// entry (one that no longer diverges and must be removed or promoted).
func gateFailed(rep report) bool {
	return rep.Diffs > 0 || rep.Errors > 0 || rep.KnownStale > 0
}

func loadCorpus(path string) (queryCorpus, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return queryCorpus{}, err
	}
	var corpus queryCorpus
	if err := yaml.Unmarshal(payload, &corpus); err != nil {
		return queryCorpus{}, err
	}
	if len(corpus.Queries) == 0 {
		return queryCorpus{}, fmt.Errorf("corpus %q has no queries", path)
	}
	return corpus, nil
}

func compareQuery(client *http.Client, referenceURL, testURL, query, evalTime string, tolFrac, tolAbs float64) caseResult {
	ref, refErr := instantQuery(client, referenceURL, query, evalTime)
	if refErr != nil {
		return caseResult{Query: query, Status: "error", Detail: "reference: " + refErr.Error()}
	}
	test, testErr := instantQuery(client, testURL, query, evalTime)
	if testErr != nil {
		return caseResult{Query: query, Status: "error", Detail: "test: " + testErr.Error()}
	}

	if len(ref) != len(test) {
		return caseResult{
			Query:     query,
			Status:    "diff",
			Detail:    fmt.Sprintf("series count mismatch: reference=%d test=%d", len(ref), len(test)),
			Reference: summarize(ref),
			Test:      summarize(test),
		}
	}

	refByKey := indexByLabels(ref)
	testByKey := indexByLabels(test)
	maxAbs := 0.0
	for key, refValue := range refByKey {
		testValue, ok := testByKey[key]
		if !ok {
			return caseResult{
				Query:     query,
				Status:    "diff",
				Detail:    "series present in reference but missing in test: " + key,
				Reference: summarize(ref),
				Test:      summarize(test),
			}
		}
		if !valuesClose(refValue, testValue, tolFrac, tolAbs) {
			return caseResult{
				Query:      query,
				Status:     "diff",
				Detail:     fmt.Sprintf("value mismatch for %s: reference=%v test=%v", key, refValue, testValue),
				Reference:  summarize(ref),
				Test:       summarize(test),
				MaxAbsDiff: strconv.FormatFloat(math.Abs(refValue-testValue), 'g', -1, 64),
			}
		}
		if d := math.Abs(refValue - testValue); d > maxAbs {
			maxAbs = d
		}
	}
	return caseResult{Query: query, Status: "pass", MaxAbsDiff: strconv.FormatFloat(maxAbs, 'g', -1, 64)}
}

func instantQuery(client *http.Client, baseURL, query, evalTime string) ([]sample, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v1/query")
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("query", query)
	values.Set("time", evalTime)
	parsed.RawQuery = values.Encode()

	response, err := client.Get(parsed.String())
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
		Data      struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode response (%d): %v", response.StatusCode, err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("%s: %s", envelope.ErrorType, envelope.Error)
	}
	return normalize(envelope.Data.ResultType, envelope.Data.Result)
}

func normalize(resultType string, raw json.RawMessage) ([]sample, error) {
	switch resultType {
	case "scalar":
		var payload []any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, err
		}
		if len(payload) != 2 {
			return nil, fmt.Errorf("unexpected scalar payload shape: %#v", payload)
		}
		value, err := toFloat(payload[1])
		if err != nil {
			return nil, err
		}
		return []sample{{Metric: map[string]string{}, Value: value}}, nil
	case "vector":
		var rows []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		out := make([]sample, 0, len(rows))
		for _, row := range rows {
			if len(row.Value) != 2 {
				return nil, fmt.Errorf("unexpected vector value shape: %#v", row.Value)
			}
			value, err := toFloat(row.Value[1])
			if err != nil {
				return nil, err
			}
			out = append(out, sample{Metric: row.Metric, Value: value})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported instant resultType %q", resultType)
	}
}

func toFloat(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case string:
		if strings.EqualFold(typed, "nan") {
			return math.NaN(), nil
		}
		if strings.EqualFold(typed, "+inf") || strings.EqualFold(typed, "inf") {
			return math.Inf(1), nil
		}
		if strings.EqualFold(typed, "-inf") {
			return math.Inf(-1), nil
		}
		return strconv.ParseFloat(typed, 64)
	default:
		return 0, fmt.Errorf("cannot parse float from %T (%v)", value, value)
	}
}

// valuesClose mirrors the upstream compliance tester's comparison: NaNs are
// considered equal, and finite values must agree within either the fractional
// or absolute tolerance.
func valuesClose(a, b, tolFrac, tolAbs float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return a == b
	}
	diff := math.Abs(a - b)
	if diff <= tolAbs {
		return true
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	return diff <= tolFrac*scale
}

func indexByLabels(samples []sample) map[string]float64 {
	out := make(map[string]float64, len(samples))
	for _, s := range samples {
		out[labelKey(s.Metric)] = s.Value
	}
	return out
}

func labelKey(metric map[string]string) string {
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	// Quote both name and value so label values containing ',' or '='
	// cannot produce a key that collides with a different label set.
	for _, key := range keys {
		builder.WriteString(strconv.Quote(key))
		builder.WriteByte('=')
		builder.WriteString(strconv.Quote(metric[key]))
		builder.WriteByte(',')
	}
	return builder.String()
}

func summarize(samples []sample) string {
	if len(samples) == 0 {
		return "(empty)"
	}
	parts := make([]string, 0, len(samples))
	for _, s := range samples {
		parts = append(parts, fmt.Sprintf("%s=%v", labelKey(s.Metric), s.Value))
	}
	sort.Strings(parts)
	if len(parts) > 8 {
		parts = append(parts[:8], fmt.Sprintf("(+%d more)", len(samples)-8))
	}
	return strings.Join(parts, " ")
}

func writeJSON(path string, rep report) error {
	payload, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}
