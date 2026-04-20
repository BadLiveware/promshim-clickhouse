package exec

import (
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestApplyAbsentReturnsOneSampleWhenInputVectorIsEmpty(t *testing.T) {
	value, err := ApplyAbsent(model.VectorValue{}, map[string]string{"job": "api"}, 42)
	if err != nil {
		t.Fatalf("expected absent success, got error: %v", err)
	}
	if len(value.Samples) != 1 {
		t.Fatalf("expected one absent sample, got %#v", value.Samples)
	}
	if value.Samples[0].Metric["job"] != "api" || value.Samples[0].Value != 1 || value.Samples[0].Timestamp != 42 {
		t.Fatalf("unexpected absent sample: %#v", value.Samples[0])
	}
}

func TestApplyAbsentReturnsEmptyWhenInputVectorHasSamples(t *testing.T) {
	value, err := ApplyAbsent(model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 10, Value: 5}}}, map[string]string{"job": "api"}, 42)
	if err != nil {
		t.Fatalf("expected absent success, got error: %v", err)
	}
	if len(value.Samples) != 0 {
		t.Fatalf("expected empty absent result, got %#v", value.Samples)
	}
}

func TestApplyAbsentRejectsNonVectorInput(t *testing.T) {
	_, err := ApplyAbsent(model.MatrixValue{}, nil, 42)
	if err == nil {
		t.Fatal("expected absent type error")
	}
	execErr, ok := err.(*Error)
	if !ok || execErr.Kind != ErrorKindUnsupported {
		t.Fatalf("expected unsupported absent type error, got %T (%v)", err, err)
	}
}

func TestApplyAbsentOverTimeReturnsOneSampleWhenInputMatrixIsEmpty(t *testing.T) {
	value, err := ApplyAbsentOverTime(model.MatrixValue{}, map[string]string{"job": "api"}, 42)
	if err != nil {
		t.Fatalf("expected absent_over_time success, got error: %v", err)
	}
	if len(value.Samples) != 1 {
		t.Fatalf("expected one absent_over_time sample, got %#v", value.Samples)
	}
	if value.Samples[0].Metric["job"] != "api" || value.Samples[0].Value != 1 || value.Samples[0].Timestamp != 42 {
		t.Fatalf("unexpected absent_over_time sample: %#v", value.Samples[0])
	}
}

func TestApplyAbsentOverTimeReturnsEmptyWhenInputMatrixHasSamples(t *testing.T) {
	value, err := ApplyAbsentOverTime(model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 5}}}}}, map[string]string{"job": "api"}, 42)
	if err != nil {
		t.Fatalf("expected absent_over_time success, got error: %v", err)
	}
	if len(value.Samples) != 0 {
		t.Fatalf("expected empty absent_over_time result, got %#v", value.Samples)
	}
}

func TestApplyAbsentOverTimeRejectsNonMatrixInput(t *testing.T) {
	_, err := ApplyAbsentOverTime(model.VectorValue{}, nil, 42)
	if err == nil {
		t.Fatal("expected absent_over_time type error")
	}
	execErr, ok := err.(*Error)
	if !ok || execErr.Kind != ErrorKindUnsupported {
		t.Fatalf("expected unsupported absent_over_time type error, got %T (%v)", err, err)
	}
}
