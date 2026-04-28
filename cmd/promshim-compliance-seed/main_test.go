package main

import (
	"math"
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

func TestFixtureWriteRequestIncludesEdgePatterns(t *testing.T) {
	end := time.Date(2026, 4, 21, 21, 45, 42, 0, time.UTC)
	start := end.Add(-2 * time.Hour)
	req := fixtureWriteRequest(start, end, 5*time.Second)

	seriesByMetric := map[string]int{}
	for _, ts := range req.Timeseries {
		seriesByMetric[labelValue(ts.Labels, "__name__")]++
	}
	wantSeries := map[string]int{
		"demo_memory_usage_bytes":                  12,
		"demo_cpu_usage_seconds_total":             9,
		"demo_api_request_duration_seconds_bucket": 702,
		"demo_intermittent_metric":                 3,
		"promshim_compliance_fixture_info":         1,
	}
	for metric, want := range wantSeries {
		if got := seriesByMetric[metric]; got != want {
			t.Fatalf("series count for %s = %d, want %d", metric, got, want)
		}
	}

	if got, want := len(req.Timeseries), 793; got != want {
		t.Fatalf("total series = %d, want %d", got, want)
	}

	bufferFinalValues := map[float64]int{}
	for _, ts := range req.Timeseries {
		if labelValue(ts.Labels, "__name__") == "demo_memory_usage_bytes" && labelValue(ts.Labels, "type") == "buffers" {
			bufferFinalValues[lastSample(t, ts).Value]++
		}
	}
	if len(bufferFinalValues) != 1 || bufferFinalValues[173_015_040] != 3 {
		t.Fatalf("expected final buffers exact tie across 3 instances, got %#v", bufferFinalValues)
	}

	cpu := findSeries(t, req, map[string]string{
		"__name__": "demo_cpu_usage_seconds_total",
		"instance": "demo.promlabs.com:10000",
		"mode":     "idle",
	})
	if resets := countResets(cpu.Samples); resets < 5 {
		t.Fatalf("expected repeated counter resets, got %d", resets)
	}

	intermittent := findSeries(t, req, map[string]string{
		"__name__": "demo_intermittent_metric",
		"instance": "demo.promlabs.com:10000",
	})
	last := lastSample(t, intermittent)
	if cutoff := end.Add(-7 * time.Minute); !time.UnixMilli(last.Timestamp).Before(cutoff) {
		t.Fatalf("intermittent metric last sample = %s, want before %s", time.UnixMilli(last.Timestamp), cutoff)
	}
}

func findSeries(t *testing.T, req *prompb.WriteRequest, labels map[string]string) prompb.TimeSeries {
	t.Helper()
	for _, ts := range req.Timeseries {
		matched := true
		for name, want := range labels {
			if labelValue(ts.Labels, name) != want {
				matched = false
				break
			}
		}
		if matched {
			return ts
		}
	}
	t.Fatalf("series not found: %#v", labels)
	return prompb.TimeSeries{}
}

func labelValue(labels []prompb.Label, name string) string {
	for _, label := range labels {
		if label.Name == name {
			return label.Value
		}
	}
	return ""
}

func lastSample(t *testing.T, ts prompb.TimeSeries) prompb.Sample {
	t.Helper()
	if len(ts.Samples) == 0 {
		t.Fatal("series has no samples")
	}
	return ts.Samples[len(ts.Samples)-1]
}

func countResets(samples []prompb.Sample) int {
	resets := 0
	prev := math.Inf(-1)
	for _, sample := range samples {
		if sample.Value < prev {
			resets++
		}
		prev = sample.Value
	}
	return resets
}
