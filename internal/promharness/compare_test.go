package promharness

import (
	"math"
	"testing"
)

func TestCompareQueryOutcomeSupportsExpectedErrorMatching(t *testing.T) {
	status, err := CompareQueryOutcome(QuerySpec{
		Name:                  "matrix-root-range-query",
		ExpectedStatus:        "error",
		ExpectedErrorType:     "bad_data",
		ExpectedErrorContains: "invalid expression type",
	},
		"shim",
		queryResult{Status: "error", ErrorType: "bad_data", Error: "query execution failed: invalid expression type in query_range"},
		queryResult{Status: "error", ErrorType: "bad_data", Error: "query execution failed: invalid expression type"},
	)
	if err != nil {
		t.Fatalf("expected expected-error outcome to pass, got err: %v", err)
	}
	if status != "ok" {
		t.Fatalf("expected query outcome status ok, got %q", status)
	}
}

func TestCompareQueryOutcomeRejectsUnexpectedErrorTypeForExpectedErrorQuery(t *testing.T) {
	_, err := CompareQueryOutcome(QuerySpec{
		Name:              "unsupported-error",
		ExpectedStatus:    "error",
		ExpectedErrorType: "unsupported",
	},
		"shim",
		queryResult{Status: "error", ErrorType: "bad_data", Error: "bad_data error"},
		queryResult{Status: "error", ErrorType: "unsupported", Error: "unsupported function"},
	)
	if err == nil {
		t.Fatal("expected error-type mismatch to fail")
	}
}

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
