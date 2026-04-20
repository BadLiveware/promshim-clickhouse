package promshim

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
	labels := normalizeLabelSet(metric)
	if len(labels) != 3 {
		t.Fatalf("expected 3 normalized labels, got %#v", labels)
	}
	if labels[0].Name != "__name__" || labels[1].Name != "job" || labels[2].Name != "pod" {
		t.Fatalf("expected sorted label names, got %#v", labels)
	}
}

func TestBuildConstantRangePointsUsesStepAlignedTimestamps(t *testing.T) {
	points, err := buildConstantRangePoints(5, evalParams{
		Mode:  evalModeRange,
		Start: time.Unix(0, 0).UTC(),
		End:   time.Unix(90, 0).UTC(),
		Step:  30 * time.Second,
	})
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
	_, err := sortAndValidateRangePoints([]rangePoint{{Timestamp: 2, Value: 1}, {Timestamp: 2, Value: 2}})
	if err == nil {
		t.Fatal("expected duplicate timestamp error")
	}
}

func TestAppendRangePointsStrictRejectsOverlap(t *testing.T) {
	_, err := appendRangePointsStrict([]rangePoint{{Timestamp: 10, Value: 1}}, []rangePoint{{Timestamp: 10, Value: 2}})
	if err == nil {
		t.Fatal("expected overlapping timestamp error")
	}
}
