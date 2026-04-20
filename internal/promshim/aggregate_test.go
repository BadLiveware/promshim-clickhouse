package promshim

import (
	"math"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestAggregateRuntimeValueSumsVectorSamples(t *testing.T) {
	value, err := aggregateRuntimeValue(parser.SUM, vectorValue{Samples: []instantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: 0},
	}}, []string{"job"}, false, time.Unix(42, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	vector, ok := value.(vectorValue)
	if !ok {
		t.Fatalf("expected vectorValue, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one aggregated sample, got %d", len(vector.Samples))
	}
	if vector.Samples[0].Metric["job"] != "clickhouse" {
		t.Fatalf("unexpected metric: %#v", vector.Samples[0].Metric)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ label: %#v", vector.Samples[0].Metric)
	}
	if vector.Samples[0].Value != 1 {
		t.Fatalf("unexpected aggregated value: %#v", vector.Samples[0])
	}
}

func TestAggregateRuntimeValueCountsVectorSamples(t *testing.T) {
	value, err := aggregateRuntimeValue(parser.COUNT, vectorValue{Samples: []instantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: math.NaN()},
	}}, []string{"job"}, false, time.Unix(42, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	vector := value.(vectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected count aggregation result: %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueAvgVectorSamples(t *testing.T) {
	value, err := aggregateRuntimeValue(parser.AVG, vectorValue{Samples: []instantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: 0},
	}}, []string{"job"}, false, time.Unix(42, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	vector := value.(vectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 0.5 {
		t.Fatalf("unexpected avg aggregation result: %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueMinMaxIgnoreTrailingNaN(t *testing.T) {
	samples := vectorValue{Samples: []instantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: math.NaN()},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "c"}, Value: 0},
	}}

	minValue, err := aggregateRuntimeValue(parser.MIN, samples, []string{"job"}, false, time.Unix(42, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	maxValue, err := aggregateRuntimeValue(parser.MAX, samples, []string{"job"}, false, time.Unix(42, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}

	minVector := minValue.(vectorValue)
	maxVector := maxValue.(vectorValue)
	if minVector.Samples[0].Value != 0 {
		t.Fatalf("unexpected min aggregation result: %#v", minVector.Samples)
	}
	if maxVector.Samples[0].Value != 1 {
		t.Fatalf("unexpected max aggregation result: %#v", maxVector.Samples)
	}
}

func TestAggregateRuntimeValueRejectsUnsupportedReducer(t *testing.T) {
	_, err := aggregateRuntimeValue(parser.TOPK, vectorValue{Samples: []instantSample{{Metric: map[string]string{"job": "clickhouse"}, Value: 1}}}, nil, false, time.Unix(42, 0).UTC())
	if err == nil {
		t.Fatal("expected unsupported reducer error")
	}
	if internalErrorKindOf(err) != internalErrorKindUnsupported {
		t.Fatalf("unexpected error kind: %v (%v)", internalErrorKindOf(err), err)
	}
}
