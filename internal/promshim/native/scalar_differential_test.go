package native

import (
	"math"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/exec"
	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestScalarNativeSemanticsMatchLocalOracle(t *testing.T) {
	params := exec.EvalParams{Mode: exec.EvalModeInstant, EvaluationTime: time.Unix(42, 0).UTC()}
	testCases := []struct {
		name  string
		input model.VectorValue
	}{
		{name: "single_series", input: model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 10, Value: 7}}}},
		{name: "multi_series", input: model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 10, Value: 7}, {Metric: map[string]string{"job": "worker"}, Timestamp: 10, Value: 8}}}},
		{name: "empty_vector", input: model.VectorValue{}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			localValue, err := exec.ApplyScalar(tc.input, params)
			if err != nil {
				t.Fatalf("local scalar oracle returned error: %v", err)
			}
			nativeValue := applyNativeScalarForTest(tc.input, params)
			assertScalarEqual(t, tc.name, localValue, nativeValue)
		})
	}
}

func applyNativeScalarForTest(input model.VectorValue, params exec.EvalParams) model.ScalarValue {
	timestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
	if len(input.Samples) != 1 {
		return model.ScalarValue{Timestamp: timestamp, Value: math.NaN()}
	}
	return model.ScalarValue{Timestamp: timestamp, Value: input.Samples[0].Value}
}

func assertScalarEqual(t *testing.T, name string, want, got model.ScalarValue) {
	t.Helper()
	if want.Timestamp != got.Timestamp {
		t.Fatalf("%s: timestamp mismatch want=%v got=%v", name, want.Timestamp, got.Timestamp)
	}
	if math.IsNaN(want.Value) {
		if !math.IsNaN(got.Value) {
			t.Fatalf("%s: expected NaN scalar value, got %v", name, got.Value)
		}
		return
	}
	if want.Value != got.Value {
		t.Fatalf("%s: value mismatch want=%v got=%v", name, want.Value, got.Value)
	}
}
