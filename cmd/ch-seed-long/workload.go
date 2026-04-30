package main

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

var realisticBucketLE = []string{"0.005", "0.01", "0.025", "0.05", "0.1", "0.25", "0.5", "1", "2.5", "5", "10", "+Inf"}

func resolveWorkloadProfile(flagValue, activeLabel string) string {
	if flagValue != "" && flagValue != "auto" {
		return flagValue
	}
	switch activeLabel {
	case "dashboard-50k", "realistic-50k":
		return "dashboard"
	case "envoy-heavy-50k":
		return "envoy-heavy"
	case "churn-50k":
		return "churn"
	default:
		return "legacy"
	}
}

func buildWorkloadSeries(profile string, target int, jobs []string, start, end time.Time, duration, baseStep time.Duration, seed int64) ([]seriesDesc, error) {
	if target <= 0 {
		return nil, fmt.Errorf("target active series must be > 0")
	}
	if baseStep <= 0 {
		return nil, fmt.Errorf("base step must be > 0")
	}
	b := workloadBuilder{target: target, jobs: jobs, start: start, end: end, duration: duration, baseStep: baseStep, rng: rand.New(rand.NewSource(seed))}
	switch profile {
	case "dashboard":
		b.addHistogramFamily(int(math.Round(float64(target)*0.65)), 60*time.Second, 0.70, 0.0, "demo-api", []string{"stable_histogram", "tail_spike_histogram", "bimodal_histogram"})
		b.addCounterFamily(int(math.Round(float64(target)*0.12)), 15*time.Second, 1.0, 0.0, []string{"steady_counter", "bursty_counter", "resetting_counter"})
		b.addGaugeFamily(int(math.Round(float64(target)*0.18)), 60*time.Second, 0.75, 0.0, []string{"smooth_gauge", "sawtooth_gauge", "plateau_gauge"})
		b.addSparseCounterFamily(target - len(b.out))
	case "envoy-heavy":
		b.addHistogramFamily(int(math.Round(float64(target)*0.82)), 15*time.Second, 1.0, 0.0, "envoy-gateway-system/envoy-gateway-proxy-monitoring", []string{"stable_histogram", "tail_spike_histogram", "bimodal_histogram"})
		b.addGaugeFamily(int(math.Round(float64(target)*0.10)), 60*time.Second, 0.85, 0.0, []string{"smooth_gauge", "sawtooth_gauge", "plateau_gauge"})
		b.addCounterFamily(int(math.Round(float64(target)*0.05)), 15*time.Second, 1.0, 0.0, []string{"steady_counter", "bursty_counter", "resetting_counter"})
		b.addSparseCounterFamily(target - len(b.out))
	case "churn":
		b.addHistogramFamily(int(math.Round(float64(target)*0.50)), 15*time.Second, 0.40, 0.20, "demo-api", []string{"stable_histogram", "tail_spike_histogram", "bimodal_histogram"})
		b.addCounterFamily(int(math.Round(float64(target)*0.15)), 15*time.Second, 0.55, 0.15, []string{"steady_counter", "bursty_counter", "resetting_counter"})
		b.addGaugeFamily(int(math.Round(float64(target)*0.25)), 60*time.Second, 0.45, 0.20, []string{"smooth_gauge", "sawtooth_gauge", "plateau_gauge"})
		b.addSparseCounterFamily(target - len(b.out))
	default:
		return nil, fmt.Errorf("unknown workload profile %q (want legacy|dashboard|envoy-heavy|churn)", profile)
	}
	if len(b.out) > target {
		b.out = b.out[:target]
	}
	return b.out, nil
}

type workloadBuilder struct {
	target   int
	jobs     []string
	start    time.Time
	end      time.Time
	duration time.Duration
	baseStep time.Duration
	rng      *rand.Rand
	out      []seriesDesc
}

func (b *workloadBuilder) add(metric, kind string, lbls map[string]string, base, amp float64, leIdx, bucketCount int, interval time.Duration, activeFraction, endedFraction float64, shape string) {
	if len(b.out) >= b.target {
		return
	}
	full := map[string]string{"__name__": metric}
	for k, v := range lbls {
		full[k] = v
	}
	idx := len(b.out)
	activeStart, activeEnd := b.activeWindow(activeFraction, endedFraction, idx)
	b.out = append(b.out, seriesDesc{
		labels:       sortedLabels(full),
		kind:         kind,
		base:         base,
		amp:          amp,
		leIdx:        leIdx,
		bucketCount:  bucketCount,
		seriesIndex:  idx,
		shape:        shape,
		sampleEvery:  sampleEvery(interval, b.baseStep),
		sampleOffset: idx % max(1, sampleEvery(interval, b.baseStep)),
		activeStart:  activeStart,
		activeEnd:    activeEnd,
	})
}

func (b *workloadBuilder) activeWindow(activeFraction, endedFraction float64, idx int) (time.Time, time.Time) {
	if activeFraction <= 0 || activeFraction >= 1 {
		if endedFraction > 0 && b.rng.Float64() < endedFraction {
			length := time.Duration(float64(b.duration) * 0.35)
			startOffset := time.Duration(float64(b.duration) * b.rng.Float64() * 0.45)
			return b.start.Add(startOffset), b.start.Add(startOffset).Add(length)
		}
		return time.Time{}, time.Time{}
	}
	length := time.Duration(float64(b.duration) * activeFraction)
	if endedFraction > 0 && b.rng.Float64() < endedFraction {
		maxStart := b.duration - length
		if maxStart < 0 {
			maxStart = 0
		}
		startOffset := time.Duration(float64(maxStart) * b.rng.Float64())
		return b.start.Add(startOffset), b.start.Add(startOffset).Add(length)
	}
	// Most realistic non-ended series are active at eval but were not present
	// for the entire historical window, modeling deployment churn without
	// making instant queries empty at the benchmark evaluation time.
	jitter := time.Duration(float64(b.duration-length) * float64(idx%17) / 17.0)
	activeStart := b.end.Add(-length).Add(-jitter / 8)
	if activeStart.Before(b.start) {
		activeStart = b.start
	}
	return activeStart, time.Time{}
}

func (b *workloadBuilder) addHistogramFamily(seriesBudget int, interval time.Duration, activeFraction, endedFraction float64, preferredJob string, shapes []string) {
	if seriesBudget <= 0 {
		return
	}
	bucketCount := len(realisticBucketLE)
	entities := max(1, seriesBudget/bucketCount)
	for i := 0; i < entities; i++ {
		job := preferredJob
		if job == "" {
			job = b.job(i)
		}
		common := map[string]string{
			"job":       job,
			"namespace": namespaceForJob(job, i),
			"instance":  fmt.Sprintf("demo.promlabs.com:%d", 10000+i%4096),
			"route":     fmt.Sprintf("/api/%03d", i%128),
			"status":    []string{"200", "200", "200", "500"}[i%4],
			"upstream":  fmt.Sprintf("cluster-%03d", i%256),
		}
		shape := shapes[i%len(shapes)]
		for leIdx, le := range realisticBucketLE {
			lbls := cloneWith(common, "le", le)
			b.add("demo_api_request_duration_seconds_bucket", "histogram_bucket", lbls, 0, 0, leIdx, bucketCount, interval, activeFraction, endedFraction, shape)
		}
	}
}

func (b *workloadBuilder) addCounterFamily(seriesBudget int, interval time.Duration, activeFraction, endedFraction float64, shapes []string) {
	modes := []string{"idle", "user", "system"}
	entities := max(1, seriesBudget/len(modes))
	for i := 0; i < entities; i++ {
		common := map[string]string{"job": b.job(i), "namespace": namespaceForJob(b.job(i), i), "instance": fmt.Sprintf("demo.promlabs.com:%d", 20000+i%4096)}
		for modeIdx, mode := range modes {
			lbls := cloneWith(common, "mode", mode)
			b.add("demo_cpu_usage_seconds_total", "counter", lbls, 0, 0, 0, 0, interval, activeFraction, endedFraction, shapes[(i+modeIdx)%len(shapes)])
		}
	}
}

func (b *workloadBuilder) addGaugeFamily(seriesBudget int, interval time.Duration, activeFraction, endedFraction float64, shapes []string) {
	types := []string{"free", "cached", "used"}
	entities := max(1, seriesBudget/len(types))
	for i := 0; i < entities; i++ {
		common := map[string]string{"job": b.job(i), "namespace": namespaceForJob(b.job(i), i), "instance": fmt.Sprintf("demo.promlabs.com:%d", 30000+i%4096)}
		for typeIdx, typ := range types {
			lbls := cloneWith(common, "type", typ)
			b.add("demo_memory_usage_bytes", "gauge", lbls, 1<<30, float64(1<<28), 0, 0, interval, activeFraction, endedFraction, shapes[(i+typeIdx)%len(shapes)])
		}
	}
}

func (b *workloadBuilder) addSparseCounterFamily(seriesBudget int) {
	if seriesBudget <= 0 {
		return
	}
	for i := 0; i < seriesBudget; i++ {
		common := map[string]string{
			"job":       b.job(i),
			"namespace": namespaceForJob(b.job(i), i),
			"instance":  fmt.Sprintf("demo.promlabs.com:%d", 40000+i%4096),
			"code":      []string{"ok", "retry", "error"}[i%3],
		}
		b.add("demo_background_events_total", "counter", common, 0, 0, 0, 0, 5*time.Minute, 0.25, 0.10, "sparse_counter")
	}
}

func (b *workloadBuilder) job(i int) string {
	if len(b.jobs) == 0 {
		return "demo-api"
	}
	return b.jobs[i%len(b.jobs)]
}

func namespaceForJob(job string, i int) string {
	switch job {
	case "envoy-gateway-system/envoy-gateway-proxy-monitoring":
		return "envoy-gateway-system"
	case "demo-api":
		return "webapi"
	case "demo-worker":
		return "workers"
	default:
		if i%5 == 0 {
			return "default"
		}
		return "tradera-production"
	}
}

func sampleEvery(interval, baseStep time.Duration) int {
	if interval <= baseStep {
		return 1
	}
	return max(1, int(math.Round(float64(interval)/float64(baseStep))))
}

func estimateGeneratedSamples(series []seriesDesc, start, end time.Time, step time.Duration) int64 {
	var total int64
	for i := range series {
		effectiveStart := start
		if !series[i].activeStart.IsZero() && series[i].activeStart.After(effectiveStart) {
			effectiveStart = series[i].activeStart
		}
		effectiveEnd := end
		if !series[i].activeEnd.IsZero() && series[i].activeEnd.Before(effectiveEnd) {
			effectiveEnd = series[i].activeEnd
		}
		if !effectiveEnd.After(effectiveStart) {
			continue
		}
		points := int64(effectiveEnd.Sub(effectiveStart) / step)
		every := series[i].sampleEvery
		if every < 1 {
			every = 1
		}
		total += (points + int64(every) - 1) / int64(every)
	}
	return total
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
