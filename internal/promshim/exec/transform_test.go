package exec

import (
	"testing"

	"ch-observability/internal/promshim/model"
)

func TestApplyVectorFromScalar(t *testing.T) {
	scalar := model.ScalarValue{Timestamp: 1234.5, Value: 7}
	vector, err := ApplyVector(scalar)
	if err != nil {
		t.Fatalf("expected vector from scalar, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Timestamp != scalar.Timestamp {
		t.Fatalf("expected timestamp %v, got %v", scalar.Timestamp, vector.Samples[0].Timestamp)
	}
	if vector.Samples[0].Value != scalar.Value {
		t.Fatalf("expected value %v, got %v", scalar.Value, vector.Samples[0].Value)
	}
	if len(vector.Samples[0].Metric) != 0 {
		t.Fatalf("expected empty metric for vector(), got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyRoundDropsMetricName(t *testing.T) {
	vector := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "requests_total", "job": "api"},
		Timestamp: 10,
		Value:     1.49,
	}}}
	rounded, err := ApplyRound(vector, nil)
	if err != nil {
		t.Fatalf("expected round output, got error: %v", err)
	}
	if len(rounded.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", rounded.Samples)
	}
	if _, ok := rounded.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected __name__ to be dropped, got %#v", rounded.Samples[0].Metric)
	}
	if rounded.Samples[0].Value != 1 {
		t.Fatalf("expected rounded value 1, got %v", rounded.Samples[0].Value)
	}
}

func TestApplyRoundSupportsToNearest(t *testing.T) {
	vector := model.VectorValue{Samples: []model.InstantSample{{Timestamp: 10, Value: 1.23}, {Timestamp: 11, Value: 1.25}}}
	rounded, err := ApplyRound(vector, floatPtr(0.1))
	if err != nil {
		t.Fatalf("expected round output, got error: %v", err)
	}
	if rounded.Samples[0].Value < 1.19 || rounded.Samples[0].Value > 1.21 || rounded.Samples[1].Value < 1.29 || rounded.Samples[1].Value > 1.31 {
		t.Fatalf("unexpected rounded values: %#v", rounded.Samples)
	}
}

func TestApplyRoundRejectsZeroMultiplier(t *testing.T) {
	vector := model.VectorValue{Samples: []model.InstantSample{{Timestamp: 10, Value: 1.2}}}
	if _, err := ApplyRound(vector, floatPtr(0)); err == nil {
		t.Fatal("expected error for zero multiplier")
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
