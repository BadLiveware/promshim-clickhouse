package exec

import (
	"math"
	"testing"

	"ch-observability/internal/promshim/model"
)

func TestApplyChangesRejectsNonMatrixInput(t *testing.T) {
	if _, err := ApplyChanges(model.VectorValue{}); err == nil {
		t.Fatal("expected error for vector input")
	}
}

func TestApplyChangesDropsMetricName(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "requests_total", "job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 10}, {Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 16}},
	}}}
	vector, err := ApplyChanges(matrix)
	if err != nil {
		t.Fatalf("expected changes output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected __name__ to be dropped, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyChangesCountsTransitions(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}, {Timestamp: 30, Value: 3}, {Timestamp: 40, Value: 3}},
	}}}
	vector, err := ApplyChanges(matrix)
	if err != nil {
		t.Fatalf("expected changes output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if expected := 2.0; vector.Samples[0].Value != expected {
		t.Fatalf("expected changes %v, got %v", expected, vector.Samples[0].Value)
	}
}

func TestApplyChangesSingleSampleReturnsZero(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 5}},
	}}}
	vector, err := ApplyChanges(matrix)
	if err != nil {
		t.Fatalf("expected changes output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 0 {
		t.Fatalf("expected zero changes, got %v", vector.Samples[0].Value)
	}
}

func TestApplyChangesPropagatesNaN(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: math.NaN()}, {Timestamp: 20, Value: 3}},
	}}}
	vector, err := ApplyChanges(matrix)
	if err != nil {
		t.Fatalf("expected changes output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN changes output, got %v", vector.Samples[0].Value)
	}
}
