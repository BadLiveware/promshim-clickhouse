package exec

import (
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestApplyDoubleExponentialSmoothingRejectsNonMatrixInput(t *testing.T) {
	_, err := ApplyDoubleExponentialSmoothing(0.5, 0.3, model.VectorValue{})
	if err == nil {
		t.Fatal("expected unsupported error for non-matrix input")
	}
}

func TestApplyDoubleExponentialSmoothingRejectsInvalidFactors(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}}}}}
	_, err := ApplyDoubleExponentialSmoothing(0, 0.3, matrix)
	if err == nil || !strings.Contains(err.Error(), "smoothing factor") {
		t.Fatalf("expected smoothing factor error, got %v", err)
	}
	_, err = ApplyDoubleExponentialSmoothing(0.5, 1, matrix)
	if err == nil || !strings.Contains(err.Error(), "trend factor") {
		t.Fatalf("expected trend factor error, got %v", err)
	}
}

func TestApplyDoubleExponentialSmoothingMatchesTwoPointEdge(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 1, Value: 3}, {Timestamp: 2, Value: 7}}}}}
	vector, err := ApplyDoubleExponentialSmoothing(0.5, 0.3, matrix)
	if err != nil {
		t.Fatalf("expected smoothing result, got error: %v", err)
	}
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 7 {
		t.Fatalf("expected two-point edge to return second value, got %#v", vector.Samples)
	}
}

func TestApplyDoubleExponentialSmoothingComputesSmoothedValue(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"__name__": "up", "job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 3}, {Timestamp: 3, Value: 2}, {Timestamp: 4, Value: 5}}}}}
	vector, err := ApplyDoubleExponentialSmoothing(0.5, 0.5, matrix)
	if err != nil {
		t.Fatalf("expected smoothing result, got error: %v", err)
	}
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 4.875 {
		t.Fatalf("expected smoothed value 4.875, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect metric name in smoothing output: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyDoubleExponentialSmoothingSkipsShortSeries(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: 1, Value: 3}}}}}
	vector, err := ApplyDoubleExponentialSmoothing(0.5, 0.3, matrix)
	if err != nil {
		t.Fatalf("expected smoothing result, got error: %v", err)
	}
	if len(vector.Samples) != 0 {
		t.Fatalf("expected short series to be dropped, got %#v", vector.Samples)
	}
}
