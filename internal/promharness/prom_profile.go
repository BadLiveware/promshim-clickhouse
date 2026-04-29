package promharness

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultPromProfileSampleInterval = 20 * time.Millisecond

var prometheusRuntimeMetricNames = []string{
	"process_resident_memory_bytes",
	"process_virtual_memory_bytes",
	"process_cpu_seconds_total",
	"go_memstats_heap_alloc_bytes",
	"go_memstats_heap_inuse_bytes",
	"go_memstats_heap_sys_bytes",
	"go_memstats_stack_inuse_bytes",
	"go_memstats_mallocs_total",
	"go_memstats_frees_total",
	"go_memstats_alloc_bytes_total",
	"go_memstats_gc_cpu_fraction",
	"go_goroutines",
}

type BenchPrometheusRuntimeProfile struct {
	Mode                   string             `json:"mode"`
	SampleIntervalMS       float64            `json:"sampleIntervalMs"`
	ForceGCBeforeMeasure   bool               `json:"forceGcBeforeMeasure"`
	WallMS                 float64            `json:"wallMs,omitempty"`
	SampleCount            int                `json:"sampleCount"`
	Metrics                map[string]float64 `json:"metrics,omitempty"`
	MetricBaselines        map[string]float64 `json:"metricBaselines,omitempty"`
	MetricMax              map[string]float64 `json:"metricMax,omitempty"`
	MetricMaxDeltas        map[string]float64 `json:"metricMaxDeltas,omitempty"`
	ProcessCPUSeconds      float64            `json:"processCpuSeconds,omitempty"`
	RSSMaxDeltaBytes       float64            `json:"rssMaxDeltaBytes,omitempty"`
	HeapAllocMaxDeltaBytes float64            `json:"heapAllocMaxDeltaBytes,omitempty"`
	HeapInuseMaxDeltaBytes float64            `json:"heapInuseMaxDeltaBytes,omitempty"`
	HeapSysMaxDeltaBytes   float64            `json:"heapSysMaxDeltaBytes,omitempty"`
	AllocBytesDelta        float64            `json:"allocBytesDelta,omitempty"`
	MallocsDelta           float64            `json:"mallocsDelta,omitempty"`
	FreesDelta             float64            `json:"freesDelta,omitempty"`
	Error                  string             `json:"error,omitempty"`
}

func capturePrometheusRuntimeProfile(client *http.Client, cfg BenchConfig, spec QuerySpec) BenchPrometheusRuntimeProfile {
	interval := cfg.PromProfileSampleInterval
	if interval <= 0 {
		interval = defaultPromProfileSampleInterval
	}
	profile := BenchPrometheusRuntimeProfile{
		Mode:                 "runtime",
		SampleIntervalMS:     float64(interval.Microseconds()) / 1000.0,
		ForceGCBeforeMeasure: true,
	}
	metricsClient := *client
	metricsClient.Timeout = 5 * time.Second
	if err := forcePrometheusHeapGC(&metricsClient, cfg.PromURL); err != nil {
		profile.Error = fmt.Sprintf("force gc: %v", err)
	}
	baseline, err := scrapePrometheusMetrics(&metricsClient, cfg.PromURL)
	if err != nil {
		if profile.Error != "" {
			profile.Error += "; "
		}
		profile.Error += fmt.Sprintf("baseline metrics: %v", err)
		return profile
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	maxMetrics := cloneFloatMap(baseline)
	sampleCount := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				metrics, err := scrapePrometheusMetrics(&metricsClient, cfg.PromURL)
				if err != nil {
					continue
				}
				mu.Lock()
				sampleCount++
				for key, value := range metrics {
					if current, ok := maxMetrics[key]; !ok || value > current {
						maxMetrics[key] = value
					}
				}
				mu.Unlock()
			}
		}
	}()

	timing, _, requestErr := timedRequest(client, cfg.PromURL, cfg, spec)
	close(stop)
	wg.Wait()
	endMetrics, endErr := scrapePrometheusMetrics(&metricsClient, cfg.PromURL)
	mu.Lock()
	profile.SampleCount = sampleCount
	profile.MetricBaselines = cloneFloatMap(baseline)
	profile.MetricMax = cloneFloatMap(maxMetrics)
	mu.Unlock()
	profile.WallMS = timing.TotalMS
	if requestErr != nil {
		profile.Error = appendProfileError(profile.Error, fmt.Sprintf("query: %v", requestErr))
	}
	if endErr != nil {
		profile.Error = appendProfileError(profile.Error, fmt.Sprintf("end metrics: %v", endErr))
	} else {
		for key, value := range endMetrics {
			if current, ok := profile.MetricMax[key]; !ok || value > current {
				profile.MetricMax[key] = value
			}
		}
		profile.Metrics = cloneFloatMap(endMetrics)
	}
	profile.MetricMaxDeltas = map[string]float64{}
	for _, name := range prometheusRuntimeMetricNames {
		base, ok := baseline[name]
		if !ok {
			continue
		}
		if maxValue, ok := profile.MetricMax[name]; ok {
			profile.MetricMaxDeltas[name] = maxValue - base
		}
	}
	if endErr == nil {
		profile.ProcessCPUSeconds = metricDelta(endMetrics, baseline, "process_cpu_seconds_total")
		profile.AllocBytesDelta = metricDelta(endMetrics, baseline, "go_memstats_alloc_bytes_total")
		profile.MallocsDelta = metricDelta(endMetrics, baseline, "go_memstats_mallocs_total")
		profile.FreesDelta = metricDelta(endMetrics, baseline, "go_memstats_frees_total")
	}
	profile.RSSMaxDeltaBytes = profile.MetricMaxDeltas["process_resident_memory_bytes"]
	profile.HeapAllocMaxDeltaBytes = profile.MetricMaxDeltas["go_memstats_heap_alloc_bytes"]
	profile.HeapInuseMaxDeltaBytes = profile.MetricMaxDeltas["go_memstats_heap_inuse_bytes"]
	profile.HeapSysMaxDeltaBytes = profile.MetricMaxDeltas["go_memstats_heap_sys_bytes"]
	return profile
}

func forcePrometheusHeapGC(client *http.Client, baseURL string) error {
	url := strings.TrimRight(baseURL, "/") + "/debug/pprof/heap?gc=1"
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func scrapePrometheusMetrics(client *http.Client, baseURL string) (map[string]float64, error) {
	url := strings.TrimRight(baseURL, "/") + "/metrics"
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	wanted := map[string]struct{}{}
	for _, name := range prometheusRuntimeMetricNames {
		wanted[name] = struct{}{}
	}
	out := map[string]float64{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := parsePrometheusMetricLine(line)
		if !ok {
			continue
		}
		if _, keep := wanted[name]; !keep {
			continue
		}
		if _, exists := out[name]; exists {
			continue
		}
		out[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parsePrometheusMetricLine(line string) (string, float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0, false
	}
	name := fields[0]
	if idx := strings.IndexByte(name, '{'); idx >= 0 {
		name = name[:idx]
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return "", 0, false
	}
	return name, value, true
}

func metricDelta(current, baseline map[string]float64, name string) float64 {
	currentValue, currentOK := current[name]
	baselineValue, baselineOK := baseline[name]
	if !currentOK || !baselineOK {
		return 0
	}
	return currentValue - baselineValue
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func appendProfileError(existing, next string) string {
	if strings.TrimSpace(existing) == "" {
		return next
	}
	return existing + "; " + next
}
