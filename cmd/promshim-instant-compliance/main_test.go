package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
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
