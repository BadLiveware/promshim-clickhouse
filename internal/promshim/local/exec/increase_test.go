package exec

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestApplyIncreaseInstantUsesDeltaAcrossSamples(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_requests_total", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}, {Timestamp: 30, Value: 8}},
	}}}

	vector, err := ApplyIncreaseInstant(input)
	if err != nil {
		t.Fatalf("expected increase result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 7 {
		t.Fatalf("expected increase 7, got %#v", vector.Samples[0])
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ in increase output: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyIncreaseInstantAccountsForCounterReset(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_requests_total", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 5}, {Timestamp: 20, Value: 9}, {Timestamp: 30, Value: 2}, {Timestamp: 40, Value: 6}},
	}}}

	vector, err := ApplyIncreaseInstant(input)
	if err != nil {
		t.Fatalf("expected increase result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 10 {
		t.Fatalf("expected reset-aware increase 10, got %#v", vector.Samples[0])
	}
}

func TestApplyIncreaseInstantSkipsSeriesWithFewerThanTwoPoints(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_requests_total", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 5}},
	}}}

	vector, err := ApplyIncreaseInstant(input)
	if err != nil {
		t.Fatalf("expected increase result, got error: %v", err)
	}
	if len(vector.Samples) != 0 {
		t.Fatalf("expected no output samples, got %#v", vector.Samples)
	}
}

func TestApplyIncreaseInstantMarksNaNInputAsNaN(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_requests_total", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 5}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 7}},
	}}}

	vector, err := ApplyIncreaseInstant(input)
	if err != nil {
		t.Fatalf("expected increase result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN increase result, got %#v", vector.Samples[0])
	}
}
