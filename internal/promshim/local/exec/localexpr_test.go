package exec

import (
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestApplyBinaryRuntimeValueScalarScalar(t *testing.T) {
	value, err := ApplyBinaryRuntimeValue(parser.ADD, model.ScalarValue{Value: 1}, model.ScalarValue{Value: 2}, nil, false, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	scalar, ok := value.(model.ScalarValue)
	if !ok {
		t.Fatalf("expected model.ScalarValue, got %T", value)
	}
	if scalar.Value != 3 {
		t.Fatalf("unexpected scalar result: %#v", scalar)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarDropsNameForArithmetic(t *testing.T) {
	left := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     1,
	}}}
	value, err := ApplyBinaryRuntimeValue(parser.MUL, left, model.ScalarValue{Value: 100}, nil, false, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 100 {
		t.Fatalf("unexpected vector result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ after vector-scalar arithmetic: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarComparisonKeepsName(t *testing.T) {
	left := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     1,
	}}}
	value, err := ApplyBinaryRuntimeValue(parser.EQLC, left, model.ScalarValue{Value: 1}, nil, false, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 1 {
		t.Fatalf("unexpected vector comparison result: %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["__name__"] != "up" {
		t.Fatalf("expected __name__ to be preserved: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarComparisonBoolDropsName(t *testing.T) {
	left := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     0,
	}}}
	value, err := ApplyBinaryRuntimeValue(parser.EQLC, left, model.ScalarValue{Value: 1}, nil, true, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 0 {
		t.Fatalf("unexpected vector bool comparison result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ after bool comparison: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyUnaryRuntimeValueNegatesVectorAndDropsName(t *testing.T) {
	value := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     2,
	}}}
	result, err := ApplyUnaryRuntimeValue(parser.SUB, value, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	vector := result.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != -2 {
		t.Fatalf("unexpected unary result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ after unary minus: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyUnaryRuntimeValueRejectsDuplicateLabelsetsAfterNameDrop(t *testing.T) {
	value := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "metric_a", "job": "api"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"__name__": "metric_b", "job": "api"}, Timestamp: 1, Value: 2},
	}}
	_, err := ApplyUnaryRuntimeValue(parser.SUB, value, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err == nil {
		t.Fatal("expected duplicate labelset error")
	}
	execErr, ok := err.(*Error)
	if !ok || execErr.Kind != ErrorKindBadData {
		t.Fatalf("expected bad_data kind, got %T (%v)", err, err)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarRejectsDuplicateLabelsetsAfterNameDrop(t *testing.T) {
	left := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "metric_a", "job": "api"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"__name__": "metric_b", "job": "api"}, Timestamp: 1, Value: 2},
	}}
	_, err := ApplyBinaryRuntimeValue(parser.MUL, left, model.ScalarValue{Value: 1}, nil, false, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(42, 0).UTC(),
	})
	if err == nil {
		t.Fatal("expected duplicate labelset error")
	}
	execErr, ok := err.(*Error)
	if !ok || execErr.Kind != ErrorKindBadData {
		t.Fatalf("expected bad_data kind, got %T (%v)", err, err)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarRangeRejectsDuplicateLabelsetsAfterNameDrop(t *testing.T) {
	left := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "metric_a", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}}},
		{Metric: map[string]string{"__name__": "metric_b", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 2}}},
	}}
	_, err := ApplyBinaryRuntimeValue(parser.MUL, left, model.ScalarValue{Value: 1}, nil, false, EvalParams{
		Mode:           EvalModeRange,
		EvaluationTime: time.Unix(42, 0).UTC(),
		Start:          time.Unix(10, 0).UTC(),
		End:            time.Unix(10, 0).UTC(),
		Step:           time.Minute,
	})
	if err == nil {
		t.Fatal("expected duplicate labelset range error")
	}
	execErr, ok := err.(*Error)
	if !ok || execErr.Kind != ErrorKindBadData {
		t.Fatalf("expected bad_data kind, got %T (%v)", err, err)
	}
}

func TestApplyBinaryRuntimeValueScalarScalarRangeBuildsConstantMatrix(t *testing.T) {
	value, err := ApplyBinaryRuntimeValue(parser.ADD, model.ScalarValue{Value: 1}, model.ScalarValue{Value: 2}, nil, false, EvalParams{
		Mode:           EvalModeRange,
		EvaluationTime: time.Unix(120, 0).UTC(),
		Start:          time.Unix(0, 0).UTC(),
		End:            time.Unix(90, 0).UTC(),
		Step:           30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected model.MatrixValue, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one scalar range series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Metric) != 0 {
		t.Fatalf("expected empty metric for scalar range result, got %#v", matrix.Series[0].Metric)
	}
	if len(matrix.Series[0].Values) != 4 {
		t.Fatalf("expected four points, got %#v", matrix.Series[0].Values)
	}
	for _, point := range matrix.Series[0].Values {
		if point.Value != 3 {
			t.Fatalf("expected constant scalar range value 3, got %#v", matrix.Series[0].Values)
		}
	}
}
