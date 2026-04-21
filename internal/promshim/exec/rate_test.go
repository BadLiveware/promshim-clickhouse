package exec

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestApplyRateDropsMetricName(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "requests_total", "job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 0, Value: 10},
			{Timestamp: 10, Value: 12},
			{Timestamp: 20, Value: 16},
		},
	}}}
	vector, err := ApplyRate(matrix)
	if err != nil {
		t.Fatalf("expected rate output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected __name__ to be dropped, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyRateRejectsNonMatrixInput(t *testing.T) {
	if _, err := ApplyRate(model.VectorValue{}); err == nil {
		t.Fatal("expected error for vector input")
	}
}

func TestApplyRateComputesRate(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 100, Value: 5},
			{Timestamp: 160, Value: 11},
			{Timestamp: 220, Value: 8},
			{Timestamp: 280, Value: 15},
		},
	}}}
	vector, err := ApplyRate(matrix)
	if err != nil {
		t.Fatalf("expected rate output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	// increase: +6 +8 +7 = 21 over 180s => 0.116666...
	expected := 21.0 / 180.0
	if math.Abs(vector.Samples[0].Value-expected) > 1e-12 {
		t.Fatalf("expected rate %v, got %v", expected, vector.Samples[0].Value)
	}
}

func TestApplyRateIgnoresSeriesWithInsufficientSamples(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}}},
		{Metric: map[string]string{"job": "web"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}, {Timestamp: 20, Value: 4}}},
	}}
	vector, err := ApplyRate(matrix)
	if err != nil {
		t.Fatalf("expected rate output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["job"] != "web" {
		t.Fatalf("expected short series to be dropped, got %#v", vector.Samples)
	}
}

func TestApplyIRateUsesLastTwoSamples(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 0, Value: 2},
			{Timestamp: 10, Value: 6},
			{Timestamp: 20, Value: 4},
			{Timestamp: 30, Value: 9},
			{Timestamp: 40, Value: 7},
		},
	}}}
	vector, err := ApplyIRate(matrix)
	if err != nil {
		t.Fatalf("expected irate output, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	// last two points are reset from 9@30 to 7@40 => +7 over 10s
	expected := 7.0 / 10.0
	if math.Abs(vector.Samples[0].Value-expected) > 1e-12 {
		t.Fatalf("expected irate %v, got %v", expected, vector.Samples[0].Value)
	}
}

func TestApplyRateAndIRateSortByLabels(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{
			Metric: map[string]string{"job": "z", "__name__": "metric"},
			Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: 3}},
		},
		{
			Metric: map[string]string{"job": "a", "__name__": "metric"},
			Values: []model.RangePoint{{Timestamp: 0, Value: 5}, {Timestamp: 10, Value: 6}},
		},
	}}
	vector, err := ApplyRate(matrix)
	if err != nil {
		t.Fatalf("expected rate output, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["job"] != "a" || vector.Samples[1].Metric["job"] != "z" {
		t.Fatalf("expected deterministic sort by labels, got %#v", vector.Samples)
	}
}

func TestApplyRateAndIRatePropagateNaN(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{{Timestamp: 0, Value: 1}, {Timestamp: 10, Value: math.NaN()}, {Timestamp: 20, Value: 3}},
	}}}
	vector, err := ApplyRate(matrix)
	if err != nil {
		t.Fatalf("expected rate output, got error: %v", err)
	}
	vectorIRate, err := ApplyIRate(matrix)
	if err != nil {
		t.Fatalf("expected irate output, got error: %v", err)
	}
	if len(vector.Samples) != 1 || len(vectorIRate.Samples) != 1 {
		t.Fatalf("expected one sample from each transform, got %#v %#v", vector.Samples, vectorIRate.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) || !math.IsNaN(vectorIRate.Samples[0].Value) {
		t.Fatalf("expected NaN outputs, got rate=%v irate=%v", vector.Samples[0].Value, vectorIRate.Samples[0].Value)
	}
}
