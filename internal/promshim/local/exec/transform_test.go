package exec

import (
	"math"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
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

func TestApplyScalarSupportsSingleMultiAndEmptyVectors(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(42, 500_000_000).UTC()}
	single, err := ApplyScalar(model.VectorValue{Samples: []model.InstantSample{{Timestamp: 1, Value: 7}}}, params)
	if err != nil {
		t.Fatalf("expected scalar() output, got error: %v", err)
	}
	if single.Value != 7 || single.Timestamp != scalarEvalTimestamp(params) {
		t.Fatalf("unexpected scalar() single-series output: %#v", single)
	}
	multi, err := ApplyScalar(model.VectorValue{Samples: []model.InstantSample{{Timestamp: 1, Value: 7}, {Timestamp: 2, Value: 8}}}, params)
	if err != nil {
		t.Fatalf("expected scalar() multi-series output, got error: %v", err)
	}
	if !math.IsNaN(multi.Value) {
		t.Fatalf("expected scalar() multi-series output to be NaN, got %#v", multi)
	}
	empty, err := ApplyScalar(model.VectorValue{}, params)
	if err != nil {
		t.Fatalf("expected scalar() empty-vector output, got error: %v", err)
	}
	if !math.IsNaN(empty.Value) {
		t.Fatalf("expected scalar() empty-vector output to be NaN, got %#v", empty)
	}
}

func TestApplyScalarBuiltinFunctionSupportsPiAndTime(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(42, 500_000_000).UTC()}
	piValue, err := ApplyScalarBuiltinFunction("pi", params)
	if err != nil {
		t.Fatalf("expected pi builtin, got error: %v", err)
	}
	if piValue.Value != math.Pi {
		t.Fatalf("expected pi value, got %#v", piValue)
	}
	timeValue, err := ApplyScalarBuiltinFunction("time", params)
	if err != nil {
		t.Fatalf("expected time builtin, got error: %v", err)
	}
	if timeValue.Value != scalarEvalTimestamp(params) {
		t.Fatalf("expected eval timestamp value, got %#v", timeValue)
	}
}

func TestApplyPointwiseFunctionSupportsAbsClampAndTimestamp(t *testing.T) {
	vector := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"__name__": "up", "job": "api"}, Timestamp: 12.5, Value: -3.7}}}
	absVector, err := ApplyPointwiseFunction("abs", vector, EvalParams{Mode: EvalModeInstant}, nil)
	if err != nil {
		t.Fatalf("expected abs output, got error: %v", err)
	}
	if absVector.Samples[0].Value != 3.7 {
		t.Fatalf("expected abs value, got %#v", absVector.Samples)
	}
	if _, ok := absVector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("expected abs() to drop metric name, got %#v", absVector.Samples[0].Metric)
	}
	low, high := 1.0, 2.0
	clampVector, err := ApplyPointwiseFunction("clamp", vector, EvalParams{Mode: EvalModeInstant}, []*float64{&low, &high})
	if err != nil {
		t.Fatalf("expected clamp output, got error: %v", err)
	}
	if clampVector.Samples[0].Value != 1 {
		t.Fatalf("expected clamped value 1, got %#v", clampVector.Samples)
	}
	timestampVector, err := ApplyPointwiseFunction("timestamp", vector, EvalParams{Mode: EvalModeInstant}, nil)
	if err != nil {
		t.Fatalf("expected timestamp output, got error: %v", err)
	}
	if timestampVector.Samples[0].Value != 12.5 {
		t.Fatalf("expected timestamp value, got %#v", timestampVector.Samples)
	}
}

func TestApplyPointwiseFunctionSupportsDateDefaultAndDayOfWeek(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Date(2024, time.March, 3, 14, 5, 0, 0, time.UTC)}
	minuteVector, err := ApplyPointwiseFunction("minute", nil, params, nil)
	if err != nil {
		t.Fatalf("expected minute() output, got error: %v", err)
	}
	if len(minuteVector.Samples) != 1 || minuteVector.Samples[0].Value != 5 {
		t.Fatalf("unexpected minute() output: %#v", minuteVector.Samples)
	}

	weekdays := []struct {
		date time.Time
		want float64
	}{
		{date: time.Date(2024, time.March, 3, 0, 0, 0, 0, time.UTC), want: 0},
		{date: time.Date(2024, time.March, 4, 0, 0, 0, 0, time.UTC), want: 1},
		{date: time.Date(2024, time.March, 5, 0, 0, 0, 0, time.UTC), want: 2},
		{date: time.Date(2024, time.March, 6, 0, 0, 0, 0, time.UTC), want: 3},
		{date: time.Date(2024, time.March, 7, 0, 0, 0, 0, time.UTC), want: 4},
		{date: time.Date(2024, time.March, 8, 0, 0, 0, 0, time.UTC), want: 5},
		{date: time.Date(2024, time.March, 9, 0, 0, 0, 0, time.UTC), want: 6},
	}
	for _, weekday := range weekdays {
		vector := model.VectorValue{Samples: []model.InstantSample{{Timestamp: 10, Value: float64(weekday.date.Unix())}}}
		dayVector, err := ApplyPointwiseFunction("day_of_week", vector, EvalParams{Mode: EvalModeInstant}, nil)
		if err != nil {
			t.Fatalf("expected day_of_week output for %s, got error: %v", weekday.date.Format("2006-01-02"), err)
		}
		if dayVector.Samples[0].Value != weekday.want {
			t.Fatalf("expected %s to map to %v, got %#v", weekday.date.Format("2006-01-02"), weekday.want, dayVector.Samples)
		}
	}
}

func floatPtr(v float64) *float64 {
	return &v
}
