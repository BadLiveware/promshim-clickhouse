package exec

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestApplySortFunctionSortsByValueWithNaNLast(t *testing.T) {
	input := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "c"}, Timestamp: 1, Value: math.NaN()},
		{Metric: map[string]string{"job": "b"}, Timestamp: 1, Value: 2},
		{Metric: map[string]string{"job": "a"}, Timestamp: 1, Value: 1},
	}}

	asc, err := ApplySortFunction("sort", input, nil)
	if err != nil {
		t.Fatalf("expected sort output, got error: %v", err)
	}
	if asc.Samples[0].Metric["job"] != "a" || asc.Samples[1].Metric["job"] != "b" || !math.IsNaN(asc.Samples[2].Value) {
		t.Fatalf("unexpected ascending sort output: %#v", asc.Samples)
	}

	desc, err := ApplySortFunction("sort_desc", input, nil)
	if err != nil {
		t.Fatalf("expected sort_desc output, got error: %v", err)
	}
	if desc.Samples[0].Metric["job"] != "b" || desc.Samples[1].Metric["job"] != "a" || !math.IsNaN(desc.Samples[2].Value) {
		t.Fatalf("unexpected descending sort output: %#v", desc.Samples)
	}
}

func TestApplySortFunctionSortsByLabels(t *testing.T) {
	input := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api-10", "instance": "b"}, Timestamp: 1, Value: 3},
		{Metric: map[string]string{"job": "api-2", "instance": "a"}, Timestamp: 1, Value: 2},
		{Metric: map[string]string{"job": "api-2", "instance": "c"}, Timestamp: 1, Value: 1},
	}}

	asc, err := ApplySortFunction("sort_by_label", input, []string{"job", "instance"})
	if err != nil {
		t.Fatalf("expected sort_by_label output, got error: %v", err)
	}
	if asc.Samples[0].Metric["job"] != "api-10" || asc.Samples[1].Metric["instance"] != "a" || asc.Samples[2].Metric["instance"] != "c" {
		t.Fatalf("unexpected ascending label sort output: %#v", asc.Samples)
	}

	desc, err := ApplySortFunction("sort_by_label_desc", input, []string{"job", "instance"})
	if err != nil {
		t.Fatalf("expected sort_by_label_desc output, got error: %v", err)
	}
	if desc.Samples[0].Metric["instance"] != "c" || desc.Samples[1].Metric["instance"] != "a" || desc.Samples[2].Metric["job"] != "api-10" {
		t.Fatalf("unexpected descending label sort output: %#v", desc.Samples)
	}
}
