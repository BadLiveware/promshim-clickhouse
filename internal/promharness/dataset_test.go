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

func TestGenerateDatasetResetsCounterAndAddsGapsForResetsGapsVariant(t *testing.T) {
	dataset := GenerateDataset(SeedConfig{Seed: 12345, Step: time.Minute, Points: 10, BaseTime: time.Unix(0, 0).UTC(), DatasetVariant: "resets_gaps"})
	counterSeries := findSeries(t, dataset.Request.Timeseries, "harness_requests_total", map[string]string{"job": "api", "instance": "a"})
	if len(counterSeries.Samples) >= 10 {
		t.Fatalf("expected gap-reduced counter series sample count, got %#v", counterSeries.Samples)
	}
	foundReset := false
	for i := 1; i < len(counterSeries.Samples); i++ {
		if counterSeries.Samples[i].Value < counterSeries.Samples[i-1].Value {
			foundReset = true
			break
		}
	}
	if !foundReset {
		t.Fatalf("expected reset in counter series, got %#v", counterSeries.Samples)
	}
}

func TestGenerateDatasetChurnStaleDropsSeriesAfterMidpoint(t *testing.T) {
	dataset := GenerateDataset(SeedConfig{Seed: 12345, Step: time.Minute, Points: 10, BaseTime: time.Unix(0, 0).UTC(), DatasetVariant: "churn_stale"})
	upSeries := findSeries(t, dataset.Request.Timeseries, "harness_up", map[string]string{"job": "worker", "instance": "b"})
	if len(upSeries.Samples) != 5 {
		t.Fatalf("expected worker/b up series to stop at midpoint, got %#v", upSeries.Samples)
	}
	if upSeries.Samples[len(upSeries.Samples)-1].Timestamp != 240000 {
		t.Fatalf("expected worker/b up series to end before midpoint gap, got %#v", upSeries.Samples)
	}
	gaugeSeries := findSeries(t, dataset.Request.Timeseries, "harness_disappearing_gauge", map[string]string{"job": "api", "instance": "a"})
	if len(gaugeSeries.Samples) != 2 {
		t.Fatalf("expected shorter disappearing gauge series for churn_stale, got %#v", gaugeSeries.Samples)
	}
}

func TestGenerateDatasetHistogramBurstShiftsHistogramAndQueueDepth(t *testing.T) {
	dataset := GenerateDataset(SeedConfig{Seed: 12345, Step: time.Minute, Points: 10, BaseTime: time.Unix(0, 0).UTC(), DatasetVariant: "histogram_burst"})
	queueSeries := findSeries(t, dataset.Request.Timeseries, "harness_queue_depth", map[string]string{"job": "api", "instance": "a"})
	if queueSeries.Samples[5].Value < 70 {
		t.Fatalf("expected queue depth burst after midpoint, got %#v", queueSeries.Samples)
	}
	bucketSeries := findSeries(t, dataset.Request.Timeseries, "harness_request_duration_seconds_bucket", map[string]string{"job": "api", "instance": "a", "le": "+Inf"})
	preBurstDelta := bucketSeries.Samples[4].Value - bucketSeries.Samples[3].Value
	burstDelta := bucketSeries.Samples[5].Value - bucketSeries.Samples[4].Value
	if burstDelta <= preBurstDelta {
		t.Fatalf("expected larger post-midpoint bucket growth for histogram burst, got pre=%v post=%v series=%#v", preBurstDelta, burstDelta, bucketSeries.Samples)
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
