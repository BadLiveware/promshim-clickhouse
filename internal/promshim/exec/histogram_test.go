package exec

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func TestApplyHistogramQuantileRuntimeValueInstantClassicBuckets(t *testing.T) {
	value, err := ApplyHistogramQuantileRuntimeValue(0.5, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.1"}, Timestamp: 42, Value: 2},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.2"}, Timestamp: 42, Value: 6},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.5"}, Timestamp: 42, Value: 9},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "+Inf"}, Timestamp: 42, Value: 10},
	}})
	if err != nil {
		t.Fatal(err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected model.VectorValue, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one quantile sample, got %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["le"]; ok {
		t.Fatalf("did not expect le label in quantile output: %#v", vector.Samples[0].Metric)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ in quantile output: %#v", vector.Samples[0].Metric)
	}
	if vector.Samples[0].Metric["job"] != "api" {
		t.Fatalf("expected job label to be preserved, got %#v", vector.Samples[0].Metric)
	}
	assertFloatClose(t, vector.Samples[0].Value, 0.175)
}

func TestApplyHistogramQuantileRuntimeValueRangeClassicBuckets(t *testing.T) {
	value, err := ApplyHistogramQuantileRuntimeValue(0.5, model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.1"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}, {Timestamp: 20, Value: 4}}},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.2"}, Values: []model.RangePoint{{Timestamp: 10, Value: 6}, {Timestamp: 20, Value: 8}}},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.5"}, Values: []model.RangePoint{{Timestamp: 10, Value: 9}, {Timestamp: 20, Value: 9}}},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "+Inf"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 10}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected model.MatrixValue, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one quantile series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected two range points, got %#v", matrix.Series[0].Values)
	}
	assertFloatClose(t, matrix.Series[0].Values[0].Value, 0.175)
	assertFloatClose(t, matrix.Series[0].Values[1].Value, 0.125)
}

func TestApplyHistogramQuantileRuntimeValueReturnsNaNWhenInfBucketMissing(t *testing.T) {
	value, err := ApplyHistogramQuantileRuntimeValue(0.9, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "le": "0.1"}, Timestamp: 42, Value: 2},
		{Metric: map[string]string{"job": "api", "le": "0.2"}, Timestamp: 42, Value: 6},
	}})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN when +Inf bucket is missing, got %#v", vector.Samples[0])
	}
}

func TestApplyHistogramProjectionRuntimeValuesOnClassicBuckets(t *testing.T) {
	input := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.1"}, Timestamp: 42, Value: 2},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.2"}, Timestamp: 42, Value: 6},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.5"}, Timestamp: 42, Value: 9},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "+Inf"}, Timestamp: 42, Value: 10},
	}}

	countValue, err := ApplyHistogramCountRuntimeValue(input)
	if err != nil {
		t.Fatal(err)
	}
	fractionValue, err := ApplyHistogramFractionRuntimeValue(0, 0.2, input)
	if err != nil {
		t.Fatal(err)
	}
	sumValue, err := ApplyHistogramSumRuntimeValue(input)
	if err != nil {
		t.Fatal(err)
	}
	avgValue, err := ApplyHistogramAvgRuntimeValue(input)
	if err != nil {
		t.Fatal(err)
	}

	countVector := countValue.(model.VectorValue)
	fractionVector := fractionValue.(model.VectorValue)
	sumVector := sumValue.(model.VectorValue)
	avgVector := avgValue.(model.VectorValue)
	if len(countVector.Samples) != 1 || len(fractionVector.Samples) != 1 || len(sumVector.Samples) != 1 || len(avgVector.Samples) != 1 {
		t.Fatalf("expected one output sample from histogram projections, got count=%#v fraction=%#v sum=%#v avg=%#v", countVector.Samples, fractionVector.Samples, sumVector.Samples, avgVector.Samples)
	}
	assertFloatClose(t, countVector.Samples[0].Value, 10)
	assertFloatClose(t, fractionVector.Samples[0].Value, 0.6)
	assertFloatClose(t, sumVector.Samples[0].Value, 2.25)
	assertFloatClose(t, avgVector.Samples[0].Value, 0.225)
}

func TestApplyHistogramProjectionRuntimeValuesIgnoreNonHistogramInputs(t *testing.T) {
	input := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"__name__": "up", "job": "api"}, Timestamp: 42, Value: 1}}}

	countValue, err := ApplyHistogramCountRuntimeValue(input)
	if err != nil {
		t.Fatal(err)
	}
	fractionValue, err := ApplyHistogramFractionRuntimeValue(0, 1, input)
	if err != nil {
		t.Fatal(err)
	}
	sumValue, err := ApplyHistogramSumRuntimeValue(input)
	if err != nil {
		t.Fatal(err)
	}
	avgValue, err := ApplyHistogramAvgRuntimeValue(input)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(countValue.(model.VectorValue).Samples); got != 0 {
		t.Fatalf("expected empty histogram_count result for non-histogram input, got %#v", countValue)
	}
	if got := len(fractionValue.(model.VectorValue).Samples); got != 0 {
		t.Fatalf("expected empty histogram_fraction result for non-histogram input, got %#v", fractionValue)
	}
	if got := len(sumValue.(model.VectorValue).Samples); got != 0 {
		t.Fatalf("expected empty histogram_sum result for non-histogram input, got %#v", sumValue)
	}
	if got := len(avgValue.(model.VectorValue).Samples); got != 0 {
		t.Fatalf("expected empty histogram_avg result for non-histogram input, got %#v", avgValue)
	}
}

func TestApplyHistogramFractionRuntimeValueRangeClassicBuckets(t *testing.T) {
	value, err := ApplyHistogramFractionRuntimeValue(0, 0.2, model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.1"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}, {Timestamp: 20, Value: 4}}},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.2"}, Values: []model.RangePoint{{Timestamp: 10, Value: 6}, {Timestamp: 20, Value: 8}}},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "0.5"}, Values: []model.RangePoint{{Timestamp: 10, Value: 9}, {Timestamp: 20, Value: 9}}},
		{Metric: map[string]string{"__name__": "request_duration_seconds_bucket", "job": "api", "le": "+Inf"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 10}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected model.MatrixValue, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one fraction series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected two range points, got %#v", matrix.Series[0].Values)
	}
	assertFloatClose(t, matrix.Series[0].Values[0].Value, 0.6)
	assertFloatClose(t, matrix.Series[0].Values[1].Value, 0.8)
}

func TestApplyHistogramQuantileRuntimeValueRejectsScalarInput(t *testing.T) {
	_, err := ApplyHistogramQuantileRuntimeValue(0.9, model.ScalarValue{Timestamp: 1, Value: 1})
	if err == nil {
		t.Fatal("expected histogram_quantile type error")
	}
	execErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected exec.Error, got %T (%v)", err, err)
	}
	if execErr.Kind != ErrorKindExecution {
		t.Fatalf("expected execution error kind, got %v (%v)", execErr.Kind, err)
	}
}

func TestApplyHistogramFractionRuntimeValueRejectsScalarInput(t *testing.T) {
	_, err := ApplyHistogramFractionRuntimeValue(0, 1, model.ScalarValue{Timestamp: 1, Value: 1})
	if err == nil {
		t.Fatal("expected histogram_fraction type error")
	}
	execErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected exec.Error, got %T (%v)", err, err)
	}
	if execErr.Kind != ErrorKindExecution {
		t.Fatalf("expected execution error kind, got %v (%v)", execErr.Kind, err)
	}
}

func assertFloatClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.IsNaN(want) {
		if !math.IsNaN(got) {
			t.Fatalf("expected NaN, got %v", got)
		}
		return
	}
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
