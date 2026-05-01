package exec

import (
	"math"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestApplyPredictLinearRejectsNonMatrixInput(t *testing.T) {
	_, err := ApplyPredictLinear(60, model.VectorValue{}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(0, 0).UTC()})
	if err == nil {
		t.Fatal("expected unsupported error for non-matrix input")
	}
}

func TestApplyPredictLinearComputesPredictionAtEvaluationTime(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(4, 0).UTC()}
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "harness_queue_depth", "job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}, {Timestamp: 3, Value: 3}},
	}}}
	vector, err := ApplyPredictLinear(2, input, params)
	if err != nil {
		t.Fatalf("expected predict_linear result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Timestamp != 4 {
		t.Fatalf("expected evaluation timestamp output, got %#v", vector.Samples[0])
	}
	if vector.Samples[0].Value != 6 {
		t.Fatalf("expected forward projection value 6, got %#v", vector.Samples[0])
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect metric name in predict_linear output: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyPredictLinearUsesSecondBasedTimestamps(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(4, 0).UTC()}
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}, {Timestamp: 3, Value: 3}},
	}}}
	vector, err := ApplyPredictLinear(0, input, params)
	if err != nil {
		t.Fatalf("expected predict_linear result, got error: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if math.Abs(vector.Samples[0].Value-4) > 1e-9 {
		t.Fatalf("expected intercept-at-eval result of 4, got %#v", vector.Samples[0])
	}
}

func TestApplyPredictLinearDurationZeroReturnsInterceptNotLastSample(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(4, 0).UTC()}
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}, {Timestamp: 3, Value: 3}},
	}}}
	vector, err := ApplyPredictLinear(0, input, params)
	if err != nil {
		t.Fatalf("expected predict_linear result, got error: %v", err)
	}
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 4 {
		t.Fatalf("expected intercept-at-eval result of 4, got %#v", vector.Samples)
	}
}

func TestApplyPredictLinearConstantSeriesShortCircuits(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.UnixMilli(4000).UTC()}
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 1000, Value: 7}, {Timestamp: 2000, Value: 7}, {Timestamp: 3000, Value: 7}},
	}}}
	vector, err := ApplyPredictLinear(60, input, params)
	if err != nil {
		t.Fatalf("expected predict_linear result, got error: %v", err)
	}
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 7 {
		t.Fatalf("expected constant-series result of 7, got %#v", vector.Samples)
	}
}

func TestApplyPredictLinearConstantInfSeriesReturnsNaN(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.UnixMilli(4000).UTC()}
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 1000, Value: math.Inf(1)}, {Timestamp: 2000, Value: math.Inf(1)}},
	}}}
	vector, err := ApplyPredictLinear(60, input, params)
	if err != nil {
		t.Fatalf("expected predict_linear result, got error: %v", err)
	}
	if len(vector.Samples) != 1 || !math.IsNaN(vector.Samples[0].Value) {
		t.Fatalf("expected NaN constant-inf result, got %#v", vector.Samples)
	}
}

func TestApplyPredictLinearSkipsSeriesWithInsufficientSamples(t *testing.T) {
	params := EvalParams{Mode: EvalModeInstant, EvaluationTime: time.UnixMilli(4000).UTC()}
	input := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api", "instance": "a"},
		Values: []model.RangePoint{{Timestamp: 1000, Value: 1}},
	}}}
	vector, err := ApplyPredictLinear(60, input, params)
	if err != nil {
		t.Fatalf("expected predict_linear result, got error: %v", err)
	}
	if len(vector.Samples) != 0 {
		t.Fatalf("expected insufficient-sample series to be dropped, got %#v", vector.Samples)
	}
}
