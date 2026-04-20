package model

import (
	"testing"
	"time"
)

func TestNormalizeLabelSetSortsLabels(t *testing.T) {
	metric := map[string]string{
		"pod":      "pod-a",
		"__name__": "up",
		"job":      "clickhouse",
	}
	labels := NormalizeLabelSet(metric)
	if len(labels) != 3 {
		t.Fatalf("expected 3 normalized labels, got %#v", labels)
	}
	if labels[0].Name != "__name__" || labels[1].Name != "job" || labels[2].Name != "pod" {
		t.Fatalf("expected sorted label names, got %#v", labels)
	}
}

func TestBuildConstantRangePointsUsesStepAlignedTimestamps(t *testing.T) {
	points, err := BuildConstantRangePoints(time.Unix(0, 0).UTC(), time.Unix(90, 0).UTC(), 30*time.Second, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 4 {
		t.Fatalf("expected 4 points, got %#v", points)
	}
	if points[0].Timestamp != 0 || points[1].Timestamp != 30 || points[3].Timestamp != 90 {
		t.Fatalf("unexpected step-aligned timestamps: %#v", points)
	}
}

func TestSortAndValidateRangePointsRejectsDuplicateTimestamp(t *testing.T) {
	_, err := SortAndValidateRangePoints([]RangePoint{{Timestamp: 2, Value: 1}, {Timestamp: 2, Value: 2}})
	if err == nil {
		t.Fatal("expected duplicate timestamp error")
	}
}

func TestAppendRangePointsStrictRejectsOverlap(t *testing.T) {
	_, err := AppendRangePointsStrict(
		[]RangePoint{{Timestamp: 10, Value: 1}},
		[]RangePoint{{Timestamp: 10, Value: 2}},
	)
	if err == nil {
		t.Fatal("expected overlapping timestamp error")
	}
}

func TestAggregationMetricWithoutDropsMetricName(t *testing.T) {
	metric := map[string]string{
		"__name__": "up",
		"job":      "clickhouse",
		"instance": "127.0.0.1:9363",
		"pod":      "clickhouse-0",
		"service":  "clickhouse",
	}
	result := AggregationMetric(metric, []string{"instance", "pod"}, true)
	if _, ok := result["__name__"]; ok {
		t.Fatal("expected __name__ to be removed")
	}
	if _, ok := result["instance"]; ok {
		t.Fatal("expected instance to be removed")
	}
	if _, ok := result["pod"]; ok {
		t.Fatal("expected pod to be removed")
	}
	if result["job"] != "clickhouse" || result["service"] != "clickhouse" {
		t.Fatalf("unexpected remaining labels: %#v", result)
	}
}
