package promharness

import (
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

func TestGenerateDatasetAddsSparseGaugeSeries(t *testing.T) {
	dataset := GenerateDataset(SeedConfig{Seed: 12345, Step: time.Minute, Points: 10, BaseTime: time.Unix(0, 0).UTC()})
	series := findSeries(t, dataset.Request.Timeseries, "harness_sparse_gauge", map[string]string{"job": "api", "instance": "a"})
	if len(series.Samples) != 4 {
		t.Fatalf("expected four sparse gauge samples, got %#v", series.Samples)
	}
	expected := []int64{0, 180000, 360000, 540000}
	for i, sample := range series.Samples {
		if sample.Timestamp != expected[i] {
			t.Fatalf("expected sparse sample %d at %d, got %d", i, expected[i], sample.Timestamp)
		}
	}
}

func TestGenerateDatasetAddsDisappearingGaugeSeries(t *testing.T) {
	dataset := GenerateDataset(SeedConfig{Seed: 12345, Step: time.Minute, Points: 10, BaseTime: time.Unix(0, 0).UTC()})
	series := findSeries(t, dataset.Request.Timeseries, "harness_disappearing_gauge", map[string]string{"job": "api", "instance": "a"})
	if len(series.Samples) != 3 {
		t.Fatalf("expected three disappearing gauge samples, got %#v", series.Samples)
	}
	expected := []int64{0, 60000, 120000}
	for i, sample := range series.Samples {
		if sample.Timestamp != expected[i] {
			t.Fatalf("expected disappearing sample %d at %d, got %d", i, expected[i], sample.Timestamp)
		}
	}
}

func findSeries(t *testing.T, series []prompb.TimeSeries, metric string, labels map[string]string) prompb.TimeSeries {
	t.Helper()
	for _, ts := range series {
		actual := labelsFromPromSeries(ts)
		if actual["__name__"] != metric {
			continue
		}
		matches := true
		for key, value := range labels {
			if actual[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return ts
		}
	}
	t.Fatalf("series %q with labels %#v not found", metric, labels)
	return prompb.TimeSeries{}
}

func labelsFromPromSeries(series prompb.TimeSeries) map[string]string {
	result := make(map[string]string, len(series.Labels))
	for _, label := range series.Labels {
		result[label.Name] = label.Value
	}
	return result
}
