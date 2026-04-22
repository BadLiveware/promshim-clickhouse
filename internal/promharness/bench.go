package promharness

// Benchmark driver for the Prom-vs-promshim rung-drift and wall-clock
// tripwire. See plan: .pi/benchmark-native-lowering.md (and the generated
// plan file under ~/.claude/plans). Non-goals: statistical rigor beyond
// p50/p95, CI gating, or long-term trend analysis. The goal is to catch
// complexity regressions on the native-SQL lowering path before they land.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type BenchConfig struct {
	PromURL        string
	ShimURL        string
	CorpusPath     string
	ArtifactDir    string
	Manifest       Manifest
	Repeats        int
	WarmupRepeats  int
	BaselinePath   string
	UpdateBaseline bool
	Timeout        time.Duration
}

type BenchRow struct {
	Name                string  `json:"name"`
	Query               string  `json:"query"`
	Endpoint            string  `json:"endpoint"`
	Strategy            string  `json:"strategy"`
	FallbackReason      string  `json:"fallbackReason,omitempty"`
	CHRoundtrips        int     `json:"chRoundtrips"`
	CHMillis            int     `json:"chMillis"`
	PromP50MS           float64 `json:"promP50Ms"`
	PromP95MS           float64 `json:"promP95Ms"`
	NativeP50MS         float64 `json:"nativeP50Ms"`
	NativeP95MS         float64 `json:"nativeP95Ms"`
	NativeUnsupported   bool    `json:"nativeUnsupported,omitempty"`
	FallbackP50MS       float64 `json:"fallbackP50Ms"`
	FallbackP95MS       float64 `json:"fallbackP95Ms"`
	NativePromRatio     float64 `json:"nativePromRatio"`
	FallbackNativeRatio float64 `json:"fallbackNativeRatio"`
	StrategyFlap        bool    `json:"strategyFlap,omitempty"`
	Error               string  `json:"error,omitempty"`
}

type BenchSummary struct {
	QueryCount        int            `json:"queryCount"`
	StrategyHistogram map[string]int `json:"strategyHistogram"`
	RegressionCount   int            `json:"regressionCount"`
}

type BenchReport struct {
	CorpusPath string       `json:"corpusPath"`
	Manifest   Manifest     `json:"manifest"`
	GeneratedAt string      `json:"generatedAt"`
	Rows       []BenchRow   `json:"rows"`
	Summary    BenchSummary `json:"summary"`
}

// RunBench drives each query in the corpus three ways — Prometheus (no
// native lowering knob), shim with native_lowering_mode=force_supported
// (rung-2 probe), and shim with native_lowering_mode=off (pure local
// evaluator). For each shim call it reads X-Promshim-* headers to pin
// strategy, fallback reason, and ClickHouse round-trip count. Returns
// a BenchReport and optionally writes it to ArtifactDir / BaselinePath.
func RunBench(cfg BenchConfig) (BenchReport, error) {
	if cfg.Repeats <= 0 {
		cfg.Repeats = 10
	}
	if cfg.WarmupRepeats < 0 {
		cfg.WarmupRepeats = 0
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	queries, err := LoadQueryCorpus(cfg.CorpusPath)
	if err != nil {
		return BenchReport{}, fmt.Errorf("load corpus %q: %w", cfg.CorpusPath, err)
	}
	client := &http.Client{Timeout: cfg.Timeout}
	report := BenchReport{
		CorpusPath:  cfg.CorpusPath,
		Manifest:    cfg.Manifest,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Rows:        make([]BenchRow, 0, len(queries)),
		Summary:     BenchSummary{StrategyHistogram: map[string]int{}},
	}
	for _, spec := range queries {
		row := benchOneQuery(client, cfg, spec)
		report.Rows = append(report.Rows, row)
		if row.Strategy != "" {
			report.Summary.StrategyHistogram[row.Strategy]++
		}
	}
	report.Summary.QueryCount = len(report.Rows)
	if cfg.ArtifactDir != "" {
		if err := writeBenchReport(cfg.ArtifactDir, report); err != nil {
			return report, err
		}
	}
	if cfg.UpdateBaseline && cfg.BaselinePath != "" {
		if err := writeBenchReportTo(cfg.BaselinePath, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func benchOneQuery(client *http.Client, cfg BenchConfig, spec QuerySpec) BenchRow {
	row := BenchRow{Name: spec.Name, Query: spec.Query, Endpoint: spec.Endpoint}

	// Prom baseline: no native lowering knob.
	promSpec := spec
	promSpec.NativeLoweringMode = ""
	promLatencies, _, err := repeatWithHeaders(client, cfg, cfg.PromURL, promSpec, cfg.WarmupRepeats, cfg.Repeats)
	if err != nil {
		row.Error = fmt.Sprintf("prom: %v", err)
		return row
	}
	row.PromP50MS = p50(promLatencies)
	row.PromP95MS = p95(promLatencies)

	// Shim native: force_supported. Errors are recorded as "unsupported"
	// rather than propagated — a query that cannot lower is a legitimate
	// state we want to see in the bench output.
	nativeSpec := spec
	nativeSpec.NativeLoweringMode = "force_supported"
	nativeLatencies, nativeSamples, nativeErr := repeatWithHeaders(client, cfg, cfg.ShimURL, nativeSpec, cfg.WarmupRepeats, cfg.Repeats)
	if nativeErr != nil {
		row.NativeUnsupported = true
		row.Error = fmt.Sprintf("native(force_supported): %v", nativeErr)
	} else {
		row.NativeP50MS = p50(nativeLatencies)
		row.NativeP95MS = p95(nativeLatencies)
		sample := nativeSamples[0]
		row.Strategy = sample.strategy
		row.FallbackReason = sample.fallbackReason
		row.CHRoundtrips = sample.roundtrips
		row.CHMillis = sample.millis
		row.StrategyFlap = detectStrategyFlap(nativeSamples)
	}

	// Shim fallback: native_lowering_mode=off (pure local evaluator).
	fallbackSpec := spec
	fallbackSpec.NativeLoweringMode = "off"
	fallbackLatencies, _, fallbackErr := repeatWithHeaders(client, cfg, cfg.ShimURL, fallbackSpec, cfg.WarmupRepeats, cfg.Repeats)
	if fallbackErr != nil {
		if row.Error == "" {
			row.Error = fmt.Sprintf("fallback(off): %v", fallbackErr)
		}
	} else {
		row.FallbackP50MS = p50(fallbackLatencies)
		row.FallbackP95MS = p95(fallbackLatencies)
	}

	row.NativePromRatio = safeRatio(row.NativeP50MS, row.PromP50MS)
	row.FallbackNativeRatio = safeRatio(row.FallbackP50MS, row.NativeP50MS)
	return row
}

type headerSample struct {
	strategy       string
	fallbackReason string
	roundtrips     int
	millis         int
}

func repeatWithHeaders(client *http.Client, cfg BenchConfig, baseURL string, spec QuerySpec, warmup, repeats int) ([]float64, []headerSample, error) {
	for i := 0; i < warmup; i++ {
		if _, _, err := timedRequest(client, baseURL, cfg.Manifest, spec); err != nil {
			return nil, nil, fmt.Errorf("warmup %d: %w", i+1, err)
		}
	}
	latencies := make([]float64, 0, repeats)
	samples := make([]headerSample, 0, repeats)
	for i := 0; i < repeats; i++ {
		ms, hdr, err := timedRequest(client, baseURL, cfg.Manifest, spec)
		if err != nil {
			return nil, nil, fmt.Errorf("repeat %d: %w", i+1, err)
		}
		latencies = append(latencies, ms)
		samples = append(samples, parseHeaders(hdr))
	}
	return latencies, samples, nil
}

func timedRequest(client *http.Client, baseURL string, manifest Manifest, spec QuerySpec) (float64, http.Header, error) {
	endpoint, err := buildQueryURL(baseURL, manifest, spec)
	if err != nil {
		return 0, nil, err
	}
	start := time.Now()
	response, err := client.Get(endpoint)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	// Drain and discard body — we only care about headers and wall-clock.
	// Draining is required so the TCP connection is reused.
	_, _ = io.Copy(io.Discard, response.Body)
	elapsed := time.Since(start)
	if response.StatusCode >= 400 {
		return 0, nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return float64(elapsed.Microseconds()) / 1000.0, response.Header, nil
}

func parseHeaders(h http.Header) headerSample {
	var s headerSample
	if h == nil {
		return s
	}
	s.strategy = h.Get("X-Promshim-Strategy")
	s.fallbackReason = h.Get("X-Promshim-Fallback-Reason")
	if v := h.Get("X-Promshim-CH-Roundtrips"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s.roundtrips = n
		}
	}
	if v := h.Get("X-Promshim-CH-Millis"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s.millis = n
		}
	}
	return s
}

func detectStrategyFlap(samples []headerSample) bool {
	if len(samples) < 2 {
		return false
	}
	first := samples[0].strategy
	for _, s := range samples[1:] {
		if s.strategy != first {
			return true
		}
	}
	return false
}

func p50(samples []float64) float64 {
	return percentile(samples, 0.50)
}

func p95(samples []float64) float64 {
	return percentile(samples, 0.95)
}

func percentile(samples []float64, q float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func safeRatio(num, den float64) float64 {
	if den <= 0 {
		return 0
	}
	return num / den
}

func writeBenchReport(dir string, report BenchReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeBenchReportTo(filepath.Join(dir, "bench-report.json"), report)
}

func writeBenchReportTo(path string, report BenchReport) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, payload, 0o644)
}

// ReadBenchReport loads a bench report from disk. Used for baseline
// comparison.
func ReadBenchReport(path string) (BenchReport, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return BenchReport{}, err
	}
	var report BenchReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return BenchReport{}, err
	}
	return report, nil
}
