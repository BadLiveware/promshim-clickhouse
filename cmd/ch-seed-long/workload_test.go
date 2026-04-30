package main

import (
	"testing"
	"time"
)

func TestBuildWorkloadSeriesEnvoyHeavyIsHistogramDominant(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	series, err := buildWorkloadSeries("envoy-heavy", 1000, []string{"demo-api", "demo-worker"}, start, end, end.Sub(start), 15*time.Second, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1000 {
		t.Fatalf("series = %d", len(series))
	}
	var hist, slow int
	for _, s := range series {
		if s.kind == "histogram_bucket" {
			hist++
		}
		if s.sampleEvery > 1 {
			slow++
		}
	}
	if hist < 750 {
		t.Fatalf("histogram series = %d, want histogram-dominant workload", hist)
	}
	if slow == 0 {
		t.Fatalf("expected mixed sample intervals")
	}
}

func TestWorkloadActiveWindowReducesSampleEstimate(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	legacy := buildSeriesDescriptors([]string{"demo-api", "demo-worker"}, 10)
	churn, err := buildWorkloadSeries("churn", len(legacy), []string{"demo-api", "demo-worker"}, start, end, end.Sub(start), 15*time.Second, 42)
	if err != nil {
		t.Fatal(err)
	}
	legacySamples := estimateGeneratedSamples(legacy, start, end, 15*time.Second)
	churnSamples := estimateGeneratedSamples(churn, start, end, 15*time.Second)
	if churnSamples >= legacySamples {
		t.Fatalf("churn samples = %d, legacy samples = %d; want churn/sparse workload to reduce sample volume", churnSamples, legacySamples)
	}
}
