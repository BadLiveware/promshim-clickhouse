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
	h.Set("X-Promshim-Routing-Policy", "strict")
	h.Set("X-Promshim-Routing-Decision", "strict")
	h.Set("X-Promshim-Routing-Reason", "strict_policy")
	h.Set("X-Promshim-Strict-Strategy", "native_sql")
	h.Set("X-Promshim-Selected-Strategy", "native_sql")
	h.Set("X-Promshim-Cost-Family", "selector")
	got := parseHeaders(h)
	want := headerSample{strategy: "native_sql", fallbackReason: "", roundtrips: 7, millis: 42, routingPolicy: "strict", routingDecision: "strict", routingReason: "strict_policy", strictStrategy: "native_sql", selectedStrategy: "native_sql", costFamily: "selector"}
	if got != want {
		t.Fatalf("parseHeaders = %+v, want %+v", got, want)
	}
}

func TestParseHeadersMissingDefaults(t *testing.T) {
	got := parseHeaders(http.Header{})
	if got.strategy != "" || got.roundtrips != 0 || got.millis != 0 || got.routingPolicy != "" {
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

func TestRunBenchV2ModesAndLabels(t *testing.T) {
	promServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
	}))
	defer promServer.Close()

	shimServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := r.URL.Query().Get("native_lowering_mode")
		switch mode {
		case "prefer":
			w.Header().Set("X-Promshim-Strategy", "delegated_promql")
			w.Header().Set("X-Promshim-CH-Roundtrips", "1")
			w.Header().Set("X-Promshim-Routing-Policy", "strict")
			w.Header().Set("X-Promshim-Routing-Decision", "strict")
			w.Header().Set("X-Promshim-Routing-Reason", "strict_policy")
			w.Header().Set("X-Promshim-Strict-Strategy", "delegated_promql")
			w.Header().Set("X-Promshim-Selected-Strategy", "delegated_promql")
			w.Header().Set("X-Promshim-Cost-Family", "selector")
		case "force_supported":
			w.Header().Set("X-Promshim-Strategy", "native_sql")
			w.Header().Set("X-Promshim-CH-Roundtrips", "2")
		case "off":
			w.Header().Set("X-Promshim-Strategy", "local")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{}}})
	}))
	defer shimServer.Close()

	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	payload, _ := json.Marshal([]QuerySpec{{Name: "t", Endpoint: "query", Query: "up", TargetPromP50MS: &TargetPromP50MS{Min: 0.001, Max: 60000}}})
	if err := os.WriteFile(corpusPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	artifactDir := t.TempDir()
	report, err := RunBenchV2(BenchConfig{
		PromURL:       promServer.URL,
		ShimURL:       shimServer.URL,
		CorpusPath:    corpusPath,
		ArtifactDir:   artifactDir,
		ArtifactName:  "custom-v2.json",
		Repeats:       2,
		WarmupRepeats: 0,
		ShimModes:     []string{"prefer", "force_supported", "off"},
		RunLabels:     map[string]string{"transport": "native", "profile": "7d"},
		MemoryMode:    "summary",
	})
	if err != nil {
		t.Fatalf("RunBenchV2: %v", err)
	}
	if report.SchemaVersion != 2 {
		t.Fatalf("schemaVersion = %d, want 2", report.SchemaVersion)
	}
	if report.RunLabels["transport"] != "native" || report.RunLabels["profile"] != "7d" {
		t.Fatalf("runLabels = %+v", report.RunLabels)
	}
	if report.MemoryMode != "summary" {
		t.Fatalf("memoryMode = %q", report.MemoryMode)
	}
	row := report.Rows[0]
	if row.Prom == nil || row.Prom.P50MS == 0 {
		t.Fatalf("prom timing missing: %+v", row.Prom)
	}
	if row.PromBand != "in_band" {
		t.Fatalf("promBand = %q, want in_band", row.PromBand)
	}
	if row.TargetPromP50MS == nil || row.TargetPromP50MS.Min != 0.001 {
		t.Fatalf("targetPromP50Ms missing: %+v", row.TargetPromP50MS)
	}
	if got := row.Shim["prefer"].Strategy; got != "delegated_promql" {
		t.Fatalf("prefer strategy = %q", got)
	}
	if got := row.Shim["prefer"].RoutingPolicy; got != "strict" {
		t.Fatalf("prefer routing policy = %q", got)
	}
	if got := row.Shim["prefer"].CostFamily; got != "selector" {
		t.Fatalf("prefer cost family = %q", got)
	}
	if got := row.Shim["force_supported"].CHRoundtrips; got != 2 {
		t.Fatalf("force_supported roundtrips = %d", got)
	}
	if got := row.Shim["off"].Strategy; got != "local" {
		t.Fatalf("off strategy = %q", got)
	}
	if _, ok := row.Ratios["preferProm"]; !ok {
		t.Fatalf("missing prefer/prom ratio: %+v", row.Ratios)
	}
	if _, err := os.Stat(filepath.Join(artifactDir, "custom-v2.json")); err != nil {
		t.Fatalf("custom v2 artifact not written: %v", err)
	}
}

func TestBenchLogCommentIncludesQueryModeAndPolicy(t *testing.T) {
	comment := benchLogComment(QuerySpec{Name: "sum rate/by job", NativeLoweringMode: "force_supported", RoutingPolicy: "cost_shadow"})
	if comment != "promshim-bench query=sum_rate_by_job mode=force_supported policy=cost_shadow" {
		t.Fatalf("comment = %q", comment)
	}
	promComment := benchLogComment(QuerySpec{Name: "up"})
	if promComment != "promshim-bench query=up mode=prom" {
		t.Fatalf("prom comment = %q", promComment)
	}
}

func TestRunBenchV2CanSkipPrometheus(t *testing.T) {
	shimServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Promshim-Strategy", "native_sql")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}))
	defer shimServer.Close()

	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	payload, _ := json.Marshal([]QuerySpec{{Name: "t", Endpoint: "query", Query: "up"}})
	_ = os.WriteFile(corpusPath, payload, 0o644)

	report, err := RunBenchV2(BenchConfig{
		ShimURL:        shimServer.URL,
		CorpusPath:     corpusPath,
		Repeats:        1,
		WarmupRepeats:  0,
		ShimModes:      []string{"force_supported"},
		IncludeProm:    false,
		IncludePromSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows[0].Prom != nil {
		t.Fatalf("prom timing should be omitted: %+v", report.Rows[0].Prom)
	}
	if len(report.Rows[0].Ratios) != 0 {
		t.Fatalf("ratios should be omitted without prom timing: %+v", report.Rows[0].Ratios)
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

func TestRunBenchV2RoutingPolicyAxis(t *testing.T) {
	shimServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := r.URL.Query().Get("routing_policy")
		w.Header().Set("X-Promshim-Strategy", "native_sql")
		w.Header().Set("X-Promshim-Routing-Policy", policy)
		w.Header().Set("X-Promshim-Routing-Decision", "strict")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success"})
	}))
	defer shimServer.Close()

	corpusPath := filepath.Join(t.TempDir(), "corpus.json")
	payload, _ := json.Marshal([]QuerySpec{{Name: "t", Endpoint: "query", Query: "up"}})
	_ = os.WriteFile(corpusPath, payload, 0o644)

	report, err := RunBenchV2(BenchConfig{
		ShimURL:         shimServer.URL,
		CorpusPath:      corpusPath,
		Repeats:         1,
		WarmupRepeats:   0,
		ShimModes:       []string{"prefer"},
		RoutingPolicies: []string{"strict", "cost_shadow"},
		IncludeProm:     false,
		IncludePromSet:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	row := report.Rows[0]
	if row.Shim["prefer"].RoutingPolicy != "strict" {
		t.Fatalf("strict policy result missing: %+v", row.Shim)
	}
	if row.Shim["prefer@cost_shadow"].RoutingPolicy != "cost_shadow" {
		t.Fatalf("cost_shadow policy result missing: %+v", row.Shim)
	}
}
