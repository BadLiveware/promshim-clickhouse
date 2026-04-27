// ch-seed-long writes a long-range demo_* dataset to a Prometheus-compatible
// remote-write endpoint, backdating samples to a pinned benchmark window.
//
// The wrapper script can send the same generated dataset to ClickHouse and/or
// Prometheus. The data feeds long-range benchmark profiles, not the compliance
// fixture. Named sparse profiles use non-overlapping windows; dense profiles
// shift earlier again so sparse+dense data can coexist without duplicate
// samples at identical timestamps.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

// profiles maps named dashboard ranges to (end-time, duration, step).
// Each sparse profile has a distinct end-time so the datasets live in
// non-overlapping time windows inside the same observability.prometheus
// table. Step grows with duration to keep per-profile sample counts in the
// same order of magnitude — 7d@15s ≈ 5M, 30d@60s ≈ 5M, 1y@300s ≈ 14M.
type profileConfig struct {
	endTime  string
	duration time.Duration
	step     time.Duration
}

var profiles = map[string]profileConfig{
	"7d":  {"2026-03-22T21:45:42Z", 7 * 24 * time.Hour, 15 * time.Second},
	"30d": {"2026-02-22T21:45:42Z", 30 * 24 * time.Hour, 60 * time.Second},
	"1y":  {"2025-03-22T21:45:42Z", 365 * 24 * time.Hour, 300 * time.Second},
}

func main() {
	var (
		endpoint      = flag.String("endpoint", "http://localhost:29092/write", "ClickHouse remote-write endpoint.")
		username      = flag.String("username", "default", "Basic-auth username for the remote-write endpoint.")
		password      = flag.String("password", "otel", "Basic-auth password for the remote-write endpoint.")
		profile       = flag.String("profile", "", "Named dashboard profile: 7d | 30d | 1y. Sets --end-time, --duration, --step. Overrides those flags when set.")
		density       = flag.String("density", "sparse", "Dataset density: sparse | dense | stress-50k | stress-500k. Higher densities use non-overlapping windows and higher cardinality unless --instances-per-job is explicitly set.")
		endTimeFlag   = flag.String("end-time", "2026-03-22T21:45:42Z", "RFC3339 timestamp of the last sample. Pin this and reference it from the bench corpus.")
		duration      = flag.Duration("duration", 168*time.Hour, "Window size ending at --end-time (e.g. 168h for 7d, 720h for 30d).")
		step          = flag.Duration("step", 15*time.Second, "Sample interval per series.")
		jobsFlag      = flag.String("jobs", "demo-api,demo-worker", "Comma-separated job label values.")
		instancesFlag = flag.Int("instances-per-job", 5, "Number of instance label values per job.")
		batchSamples  = flag.Int("batch-samples", 50000, "Approximate samples per remote-write POST.")
		seed          = flag.Int64("seed", 42, "PRNG seed for gauge jitter (output is deterministic for a given seed).")
		maxConcurrent = flag.Int("max-concurrency", 8, "Maximum number of in-flight remote-write POSTs.")
		initConcurrent = flag.Int("initial-concurrency", 2, "Starting concurrency for the AIMD regulator. Ignored when --no-adaptive is set.")
		noAdaptive    = flag.Bool("no-adaptive", false, "Disable the AIMD regulator and run with fixed --max-concurrency workers (deterministic mode).")
		probeInterval = flag.Duration("probe-interval", 5*time.Second, "How often the health-probe goroutine polls signals (host load, CH, Prom). Zero disables all probes.")
		chProbeURL    = flag.String("ch-probe-url", "", "ClickHouse HTTP URL for health probes (e.g. http://localhost:28124). Empty disables CH probing.")
		promProbeURL  = flag.String("prom-probe-url", "", "Prometheus HTTP URL for health probes (e.g. http://localhost:29190). Empty disables Prom probing.")
		maxHostLoadPct = flag.Float64("max-host-load-pct", 50.0, "Throttle when 1-min load average exceeds this percentage of NumCPU. Default 50 leaves the machine usable for other work; raise to 80–90 in CI or on dedicated bench hosts. Set to 0 to disable host-load probing.")
	)
	flag.Parse()

	instancesExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "instances-per-job" {
			instancesExplicit = true
		}
	})

	switch *density {
	case "sparse", "dense", "stress-50k", "stress-500k":
	default:
		log.Fatalf("unknown --density %q (want: sparse | dense | stress-50k | stress-500k)", *density)
	}

	if *profile != "" {
		p, ok := profiles[*profile]
		if !ok {
			log.Fatalf("unknown --profile %q (want: 7d | 30d | 1y)", *profile)
		}
		*endTimeFlag = profileEndTime(p, *density)
		*duration = p.duration
		*step = p.step
		if !instancesExplicit {
			*instancesFlag = defaultInstancesPerJob(*profile, *density)
		}
		log.Printf("[seed-long] profile=%s density=%s end_time=%s duration=%s step=%s instances_per_job=%d",
			*profile, *density, *endTimeFlag, p.duration, p.step, *instancesFlag)
	} else if !instancesExplicit && *density != "sparse" {
		*instancesFlag = defaultInstancesPerJob("", *density)
	}

	endTime, err := time.Parse(time.RFC3339, *endTimeFlag)
	if err != nil {
		log.Fatalf("parse --end-time: %v", err)
	}
	if *duration <= 0 {
		log.Fatalf("--duration must be > 0")
	}
	if *step <= 0 {
		log.Fatalf("--step must be > 0")
	}
	if *instancesFlag <= 0 {
		log.Fatalf("--instances-per-job must be > 0")
	}
	jobs := strings.Split(*jobsFlag, ",")
	if len(jobs) == 0 {
		log.Fatalf("--jobs is empty")
	}

	startTime := endTime.Add(-*duration)
	totalPoints := int(duration.Seconds() / step.Seconds())
	log.Printf("[seed-long] window=%s → %s step=%s points_per_series=%d jobs=%d instances_per_job=%d",
		startTime.Format(time.RFC3339), endTime.Format(time.RFC3339),
		step, totalPoints, len(jobs), *instancesFlag)

	// Build series descriptors up front, then stream samples in time-ordered
	// batches. Streaming (rather than buffering everything) keeps memory
	// bounded for 30-day windows.
	series := buildSeriesDescriptors(jobs, *instancesFlag)
	log.Printf("[seed-long] %d series, %d points/series ≈ %d total samples",
		len(series), totalPoints, len(series)*totalPoints)

	fullEndpoint := withBasicAuth(*endpoint, *username, *password)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	rng := rand.New(rand.NewSource(*seed))
	state := newSeriesState(series, rng)

	stats, err := runStream(ctx, streamConfig{
		Endpoint:           fullEndpoint,
		StartTime:          startTime,
		EndTime:            endTime,
		Step:               *step,
		BatchSamples:       *batchSamples,
		Series:             series,
		State:              state,
		MaxConcurrency:     *maxConcurrent,
		InitialConcurrency: *initConcurrent,
		NoAdaptive:         *noAdaptive,
		ProbeInterval:      *probeInterval,
		CHURL:              *chProbeURL,
		CHUsername:         *username,
		CHPassword:         *password,
		PromURL:            *promProbeURL,
		MaxHostLoadPct:     *maxHostLoadPct,
	})
	if err != nil {
		log.Fatalf("[seed-long] stream: %v", err)
	}

	// Single-threaded marker post — runs after the parallel stream drains so
	// it's guaranteed to be the latest sample for the marker series.
	markerClient := &http.Client{Timeout: 30 * time.Second}
	marker := seedMarkerRequest(*profile, *density, jobs, *instancesFlag, *duration, *step, *seed, endTime)
	if err := promharness.WriteToRemoteWriteEndpoint(ctx, markerClient, fullEndpoint, marker); err != nil {
		log.Fatalf("[seed-long] remote-write seed marker: %v", err)
	}
	log.Printf("[seed-long] seed marker written at %s", endTime.Format(time.RFC3339))

	log.Printf("[seed-long] done: %d batches, %d samples written, %d errors, wallclock=%s, observed concurrency [%d..%d]",
		stats.Batches, stats.Samples, stats.Errors, stats.Wallclock.Round(time.Second),
		stats.MinObservedN, stats.MaxObservedN)
}

func profileEndTime(p profileConfig, density string) string {
	slot := densitySlot(density)
	if slot == 0 {
		return p.endTime
	}
	endTime, err := time.Parse(time.RFC3339, p.endTime)
	if err != nil {
		panic(err)
	}
	// Densities other than sparse live in non-overlapping windows immediately
	// before the sparse window, each separated by a one-day gap. This avoids
	// duplicate series at identical timestamps when multiple densities are
	// pre-seeded into the same observability.prometheus table.
	offset := -time.Duration(slot)*p.duration - time.Duration(slot)*24*time.Hour
	return endTime.Add(offset).Format(time.RFC3339)
}

// densitySlot assigns each density a non-overlapping window index. Slot 0 is
// the canonical sparse window; higher slots shift earlier in time.
func densitySlot(density string) int {
	switch density {
	case "sparse":
		return 0
	case "dense":
		return 1
	case "stress-50k":
		return 2
	case "stress-500k":
		return 3
	default:
		return 0
	}
}

func defaultInstancesPerJob(profile, density string) int {
	switch density {
	case "dense":
		if profile == "1y" {
			return 50
		}
		return 100
	case "stress-50k":
		// 1924 instances/job × 2 jobs × 13 series_per_instance = 50,024 series.
		return 1924
	case "stress-500k":
		// 19231 instances/job × 2 jobs × 13 series_per_instance = 500,006 series.
		return 19231
	default:
		return 5
	}
}

func seedMarkerRequest(profile, density string, jobs []string, instancesPerJob int, duration, step time.Duration, seed int64, endTime time.Time) *prompb.WriteRequest {
	if profile == "" {
		profile = "custom"
	}
	labels := sortedLabels(map[string]string{
		"__name__":          "promshim_seed_info",
		"profile":           profile,
		"density":           density,
		"generator":         "ch-seed-long",
		"seed":              fmt.Sprintf("%d", seed),
		"jobs":              fmt.Sprintf("%d", len(jobs)),
		"job_values":        strings.Join(jobs, ","),
		"instances_per_job": fmt.Sprintf("%d", instancesPerJob),
		"duration_seconds":  fmt.Sprintf("%.0f", duration.Seconds()),
		"step_seconds":      fmt.Sprintf("%.0f", step.Seconds()),
	})
	return &prompb.WriteRequest{Timeseries: []prompb.TimeSeries{{
		Labels: labels,
		Samples: []prompb.Sample{{
			Timestamp: endTime.UnixMilli(),
			Value:     1,
		}},
	}}}
}

// seriesDesc describes one time series — its labels and which generator
// function produces its sample values.
type seriesDesc struct {
	labels []prompb.Label
	kind   string // "counter" | "gauge" | "histogram_bucket"
	base   float64
	amp    float64
	leIdx  int // only used for histogram buckets
}

type seriesState struct {
	counter float64 // monotonic accumulator (+ occasional resets for counters)
}

var bucketLE = []string{"0.1", "0.2", "0.5", "1", "+Inf"}

func buildSeriesDescriptors(jobs []string, instancesPerJob int) []seriesDesc {
	var out []seriesDesc
	add := func(metric, kind string, lbls map[string]string, base, amp float64, leIdx int) {
		full := map[string]string{"__name__": metric}
		for k, v := range lbls {
			full[k] = v
		}
		out = append(out, seriesDesc{
			labels: sortedLabels(full),
			kind:   kind,
			base:   base,
			amp:    amp,
			leIdx:  leIdx,
		})
	}
	modes := []string{"idle", "user", "system"}
	memTypes := []string{"free", "cached", "used"}
	for _, job := range jobs {
		for i := 0; i < instancesPerJob; i++ {
			instance := fmt.Sprintf("demo.promlabs.com:%d", 10000+i)
			common := map[string]string{"job": job, "instance": instance}

			for _, mode := range modes {
				lbls := cloneWith(common, "mode", mode)
				add("demo_cpu_usage_seconds_total", "counter", lbls, 0, 0, 0)
			}
			for _, t := range memTypes {
				lbls := cloneWith(common, "type", t)
				add("demo_memory_usage_bytes", "gauge", lbls, 1<<30, float64(1<<28), 0)
			}
			add("demo_api_request_duration_seconds_count", "counter", common, 0, 0, 0)
			add("demo_api_request_duration_seconds_sum", "counter", common, 0, 0, 0)
			for leIdx, le := range bucketLE {
				lbls := cloneWith(common, "le", le)
				add("demo_api_request_duration_seconds_bucket", "histogram_bucket", lbls, 0, 0, leIdx)
			}
		}
	}
	return out
}

func newSeriesState(series []seriesDesc, rng *rand.Rand) []seriesState {
	states := make([]seriesState, len(series))
	for i := range series {
		// Seed counters with a small random offset so they aren't all zero at window start.
		if series[i].kind == "counter" || series[i].kind == "histogram_bucket" {
			states[i].counter = rng.Float64() * 10
		}
	}
	return states
}

func advanceSeries(desc *seriesDesc, state *seriesState, start, end time.Time, step time.Duration) []prompb.Sample {
	var points []prompb.Sample
	for ts := start; ts.Before(end); ts = ts.Add(step) {
		var value float64
		switch desc.kind {
		case "counter":
			// Deterministic non-negative increment; occasional larger bump for
			// realism. No resets by default — adding a rare reset would help
			// test counter_reset handling but adds variance to scan-work numbers.
			state.counter += 0.5 + float64(ts.Unix()%7)*0.1
			value = state.counter
		case "histogram_bucket":
			// Cumulative bucket counts grow faster for lower-latency buckets.
			weight := 1.0 + float64(len(bucketLE)-desc.leIdx)
			state.counter += weight * (0.2 + float64(ts.Unix()%5)*0.05)
			value = state.counter
		case "gauge":
			// Smooth sine wave with period = 1h so `rate`/`avg_over_time` over
			// multi-hour windows see meaningful variation.
			phase := float64(ts.Unix()) * 2 * math.Pi / 3600.0
			value = desc.base + desc.amp*math.Sin(phase)
		}
		points = append(points, prompb.Sample{
			Timestamp: ts.UnixMilli(),
			Value:     value,
		})
	}
	return points
}

func cloneWith(base map[string]string, key, val string) map[string]string {
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out[key] = val
	return out
}

func sortedLabels(m map[string]string) []prompb.Label {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]prompb.Label, 0, len(keys))
	for _, k := range keys {
		out = append(out, prompb.Label{Name: k, Value: m[k]})
	}
	return out
}

// withBasicAuth embeds user:password into the URL so WriteToRemoteWriteEndpoint
// (which already handles userinfo) applies Basic auth.
func withBasicAuth(raw, user, pass string) string {
	if user == "" {
		return raw
	}
	// Split on '//' so we can splice userinfo after the scheme.
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return raw
	}
	return raw[:idx+3] + user + ":" + pass + "@" + raw[idx+3:]
}
