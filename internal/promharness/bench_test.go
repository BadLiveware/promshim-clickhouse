package promharness

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPercentile(t *testing.T) {
	samples := []float64{5, 1, 4, 2, 3}
	if got := p50(samples); got != 3 {
		t.Fatalf("p50 = %v, want 3", got)
	}
	if got := p95(samples); got != 5 {
		t.Fatalf("p95 = %v, want 5", got)
	}
}

func TestPercentileEmpty(t *testing.T) {
	if got := p50(nil); got != 0 {
		t.Fatalf("p50(nil) = %v, want 0", got)
	}
}

func TestPercentileTenSamples(t *testing.T) {
	samples := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := p50(samples); got != 5 {
		t.Fatalf("p50 = %v, want 5", got)
	}
	if got := p95(samples); got != 10 {
		t.Fatalf("p95 = %v, want 10", got)
	}
}

func TestParseHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Promshim-Strategy", "native_sql")
	h.Set("X-Promshim-Fallback-Reason", "")
	h.Set("X-Promshim-CH-Roundtrips", "7")
	h.Set("X-Promshim-CH-Millis", "42")
	got := parseHeaders(h)
	want := headerSample{strategy: "native_sql", fallbackReason: "", roundtrips: 7, millis: 42}
	if got != want {
		t.Fatalf("parseHeaders = %+v, want %+v", got, want)
	}
}

func TestParseHeadersMissingDefaults(t *testing.T) {
	got := parseHeaders(http.Header{})
	if got.strategy != "" || got.roundtrips != 0 || got.millis != 0 {
		t.Fatalf("expected zero-value on empty headers, got %+v", got)
	}
}

func TestParseHeadersBadIntsIgnored(t *testing.T) {
	h := http.Header{}
	h.Set("X-Promshim-CH-Roundtrips", "not-a-number")
	got := parseHeaders(h)
	if got.roundtrips != 0 {
		t.Fatalf("bad CH-Roundtrips must fall back to 0, got %d", got.roundtrips)
	}
}

func TestDetectStrategyFlap(t *testing.T) {
	steady := []headerSample{{strategy: "native_sql"}, {strategy: "native_sql"}, {strategy: "native_sql"}}
	if detectStrategyFlap(steady) {
		t.Fatal("steady-state must not flap")
	}
	flapping := []headerSample{{strategy: "native_sql"}, {strategy: "local"}, {strategy: "native_sql"}}
	if !detectStrategyFlap(flapping) {
		t.Fatal("mixed strategies must flap")
	}
	// Single sample can't flap.
	if detectStrategyFlap([]headerSample{{strategy: "native_sql"}}) {
		t.Fatal("single sample must not flap")
	}
}

func TestRunBenchEndToEnd(t *testing.T) {
	// Fake Prom: no shim headers, fixed latency.
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
	}))
	defer promServer.Close()

	// Fake shim: emit headers whose values depend on the requested native_lowering_mode
	// so we verify the bench picks them up per mode.
	shimServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("native_lowering_mode")
		switch mode {
		case "force_supported":
			w.Header().Set("X-Promshim-Strategy", "native_sql")
			w.Header().Set("X-Promshim-CH-Roundtrips", "1")
			w.Header().Set("X-Promshim-CH-Millis", "5")
		case "off":
			w.Header().Set("X-Promshim-Strategy", "local")
			w.Header().Set("X-Promshim-CH-Roundtrips", "4")
			w.Header().Set("X-Promshim-CH-Millis", "18")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
	}))
	defer shimServer.Close()

	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	corpus := []QuerySpec{
		{Name: "t", Endpoint: "query", Query: "up", TimeOffsetSeconds: 0},
	}
	payload, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corpusPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	artifactDir := t.TempDir()
	cfg := BenchConfig{
		PromURL:       promServer.URL,
		ShimURL:       shimServer.URL,
		CorpusPath:    corpusPath,
		ArtifactDir:   artifactDir,
		Manifest:      Manifest{BaseUnixSeconds: 0},
		Repeats:       3,
		WarmupRepeats: 0,
	}
	report, err := RunBench(cfg)
	if err != nil {
		t.Fatalf("RunBench: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(report.Rows))
	}
	row := report.Rows[0]
	if row.Strategy != "native_sql" {
		t.Fatalf("row.Strategy = %q, want native_sql", row.Strategy)
	}
	if row.CHRoundtrips != 1 {
		t.Fatalf("row.CHRoundtrips = %d, want 1", row.CHRoundtrips)
	}
	if row.StrategyFlap {
		t.Fatal("deterministic shim must not flap")
	}
	if row.FallbackP50MS == 0 {
		t.Fatalf("fallback p50 not measured: %+v", row)
	}
	if report.Summary.StrategyHistogram["native_sql"] != 1 {
		t.Fatalf("histogram = %+v, want native_sql:1", report.Summary.StrategyHistogram)
	}

	// Verify report was serialized.
	if _, err := os.Stat(filepath.Join(artifactDir, "bench-report.json")); err != nil {
		t.Fatalf("bench-report.json not written: %v", err)
	}
}

func TestRunBenchStrategyFlapDetected(t *testing.T) {
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
	}))
	defer promServer.Close()
	counter := 0
	shimServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("native_lowering_mode")
		if mode == "force_supported" {
			counter++
			if counter%2 == 0 {
				w.Header().Set("X-Promshim-Strategy", "native_sql")
			} else {
				w.Header().Set("X-Promshim-Strategy", "delegated_promql")
			}
			w.Header().Set("X-Promshim-CH-Roundtrips", strconv.Itoa(counter))
		} else {
			w.Header().Set("X-Promshim-Strategy", "local")
			w.Header().Set("X-Promshim-CH-Roundtrips", "1")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
	}))
	defer shimServer.Close()

	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	corpus := []QuerySpec{{Name: "t", Endpoint: "query", Query: "up"}}
	payload, _ := json.Marshal(corpus)
	_ = os.WriteFile(corpusPath, payload, 0o644)

	report, err := RunBench(BenchConfig{
		PromURL: promServer.URL, ShimURL: shimServer.URL, CorpusPath: corpusPath,
		Repeats: 4, WarmupRepeats: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Rows[0].StrategyFlap {
		t.Fatal("expected strategy flap, got steady")
	}
}

func TestSafeRatio(t *testing.T) {
	if got := safeRatio(10, 2); got != 5 {
		t.Fatalf("safeRatio(10, 2) = %v, want 5", got)
	}
	if got := safeRatio(1, 0); got != 0 {
		t.Fatalf("safeRatio(_, 0) must be 0, got %v", got)
	}
	if math.IsNaN(safeRatio(1, 1)) {
		t.Fatal("safeRatio produced NaN")
	}
}
