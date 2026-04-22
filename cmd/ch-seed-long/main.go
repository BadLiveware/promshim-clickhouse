// ch-seed-long writes a long-range demo_* dataset directly to ClickHouse's
// Prometheus remote-write endpoint, backdating samples to a pinned window.
//
// ClickHouse MergeTree has no equivalent to Prometheus's
// out_of_order_time_window, so this program can pump an arbitrary number
// of days/weeks of samples in a single pass. Prometheus is bypassed
// entirely — this data feeds the long-range bench profile, not the
// compliance (Prom-vs-shim) comparison.
//
// Default end_time is pinned 30 days before the compliance fixture's
// end_time so the two datasets live in non-overlapping time windows
// inside the same observability.prometheus table.
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

	"ch-observability/internal/promharness"
)

// profiles maps named dashboard ranges to (end-time, duration, step).
// Each profile has a distinct end-time so the three datasets live in
// non-overlapping time windows inside the same observability.prometheus
// table. Step grows with duration to keep per-profile sample counts in the
// same order of magnitude — 7d@15s ≈ 5M, 30d@60s ≈ 5M, 1y@300s ≈ 14M.
var profiles = map[string]struct {
	endTime  string
	duration time.Duration
	step     time.Duration
}{
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
		endTimeFlag   = flag.String("end-time", "2026-03-22T21:45:42Z", "RFC3339 timestamp of the last sample. Pin this and reference it from the bench corpus.")
		duration      = flag.Duration("duration", 168*time.Hour, "Window size ending at --end-time (e.g. 168h for 7d, 720h for 30d).")
		step          = flag.Duration("step", 15*time.Second, "Sample interval per series.")
		jobsFlag      = flag.String("jobs", "demo-api,demo-worker", "Comma-separated job label values.")
		instancesFlag = flag.Int("instances-per-job", 5, "Number of instance label values per job.")
		batchSamples  = flag.Int("batch-samples", 50000, "Approximate samples per remote-write POST.")
		seed          = flag.Int64("seed", 42, "PRNG seed for gauge jitter (output is deterministic for a given seed).")
	)
	flag.Parse()

	if *profile != "" {
		p, ok := profiles[*profile]
		if !ok {
			log.Fatalf("unknown --profile %q (want: 7d | 30d | 1y)", *profile)
		}
		*endTimeFlag = p.endTime
		*duration = p.duration
		*step = p.step
		log.Printf("[seed-long] profile=%s end_time=%s duration=%s step=%s",
			*profile, p.endTime, p.duration, p.step)
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

	client := &http.Client{Timeout: 5 * time.Minute}
	fullEndpoint := withBasicAuth(*endpoint, *username, *password)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	rng := rand.New(rand.NewSource(*seed))
	state := newSeriesState(series, rng)

	// Choose a time-chunk size so each remote-write batch is ≈ batchSamples.
	pointsPerBatch := *batchSamples / len(series)
	if pointsPerBatch < 1 {
		pointsPerBatch = 1
	}

	batches := 0
	samples := 0
	batchStart := startTime
	for batchStart.Before(endTime) {
		batchEnd := batchStart.Add(time.Duration(pointsPerBatch) * *step)
		if batchEnd.After(endTime) {
			batchEnd = endTime
		}
		req := &prompb.WriteRequest{}
		for i := range series {
			points := advanceSeries(&series[i], &state[i], batchStart, batchEnd, *step)
			if len(points) == 0 {
				continue
			}
			req.Timeseries = append(req.Timeseries, prompb.TimeSeries{
				Labels:  series[i].labels,
				Samples: points,
			})
			samples += len(points)
		}
		if len(req.Timeseries) == 0 {
			break
		}
		if err := promharness.WriteToRemoteWriteEndpoint(ctx, client, fullEndpoint, req); err != nil {
			log.Fatalf("[seed-long] remote-write batch %d: %v", batches+1, err)
		}
		batches++
		log.Printf("[seed-long] batch %d: ts=%s..%s series=%d samples(total)=%d",
			batches, batchStart.Format(time.RFC3339), batchEnd.Format(time.RFC3339),
			len(req.Timeseries), samples)
		batchStart = batchEnd
	}

	log.Printf("[seed-long] done: %d batches, %d samples written", batches, samples)
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

