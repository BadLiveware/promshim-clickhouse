package exec

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestApplyLastOverTimeInstantUsesLatestPointPerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 30, Value: 4}}},
	}}
	vector, err := ApplyLastOverTimeInstant(input)
	if err != nil {
		t.Fatalf("expected last_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 20 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 30 || vector.Samples[1].Value != 4 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplySumOverTimeInstantUsesSummedPointsPerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 30, Value: 4}}},
	}}
	vector, err := ApplyRangeFunctionInstant("sum_over_time", input)
	if err != nil {
		t.Fatalf("expected sum_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 20 || vector.Samples[0].Value != 3 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 30 || vector.Samples[1].Value != 7 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplyAvgOverTimeInstantUsesAveragePerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 30, Value: 5}}},
	}}
	vector, err := ApplyRangeFunctionInstant("avg_over_time", input)
	if err != nil {
		t.Fatalf("expected avg_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 20 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 30 || vector.Samples[1].Value != 4 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplyMaxOverTimeInstantUsesMaxPerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 7}, {Timestamp: 30, Value: 5}}},
	}}
	vector, err := ApplyRangeFunctionInstant("max_over_time", input)
	if err != nil {
		t.Fatalf("expected max_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 20 || vector.Samples[0].Value != 3 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 30 || vector.Samples[1].Value != 7 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplyRangeFunctionInstantDropsMetricName(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_queue_depth", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}},
	}}}
	vector, err := ApplyRangeFunctionInstant("max_over_time", input)
	if err != nil {
		t.Fatalf("expected max_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ in range-function output: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyMinOverTimeInstantUsesMinPerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 7}, {Timestamp: 30, Value: 5}}},
	}}
	vector, err := ApplyRangeFunctionInstant("min_over_time", input)
	if err != nil {
		t.Fatalf("expected min_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 20 || vector.Samples[0].Value != 1 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 30 || vector.Samples[1].Value != 5 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplyCountOverTimeInstantUsesCountsPerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 7}}},
	}}
	vector, err := ApplyRangeFunctionInstant("count_over_time", input)
	if err != nil {
		t.Fatalf("expected count_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 20 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 10 || vector.Samples[1].Value != 1 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplyStddevAndStdvarOverTimeInstantUsePopulationVariance(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}, {Timestamp: 20, Value: 2}}},
	}}
	stddevVector, err := ApplyRangeFunctionInstant("stddev_over_time", input)
	if err != nil {
		t.Fatalf("expected stddev_over_time instant result, got error: %v", err)
	}
	stdvarVector, err := ApplyRangeFunctionInstant("stdvar_over_time", input)
	if err != nil {
		t.Fatalf("expected stdvar_over_time instant result, got error: %v", err)
	}
	if len(stddevVector.Samples) != 2 || stddevVector.Samples[0].Value != 1 || stddevVector.Samples[1].Value != 0 {
		t.Fatalf("unexpected stddev_over_time samples: %#v", stddevVector.Samples)
	}
	if len(stdvarVector.Samples) != 2 || stdvarVector.Samples[0].Value != 1 || stdvarVector.Samples[1].Value != 0 {
		t.Fatalf("unexpected stdvar_over_time samples: %#v", stdvarVector.Samples)
	}
}

func TestApplyPresentOverTimeInstantReturnsOnePerNonEmptySeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 30, Value: 7}}},
	}}
	vector, err := ApplyRangeFunctionInstant("present_over_time", input)
	if err != nil {
		t.Fatalf("expected present_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 1 || vector.Samples[1].Value != 1 {
		t.Fatalf("expected present_over_time outputs of 1, got %#v", vector.Samples)
	}
}

func TestApplyQuantileOverTimeInstantComputesMedianPerSeries(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: 1}, {Timestamp: 30, Value: 2}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 7}, {Timestamp: 30, Value: 5}}},
	}}
	vector, err := ApplyQuantileOverTime(0.5, input)
	if err != nil {
		t.Fatalf("expected quantile_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Timestamp != 30 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected first sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Timestamp != 30 || vector.Samples[1].Value != 6 {
		t.Fatalf("unexpected second sample: %#v", vector.Samples[1])
	}
}

func TestApplyQuantileOverTimeDropsMetricName(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_queue_depth", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: 1}, {Timestamp: 30, Value: 2}},
	}}}
	vector, err := ApplyQuantileOverTime(0.5, input)
	if err != nil {
		t.Fatalf("expected quantile_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ in quantile_over_time output: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyQuantileOverTimeReturnsInfForOutOfRangeQuantile(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}}},
	}}
	vector, err := ApplyQuantileOverTime(2, input)
	if err != nil {
		t.Fatalf("expected quantile_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != math.Inf(1) {
		t.Fatalf("expected +inf result for quantile >1, got %#v", vector.Samples[0].Value)
	}
}

func TestApplyQuantileOverTimeReturnsNegativeInfForNegativeQuantile(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: 1}}},
	}}
	vector, err := ApplyQuantileOverTime(-1, input)
	if err != nil {
		t.Fatalf("expected quantile_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != math.Inf(-1) {
		t.Fatalf("expected -inf result for quantile <0, got %#v", vector.Samples[0].Value)
	}
}

func TestApplyQuantileOverTimeReturnsNaNForNaNQuantile(t *testing.T) {
	input := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: 1}}},
	}}
	vector, err := ApplyQuantileOverTime(math.NaN(), input)
	if err != nil {
		t.Fatalf("expected quantile_over_time instant result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN result for quantile NaN, got %#v", vector.Samples[0].Value)
	}
}

func TestApplyLastOverTimeInstantRejectsNonMatrixInput(t *testing.T) {
	_, err := ApplyLastOverTimeInstant(model.VectorValue{})
	if err == nil {
		t.Fatal("expected unsupported error for non-matrix input")
	}
}

func TestApplyQuantileOverTimeRejectsNonMatrixInput(t *testing.T) {
	_, err := ApplyQuantileOverTime(0.5, model.VectorValue{})
	if err == nil {
		t.Fatal("expected unsupported error for non-matrix input")
	}
}

func TestApplyRangeFunctionInstantRejectsUnknownFunction(t *testing.T) {
	_, err := ApplyRangeFunctionInstant("definitely_not_implemented", model.MatrixValue{})
	if err == nil {
		t.Fatal("expected unsupported error for unknown range function")
	}
}
