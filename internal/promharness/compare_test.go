package promharness

import (
	"math"
	"testing"
)

func TestCompareNormalizedResultsTreatsNaNAsEqual(t *testing.T) {
	left := normalizedResult{ResultType: "scalar", Scalar: &normalizedScalar{Timestamp: 1, Value: math.NaN()}}
	right := normalizedResult{ResultType: "scalar", Scalar: &normalizedScalar{Timestamp: 1, Value: math.NaN()}}
	if err := CompareNormalizedResults(left, right); err != nil {
		t.Fatalf("expected NaN scalars to compare equal, got %v", err)
	}
}

func TestCompareNormalizedResultsRejectsDifferentVectorValues(t *testing.T) {
	left := normalizedResult{ResultType: "vector", Vector: []normalizedVectorSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 1}}}
	right := normalizedResult{ResultType: "vector", Vector: []normalizedVectorSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 2}}}
	if err := CompareNormalizedResults(left, right); err == nil {
		t.Fatal("expected mismatch error")
	}
}
