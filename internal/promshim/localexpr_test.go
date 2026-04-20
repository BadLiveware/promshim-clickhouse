package promshim

import (
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestApplyBinaryRuntimeValueScalarScalar(t *testing.T) {
	value, err := applyBinaryRuntimeValue(parser.ADD, scalarValue{Value: 1}, scalarValue{Value: 2}, false, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	scalar, ok := value.(scalarValue)
	if !ok {
		t.Fatalf("expected scalarValue, got %T", value)
	}
	if scalar.Value != 3 {
		t.Fatalf("unexpected scalar result: %#v", scalar)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarDropsNameForArithmetic(t *testing.T) {
	value, err := applyBinaryRuntimeValue(parser.MUL, vectorValue{Samples: []instantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     1,
	}}}, scalarValue{Value: 100}, false, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(vectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 100 {
		t.Fatalf("unexpected vector result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ after vector-scalar arithmetic: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarComparisonKeepsName(t *testing.T) {
	value, err := applyBinaryRuntimeValue(parser.EQLC, vectorValue{Samples: []instantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     1,
	}}}, scalarValue{Value: 1}, false, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(vectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 1 {
		t.Fatalf("unexpected vector comparison result: %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["__name__"] != "up" {
		t.Fatalf("expected __name__ to be preserved: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorScalarComparisonBoolDropsName(t *testing.T) {
	value, err := applyBinaryRuntimeValue(parser.EQLC, vectorValue{Samples: []instantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     0,
	}}}, scalarValue{Value: 1}, true, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(vectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 0 {
		t.Fatalf("unexpected vector bool comparison result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ after bool comparison: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyUnaryRuntimeValueNegatesVectorAndDropsName(t *testing.T) {
	value, err := applyUnaryRuntimeValue(parser.SUB, vectorValue{Samples: []instantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     2,
	}}}, evalParams{Mode: evalModeInstant, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(vectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != -2 {
		t.Fatalf("unexpected unary result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ after unary minus: %#v", vector.Samples[0].Metric)
	}
}
