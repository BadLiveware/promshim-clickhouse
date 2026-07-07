package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValuesClose(t *testing.T) {
	cases := []struct {
		name    string
		a, b    float64
		tolFrac float64
		tolAbs  float64
		want    bool
	}{
		{name: "identical", a: 1, b: 1, tolFrac: 1e-5, tolAbs: 1e-9, want: true},
		{name: "within-fraction", a: 0.163, b: 0.163 + 1e-7, tolFrac: 1e-5, tolAbs: 1e-9, want: true},
		{name: "the-reset-bug", a: 0.163, b: 0.1496, tolFrac: 1e-5, tolAbs: 1e-9, want: false},
		{name: "both-nan", a: math.NaN(), b: math.NaN(), tolFrac: 1e-5, tolAbs: 1e-9, want: true},
		{name: "one-nan", a: math.NaN(), b: 1, tolFrac: 1e-5, tolAbs: 1e-9, want: false},
		{name: "inf-equal", a: math.Inf(1), b: math.Inf(1), tolFrac: 1e-5, tolAbs: 1e-9, want: true},
		{name: "inf-vs-finite", a: math.Inf(1), b: 1e12, tolFrac: 1e-5, tolAbs: 1e-9, want: false},
		{name: "near-zero-abs", a: 0, b: 1e-10, tolFrac: 1e-5, tolAbs: 1e-9, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := valuesClose(tc.a, tc.b, tc.tolFrac, tc.tolAbs); got != tc.want {
				t.Fatalf("valuesClose(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func vectorEnvelope(samples []map[string]any) string {
	rows := make([]map[string]any, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, s)
	}
	payload := map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result":     rows,
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func vectorSample(value string, labels map[string]string) map[string]any {
	return map[string]any{
		"metric": labels,
		"value":  []any{1776807942.0, value},
	}
}

// TestCompareQueryDetectsResetDivergence is the regression test that would have
// caught the instant-rate counter-reset bug: reference returns the correct
// per-series rate, the shim returns the undercounted (reset-unaware) value, and
// the comparator must flag it as a diff.
func TestCompareQueryDetectsResetDivergence(t *testing.T) {
	ref := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorEnvelope([]map[string]any{
			vectorSample("0.163", map[string]string{"mode": "idle", "instance": "a"}),
		})))
	}))
	defer ref.Close()
	buggy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorEnvelope([]map[string]any{
			vectorSample("0.1496", map[string]string{"mode": "idle", "instance": "a"}),
		})))
	}))
	defer buggy.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := compareQuery(client, ref.URL, buggy.URL, `rate(demo_cpu_usage_seconds_total{mode="idle"}[1h])`, "2026-04-21T21:45:42Z", 1e-5, 1e-9)
	if result.Status != "diff" {
		t.Fatalf("expected diff for reset-unaware rate divergence, got %q (detail=%s)", result.Status, result.Detail)
	}
}

func TestCompareQueryPassesWhenMatching(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorEnvelope([]map[string]any{
			vectorSample("0.163", map[string]string{"mode": "idle"}),
			vectorSample("0.174", map[string]string{"mode": "user"}),
		})))
	})
	ref := httptest.NewServer(handler)
	defer ref.Close()
	test := httptest.NewServer(handler)
	defer test.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := compareQuery(client, ref.URL, test.URL, `rate(demo_cpu_usage_seconds_total[1h])`, "2026-04-21T21:45:42Z", 1e-5, 1e-9)
	if result.Status != "pass" {
		t.Fatalf("expected pass for identical results, got %q (detail=%s)", result.Status, result.Detail)
	}
}

func TestCompareQueryFlagsSeriesCountMismatch(t *testing.T) {
	ref := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorEnvelope([]map[string]any{
			vectorSample("1", map[string]string{"mode": "idle"}),
			vectorSample("2", map[string]string{"mode": "user"}),
		})))
	}))
	defer ref.Close()
	test := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(vectorEnvelope([]map[string]any{
			vectorSample("1", map[string]string{"mode": "idle"}),
		})))
	}))
	defer test.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	result := compareQuery(client, ref.URL, test.URL, `rate(x[1h])`, "2026-04-21T21:45:42Z", 1e-5, 1e-9)
	if result.Status != "diff" {
		t.Fatalf("expected diff for series count mismatch, got %q", result.Status)
	}
}

// TestEvaluateCorpusKnownDivergences exercises the known_divergences loop with a
// fake comparator: an entry that still diverges is reported but non-gating,
// while an entry that has started passing is counted STALE and gates. The note
// must be attached to the recorded result in both cases so the divergence stays
// visible in the report.
func TestEvaluateCorpusKnownDivergences(t *testing.T) {
	compare := func(query string) caseResult {
		switch query {
		case "gating_pass":
			return caseResult{Query: query, Status: "pass"}
		case "still_diverges":
			return caseResult{Query: query, Status: "diff", Detail: "value mismatch"}
		case "now_passing":
			return caseResult{Query: query, Status: "pass"}
		default:
			t.Fatalf("unexpected query %q", query)
			return caseResult{}
		}
	}
	corpus := queryCorpus{
		Queries: []string{"gating_pass"},
		KnownDivergences: []knownDivergence{
			{Query: "still_diverges", Note: "tracked issue #36"},
			{Query: "now_passing", Note: "tracked issue #99"},
		},
	}
	var rep report
	evaluateCorpus(&rep, corpus, compare)

	if rep.Total != 1 || rep.Passed != 1 || rep.Diffs != 0 || rep.Errors != 0 {
		t.Fatalf("gating tallies wrong: %+v", rep)
	}
	if len(rep.KnownResults) != 2 {
		t.Fatalf("expected 2 known results, got %d", len(rep.KnownResults))
	}
	if rep.KnownDiverging != 1 {
		t.Fatalf("expected 1 still-diverging known entry, got %d", rep.KnownDiverging)
	}
	if rep.KnownStale != 1 {
		t.Fatalf("expected 1 stale known entry, got %d", rep.KnownStale)
	}
	// The still-diverging entry carries its note plus the observed detail; the
	// now-passing (stale) entry carries just its note (compareQuery gives no
	// detail on pass).
	var sawDivergingNote, sawStaleNote bool
	for _, r := range rep.KnownResults {
		switch r.Query {
		case "still_diverges":
			if r.Status != "diff" || r.Detail != "tracked issue #36 | observed: value mismatch" {
				t.Fatalf("still_diverges detail wrong: %+v", r)
			}
			sawDivergingNote = true
		case "now_passing":
			if r.Status != "pass" || r.Detail != "tracked issue #99" {
				t.Fatalf("now_passing detail wrong: %+v", r)
			}
			sawStaleNote = true
		}
	}
	if !sawDivergingNote || !sawStaleNote {
		t.Fatalf("missing known-divergence results: diverging=%v stale=%v", sawDivergingNote, sawStaleNote)
	}
}

// TestGateFailed pins the gate exit-code wiring: any diff/error/stale trips the
// gate; a run with only still-diverging known entries (and clean gating
// results) does not.
func TestGateFailed(t *testing.T) {
	cases := []struct {
		name string
		rep  report
		want bool
	}{
		{name: "all-clean", rep: report{Passed: 3}, want: false},
		{name: "gating-diff", rep: report{Diffs: 1}, want: true},
		{name: "gating-error", rep: report{Errors: 1}, want: true},
		{name: "known-still-diverging-only", rep: report{KnownDiverging: 2}, want: false},
		{name: "known-stale", rep: report{KnownStale: 1}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gateFailed(tc.rep); got != tc.want {
				t.Fatalf("gateFailed(%+v) = %v, want %v", tc.rep, got, tc.want)
			}
		})
	}
}

func TestLoadCorpusEmptyQueriesErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yml")
	if err := os.WriteFile(path, []byte("queries: []\n"), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	if _, err := loadCorpus(path); err == nil {
		t.Fatal("expected error for corpus with no queries, got nil")
	}
	if _, err := loadCorpus(filepath.Join(dir, "does-not-exist.yml")); err == nil {
		t.Fatal("expected error for missing corpus file, got nil")
	}
}

func TestLoadCorpusUnmarshalsKnownDivergences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corpus.yml")
	body := "queries:\n" +
		"  - rate(foo[1h])\n" +
		"known_divergences:\n" +
		"  - query: rate(foo[1h] offset 5m)\n" +
		"    note: tracked offset bug\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	corpus, err := loadCorpus(path)
	if err != nil {
		t.Fatalf("loadCorpus: %v", err)
	}
	if len(corpus.Queries) != 1 || corpus.Queries[0] != "rate(foo[1h])" {
		t.Fatalf("queries not parsed: %+v", corpus.Queries)
	}
	if len(corpus.KnownDivergences) != 1 {
		t.Fatalf("expected 1 known divergence, got %d", len(corpus.KnownDivergences))
	}
	if corpus.KnownDivergences[0].Query != "rate(foo[1h] offset 5m)" || corpus.KnownDivergences[0].Note != "tracked offset bug" {
		t.Fatalf("known divergence not parsed: %+v", corpus.KnownDivergences[0])
	}
}
