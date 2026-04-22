package exec

import (
	"math"
	"testing"

	"ch-observability/internal/promshim/model"
)

func TestApplyDeltaRejectsNonMatrixInput(t *testing.T) {
	if _, err := ApplyDelta(model.VectorValue{}); err == nil {
		t.Fatal("expected error for vector input")
	}
}

func TestApplyIDeltaRejectsNonMatrixInput(t *testing.T) {
	if _, err := ApplyIDelta(model.VectorValue{}); err == nil {
		t.Fatal("expected error for vector input")
	}
}

func TestApplyDeltaDropsMetricName(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "requests_total", "job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 0, Value: 10},
			{Timestamp: 20, Value: 16},
		},
	}}}
	vector, err := ApplyDelta(matrix)
	if err != nil {
		t.Fatalf("expected delta output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected __name__ to be dropped, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyDeltaComputesFirstToLastDifference(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 100, Value: 5},
			{Timestamp: 160, Value: 11},
			{Timestamp: 220, Value: 8},
		},
	}}}
	vector, err := ApplyDelta(matrix)
	if err != nil {
		t.Fatalf("expected delta output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if expected := 3.0; vector.Samples[0].Value != expected {
		t.Fatalf("expected delta %v, got %v", expected, vector.Samples[0].Value)
	}
}

func TestApplyDeltaSkipsSeriesWithInsufficientSamples(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}}},
		{Metric: map[string]string{"job": "web"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}, {Timestamp: 20, Value: 4}}},
	}}
	vector, err := ApplyDelta(matrix)
	if err != nil {
		t.Fatalf("expected delta output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["job"] != "web" {
		t.Fatalf("expected short series to be dropped, got %#v", vector.Samples)
	}
}

func TestApplyDeltaPropagatesNaNEndpoints(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: math.NaN()}, {Timestamp: 10, Value: 2}, {Timestamp: 20, Value: 3}},
	}}}
	vector, err := ApplyDelta(matrix)
	if err != nil {
		t.Fatalf("expected delta output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN delta output, got %v", vector.Samples[0].Value)
	}
}

func TestApplyIDeltaUsesLastTwoSamples(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 5}, {Timestamp: 10, Value: 11}, {Timestamp: 20, Value: 8}},
	}}}
	vector, err := ApplyIDelta(matrix)
	if err != nil {
		t.Fatalf("expected idelta output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if expected := -3.0; vector.Samples[0].Value != expected {
		t.Fatalf("expected idelta %v, got %v", expected, vector.Samples[0].Value)
	}
}

func TestApplyIDeltaPropagatesNaNLastTwoSamples(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: math.NaN()}, {Timestamp: 20, Value: 3}},
	}}}
	vector, err := ApplyIDelta(matrix)
	if err != nil {
		t.Fatalf("expected idelta output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN idelta output, got %v", vector.Samples[0].Value)
	}
}
