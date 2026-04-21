package exec

import (
	"math"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestAggregateRuntimeValueSumsVectorSamples(t *testing.T) {
	value, err := AggregateRuntimeValue(parser.SUM, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: 0},
	}}, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}

	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected model.VectorValue, got %T", value)
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
	value, err := AggregateRuntimeValue(parser.COUNT, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: math.NaN()},
	}}, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}

	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected count aggregation result: %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueAvgVectorSamples(t *testing.T) {
	value, err := AggregateRuntimeValue(parser.AVG, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: 0},
	}}, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}

	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 0.5 {
		t.Fatalf("unexpected avg aggregation result: %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueStddevAndStdvarVectorSamples(t *testing.T) {
	stddevValue, err := AggregateRuntimeValue(parser.STDDEV, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"job": "clickhouse", "instance": "b"}, Value: 3},
	}}, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	stdvarValue, err := AggregateRuntimeValue(parser.STDVAR, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"job": "clickhouse", "instance": "b"}, Value: 3},
	}}, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}

	stddevVector := stddevValue.(model.VectorValue)
	stdvarVector := stdvarValue.(model.VectorValue)
	if len(stddevVector.Samples) != 1 || stddevVector.Samples[0].Value != 1 {
		t.Fatalf("unexpected stddev aggregation result: %#v", stddevVector.Samples)
	}
	if len(stdvarVector.Samples) != 1 || stdvarVector.Samples[0].Value != 1 {
		t.Fatalf("unexpected stdvar aggregation result: %#v", stdvarVector.Samples)
	}
}

func TestAggregateRuntimeValueQuantileAndGroupVectorSamples(t *testing.T) {
	quantile := 0.5
	quantileValue, err := AggregateRuntimeValue(parser.QUANTILE, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"job": "clickhouse", "instance": "b"}, Value: 3},
		{Metric: map[string]string{"job": "clickhouse", "instance": "c"}, Value: 5},
	}}, AggregationOptions{Grouping: []string{"job"}, ParamNumber: &quantile, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	groupValue, err := AggregateRuntimeValue(parser.GROUP, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"job": "clickhouse", "instance": "b"}, Value: 5},
	}}, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}

	quantileVector := quantileValue.(model.VectorValue)
	groupVector := groupValue.(model.VectorValue)
	if len(quantileVector.Samples) != 1 || quantileVector.Samples[0].Value != 3 {
		t.Fatalf("unexpected quantile aggregation result: %#v", quantileVector.Samples)
	}
	if len(groupVector.Samples) != 1 || groupVector.Samples[0].Value != 1 {
		t.Fatalf("unexpected group aggregation result: %#v", groupVector.Samples)
	}
}

func TestAggregateRuntimeValueMinMaxIgnoreTrailingNaN(t *testing.T) {
	samples := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "a"}, Value: 1},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "b"}, Value: math.NaN()},
		{Metric: map[string]string{"__name__": "up", "job": "clickhouse", "instance": "c"}, Value: 0},
	}}

	minValue, err := AggregateRuntimeValue(parser.MIN, samples, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	maxValue, err := AggregateRuntimeValue(parser.MAX, samples, AggregationOptions{Grouping: []string{"job"}, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}

	minVector := minValue.(model.VectorValue)
	maxVector := maxValue.(model.VectorValue)
	if minVector.Samples[0].Value != 0 {
		t.Fatalf("unexpected min aggregation result: %#v", minVector.Samples)
	}
	if maxVector.Samples[0].Value != 1 {
		t.Fatalf("unexpected max aggregation result: %#v", maxVector.Samples)
	}
}

func TestAggregateRuntimeValueTopKReturnsHighestSamplesPerGroup(t *testing.T) {
	k := 2.0
	value, err := AggregateRuntimeValue(parser.TOPK, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 42, Value: 1},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Timestamp: 42, Value: 3},
		{Metric: map[string]string{"job": "api", "instance": "c"}, Timestamp: 42, Value: 2},
		{Metric: map[string]string{"job": "worker", "instance": "d"}, Timestamp: 42, Value: 9},
	}}, AggregationOptions{Grouping: []string{"job"}, ParamNumber: &k, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 3 {
		t.Fatalf("expected three topk samples across groups, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "b" || vector.Samples[0].Value != 3 {
		t.Fatalf("expected highest api sample first, got %#v", vector.Samples)
	}
	if vector.Samples[1].Metric["instance"] != "c" || vector.Samples[1].Value != 2 {
		t.Fatalf("expected second api sample next, got %#v", vector.Samples)
	}
	if vector.Samples[2].Metric["instance"] != "d" || vector.Samples[2].Value != 9 {
		t.Fatalf("expected worker sample to be preserved, got %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueBottomKReturnsLowestSamples(t *testing.T) {
	k := 2.0
	value, err := AggregateRuntimeValue(parser.BOTTOMK, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"instance": "a"}, Timestamp: 42, Value: 5},
		{Metric: map[string]string{"instance": "b"}, Timestamp: 42, Value: 1},
		{Metric: map[string]string{"instance": "c"}, Timestamp: 42, Value: 3},
	}}, AggregationOptions{ParamNumber: &k, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two bottomk samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "b" || vector.Samples[0].Value != 1 {
		t.Fatalf("expected lowest sample first, got %#v", vector.Samples)
	}
	if vector.Samples[1].Metric["instance"] != "c" || vector.Samples[1].Value != 3 {
		t.Fatalf("expected second-lowest sample second, got %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueCountValuesAddsValueLabelAndCountsOccurrences(t *testing.T) {
	value, err := AggregateRuntimeValue(parser.COUNT_VALUES, model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 42, Value: 1},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Timestamp: 42, Value: 1},
		{Metric: map[string]string{"job": "api", "instance": "c"}, Timestamp: 42, Value: 2},
	}}, AggregationOptions{Grouping: []string{"job"}, ParamString: "sample_value", EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two count_values rows, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["job"] != "api" || vector.Samples[0].Metric["sample_value"] != "1" || vector.Samples[0].Value != 2 {
		t.Fatalf("unexpected count_values first row: %#v", vector.Samples)
	}
	if vector.Samples[1].Metric["job"] != "api" || vector.Samples[1].Metric["sample_value"] != "2" || vector.Samples[1].Value != 1 {
		t.Fatalf("unexpected count_values second row: %#v", vector.Samples)
	}
}

func TestAggregateRuntimeValueTopKRangeSelectsPerTimestampAndMergesSeries(t *testing.T) {
	k := 1.0
	value, err := AggregateRuntimeValue(parser.TOPK, model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 4}}},
		{Metric: map[string]string{"instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: 2}}},
	}}, AggregationOptions{ParamNumber: &k, EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 2 {
		t.Fatalf("expected merged topk matrix with two sparse series, got %#v", matrix.Series)
	}
	if matrix.Series[0].Metric["instance"] != "a" || len(matrix.Series[0].Values) != 1 || matrix.Series[0].Values[0].Timestamp != 20 {
		t.Fatalf("expected instance a only at second step, got %#v", matrix.Series)
	}
	if matrix.Series[1].Metric["instance"] != "b" || len(matrix.Series[1].Values) != 1 || matrix.Series[1].Values[0].Timestamp != 10 {
		t.Fatalf("expected instance b only at first step, got %#v", matrix.Series)
	}
}

func TestAggregateRuntimeValueCountValuesRangeCountsPerTimestamp(t *testing.T) {
	value, err := AggregateRuntimeValue(parser.COUNT_VALUES, model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}}},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 1}}},
	}}, AggregationOptions{Grouping: []string{"job"}, ParamString: "sample_value", EvaluationTime: time.Unix(42, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 2 {
		t.Fatalf("expected two count_values series across timestamps, got %#v", matrix.Series)
	}
	if matrix.Series[0].Metric["sample_value"] != "1" || len(matrix.Series[0].Values) != 2 || matrix.Series[0].Values[0].Value != 2 || matrix.Series[0].Values[1].Value != 1 {
		t.Fatalf("unexpected count_values series for sample_value=1: %#v", matrix.Series)
	}
	if matrix.Series[1].Metric["sample_value"] != "2" || len(matrix.Series[1].Values) != 1 || matrix.Series[1].Values[0].Value != 1 {
		t.Fatalf("unexpected count_values series for sample_value=2: %#v", matrix.Series)
	}
}

func TestAggregateRuntimeValueRejectsMissingTopKParameter(t *testing.T) {
	_, err := AggregateRuntimeValue(parser.TOPK, model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "clickhouse"}, Value: 1}}}, AggregationOptions{EvaluationTime: time.Unix(42, 0).UTC()})
	if err == nil {
		t.Fatal("expected missing parameter error")
	}
	execErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected exec.Error, got %T (%v)", err, err)
	}
	if execErr.Kind != ErrorKindBadData {
		t.Fatalf("unexpected error kind: %v (%v)", execErr.Kind, err)
	}
}
