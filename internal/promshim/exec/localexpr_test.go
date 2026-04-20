package exec

import (
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestApplyBinaryRuntimeValueScalarScalar(t *testing.T) {
	value, err := ApplyBinaryRuntimeValue(parser.ADD, model.ScalarValue{Value: 1}, model.ScalarValue{Value: 2}, false, EvalParams{
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
	value, err := ApplyBinaryRuntimeValue(parser.MUL, left, model.ScalarValue{Value: 100}, false, EvalParams{
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
	value, err := ApplyBinaryRuntimeValue(parser.EQLC, left, model.ScalarValue{Value: 1}, false, EvalParams{
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
	value, err := ApplyBinaryRuntimeValue(parser.EQLC, left, model.ScalarValue{Value: 1}, true, EvalParams{
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
