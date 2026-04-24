package promharness

// Benchmark driver for the Prom-vs-promshim rung-drift and wall-clock
// tripwire. Non-goals: statistical rigor beyond p50/p95, CI gating, or
// long-term trend analysis. The goal is to catch complexity regressions
// on the native-SQL lowering path before they land.

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
	"strings"
	"time"
)

type BenchConfig struct {
	PromURL        string
	ShimURL        string
	CorpusPath     string
	ArtifactDir    string
	ArtifactName   string
	Manifest       Manifest
	Repeats        int
	WarmupRepeats  int
	BaselinePath   string
	UpdateBaseline bool
	Timeout        time.Duration
	ShimModes      []string
	IncludeProm    bool
	IncludePromSet bool
	RunLabels      map[string]string
	MemoryMode     string
}

type BenchRow struct {
	Name                string  `json:"name"`
	Query               string  `json:"query"`
	Endpoint            string  `json:"endpoint"`
	Category            string  `json:"category,omitempty"`
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

type BenchTiming struct {
	P50MS float64 `json:"p50Ms"`
	P95MS float64 `json:"p95Ms"`
}

type BenchShimModeResult struct {
	BenchTiming
	Strategy       string `json:"strategy,omitempty"`
	FallbackReason string `json:"fallbackReason,omitempty"`
	CHRoundtrips   int    `json:"chRoundtrips"`
	CHMillis       int    `json:"chMillis"`
	Unsupported    bool   `json:"unsupported,omitempty"`
	StrategyFlap   bool   `json:"strategyFlap,omitempty"`
	Error          string `json:"error,omitempty"`
}

type BenchRowV2 struct {
	Name            string                         `json:"name"`
	Query           string                         `json:"query"`
	Endpoint        string                         `json:"endpoint"`
	Category        string                         `json:"category,omitempty"`
	TargetPromP50MS *TargetPromP50MS               `json:"targetPromP50Ms,omitempty"`
	PromBand        string                         `json:"promBand,omitempty"`
	Prom            *BenchTiming                   `json:"prom,omitempty"`
	Shim            map[string]BenchShimModeResult `json:"shim"`
	Ratios          map[string]float64             `json:"ratios,omitempty"`
	Error           string                         `json:"error,omitempty"`
}

type BenchReportV2 struct {
	SchemaVersion int               `json:"schemaVersion"`
	CorpusPath    string            `json:"corpusPath"`
	Manifest      Manifest          `json:"manifest"`
	GeneratedAt   string            `json:"generatedAt"`
	RunLabels     map[string]string `json:"runLabels,omitempty"`
	MemoryMode    string            `json:"memoryMode,omitempty"`
	Rows          []BenchRowV2      `json:"rows"`
	Summary       BenchSummary      `json:"summary"`
}

type BenchReport struct {
	CorpusPath  string       `json:"corpusPath"`
	Manifest    Manifest     `json:"manifest"`
	GeneratedAt string       `json:"generatedAt"`
	Rows        []BenchRow   `json:"rows"`
	Summary     BenchSummary `json:"summary"`
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

func RunBenchV2(cfg BenchConfig) (BenchReportV2, error) {
	cfg = normalizeBenchConfig(cfg)
	queries, err := LoadQueryCorpus(cfg.CorpusPath)
	if err != nil {
		return BenchReportV2{}, fmt.Errorf("load corpus %q: %w", cfg.CorpusPath, err)
	}
	client := &http.Client{Timeout: cfg.Timeout}
	report := BenchReportV2{
		SchemaVersion: 2,
		CorpusPath:    cfg.CorpusPath,
		Manifest:      cfg.Manifest,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		RunLabels:     cloneStringMap(cfg.RunLabels),
		MemoryMode:    cfg.MemoryMode,
		Rows:          make([]BenchRowV2, 0, len(queries)),
		Summary:       BenchSummary{StrategyHistogram: map[string]int{}},
	}
	for _, spec := range queries {
		row := benchOneQueryV2(client, cfg, spec)
		report.Rows = append(report.Rows, row)
		for mode, result := range row.Shim {
			if result.Strategy != "" {
				report.Summary.StrategyHistogram[mode+":"+result.Strategy]++
			}
		}
	}
	report.Summary.QueryCount = len(report.Rows)
	if cfg.ArtifactDir != "" {
		name := cfg.ArtifactName
		if name == "" {
			name = "bench-report-v2.json"
		}
		if err := writeBenchReportV2To(filepath.Join(cfg.ArtifactDir, name), report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func normalizeBenchConfig(cfg BenchConfig) BenchConfig {
	if cfg.Repeats <= 0 {
		cfg.Repeats = 10
	}
	if cfg.WarmupRepeats < 0 {
		cfg.WarmupRepeats = 0
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if len(cfg.ShimModes) == 0 {
		cfg.ShimModes = []string{"force_supported", "off"}
	}
	if !cfg.IncludePromSet {
		cfg.IncludeProm = true
	}
	return cfg
}

func benchOneQueryV2(client *http.Client, cfg BenchConfig, spec QuerySpec) BenchRowV2 {
	row := BenchRowV2{Name: spec.Name, Query: spec.Query, Endpoint: spec.Endpoint, Category: spec.Category, TargetPromP50MS: spec.TargetPromP50MS, PromBand: "n/a", Shim: map[string]BenchShimModeResult{}}
	var promP50 float64
	if cfg.IncludeProm {
		promSpec := spec
		promSpec.NativeLoweringMode = ""
		promLatencies, _, err := repeatWithHeaders(client, cfg, cfg.PromURL, promSpec, cfg.WarmupRepeats, cfg.Repeats)
		if err != nil {
			row.Error = fmt.Sprintf("prom: %v", err)
		} else {
			row.Prom = &BenchTiming{P50MS: p50(promLatencies), P95MS: p95(promLatencies)}
			promP50 = row.Prom.P50MS
			row.PromBand = classifyPromBand(promP50, spec.TargetPromP50MS)
		}
	}

	for _, mode := range cfg.ShimModes {
		modeSpec := spec
		modeSpec.NativeLoweringMode = mode
		latencies, samples, err := repeatWithHeaders(client, cfg, cfg.ShimURL, modeSpec, cfg.WarmupRepeats, cfg.Repeats)
		result := BenchShimModeResult{}
		if err != nil {
			result.Error = fmt.Sprintf("%s: %v", mode, err)
			if mode == "force_supported" {
				result.Unsupported = true
			}
		} else {
			result.P50MS = p50(latencies)
			result.P95MS = p95(latencies)
			if len(samples) > 0 {
				sample := samples[0]
				result.Strategy = sample.strategy
				result.FallbackReason = sample.fallbackReason
				result.CHRoundtrips = sample.roundtrips
				result.CHMillis = sample.millis
			}
			result.StrategyFlap = detectStrategyFlap(samples)
		}
		row.Shim[mode] = result
	}

	if promP50 > 0 {
		row.Ratios = map[string]float64{}
		for mode, result := range row.Shim {
			if result.P50MS > 0 {
				row.Ratios[mode+"Prom"] = safeRatio(result.P50MS, promP50)
			}
		}
	}
	return row
}

func classifyPromBand(promP50 float64, target *TargetPromP50MS) string {
	if target == nil || target.Min <= 0 || target.Max <= 0 || promP50 <= 0 {
		return "n/a"
	}
	if promP50 < target.Min {
		return "too_fast"
	}
	if promP50 > target.Max {
		return "too_slow"
	}
	return "in_band"
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func benchOneQuery(client *http.Client, cfg BenchConfig, spec QuerySpec) BenchRow {
	row := BenchRow{Name: spec.Name, Query: spec.Query, Endpoint: spec.Endpoint, Category: spec.Category}

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
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	if spec.Name != "" {
		request.Header.Set("X-Promshim-Log-Comment", benchLogComment(spec))
	}
	start := time.Now()
	response, err := client.Do(request)
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

func benchLogComment(spec QuerySpec) string {
	mode := spec.NativeLoweringMode
	if mode == "" {
		mode = "prom"
	}
	return "promshim-bench query=" + sanitizeLogCommentPart(spec.Name) + " mode=" + sanitizeLogCommentPart(mode)
}

func sanitizeLogCommentPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
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
	return writeJSONFile(path, report)
}

func writeBenchReportV2To(path string, report BenchReportV2) error {
	return writeJSONFile(path, report)
}

func writeJSONFile(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
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
