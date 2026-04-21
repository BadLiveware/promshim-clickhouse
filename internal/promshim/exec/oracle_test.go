package exec

import (
	"testing"

	"ch-observability/internal/promshim/model"
)

func TestLocalRangeOperatorInventoryIncludesCurrentRangeAndCounterSurface(t *testing.T) {
	inventory := LocalRangeOperatorInventory()
	if len(inventory) == 0 {
		t.Fatal("expected non-empty local range operator inventory")
	}
	want := map[string]string{
		"last_over_time":     "internal/promshim/exec/rangefunc.go",
		"sum_over_time":      "internal/promshim/exec/rangefunc.go",
		"avg_over_time":      "internal/promshim/exec/rangefunc.go",
		"max_over_time":      "internal/promshim/exec/rangefunc.go",
		"min_over_time":      "internal/promshim/exec/rangefunc.go",
		"count_over_time":    "internal/promshim/exec/rangefunc.go",
		"quantile_over_time": "internal/promshim/exec/rangefunc.go",
		"rate":               "internal/promshim/exec/rate.go",
		"irate":              "internal/promshim/exec/rate.go",
		"increase":           "internal/promshim/exec/increase.go",
		"delta":              "internal/promshim/exec/delta.go",
		"idelta":             "internal/promshim/exec/delta.go",
		"changes":            "internal/promshim/exec/changes.go",
		"deriv":              "internal/promshim/exec/deriv.go",
	}
	seen := map[string]LocalRangeOperatorDescriptor{}
	for _, item := range inventory {
		seen[item.Name] = item
	}
	for name, file := range want {
		item, ok := seen[name]
		if !ok {
			t.Fatalf("expected inventory item for %q, got %#v", name, inventory)
		}
		if item.File != file {
			t.Fatalf("expected %q to come from %q, got %#v", name, file, item)
		}
		if item.PrometheusRef == "" || len(item.SemanticRules) == 0 {
			t.Fatalf("expected semantic metadata for %q, got %#v", name, item)
		}
	}
}

func TestLocalRangeOracleForTestDispatchesKnownOperators(t *testing.T) {
	oracle, ok := LocalRangeOracleForTest("sum_over_time")
	if !ok {
		t.Fatal("expected oracle for sum_over_time")
	}
	value, err := oracle(model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}}}}})
	if err != nil {
		t.Fatalf("expected oracle to evaluate sum_over_time, got error: %v", err)
	}
	if len(value.Samples) != 1 || value.Samples[0].Value != 3 {
		t.Fatalf("unexpected oracle output: %#v", value)
	}
	if _, ok := value.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected oracle output to drop metric name, got %#v", value.Samples[0].Metric)
	}
}

func TestApplyLocalQuantileOverTimeOracleForTest(t *testing.T) {
	value, err := ApplyLocalQuantileOverTimeOracleForTest(0.5, model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 3}, {Timestamp: 3, Value: 5}}}}})
	if err != nil {
		t.Fatalf("expected quantile oracle to evaluate, got error: %v", err)
	}
	if len(value.Samples) != 1 {
		t.Fatalf("expected single sample, got %#v", value)
	}
	if _, ok := value.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected quantile oracle to drop metric name, got %#v", value.Samples[0].Metric)
	}
}
