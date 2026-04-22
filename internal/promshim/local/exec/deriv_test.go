package exec

import (
	"math"
	"testing"

	"ch-observability/internal/promshim/model"
)

func TestApplyDerivRejectsNonMatrixInput(t *testing.T) {
	if _, err := ApplyDeriv(model.VectorValue{}); err == nil {
		t.Fatal("expected error for vector input")
	}
}

func TestApplyDerivDropsMetricName(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "requests_total", "job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: 21}, {Timestamp: 20, Value: 41}},
	}}}
	vector, err := ApplyDeriv(matrix)
	if err != nil {
		t.Fatalf("expected deriv output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected __name__ to be dropped, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyDerivComputesSlope(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: 21}, {Timestamp: 20, Value: 41}},
	}}}
	vector, err := ApplyDeriv(matrix)
	if err != nil {
		t.Fatalf("expected deriv output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if math.Abs(vector.Samples[0].Value-2.0) > 1e-12 {
		t.Fatalf("expected deriv slope 2, got %v", vector.Samples[0].Value)
	}
}

func TestApplyDerivSkipsSeriesWithInsufficientSamples(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 5}},
	}}}
	vector, err := ApplyDeriv(matrix)
	if err != nil {
		t.Fatalf("expected deriv output, got error: %v", err)
	}
	if len(vector.Samples) != 0 {
		t.Fatalf("expected no output samples, got %#v", vector.Samples)
	}
}

func TestApplyDerivPropagatesNaN(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: math.NaN()}, {Timestamp: 20, Value: 3}},
	}}}
	vector, err := ApplyDeriv(matrix)
	if err != nil {
		t.Fatalf("expected deriv output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN deriv output, got %v", vector.Samples[0].Value)
	}
}

func TestApplyDerivReturnsNaNForZeroTimestampSpan(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 10, Value: 2}, {Timestamp: 10, Value: 3}},
	}}}
	vector, err := ApplyDeriv(matrix)
	if err != nil {
		t.Fatalf("expected deriv output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN deriv output, got %v", vector.Samples[0].Value)
	}
}
