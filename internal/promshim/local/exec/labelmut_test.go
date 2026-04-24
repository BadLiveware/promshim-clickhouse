package exec

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestApplyLabelReplaceRuntimeValueAddsLabel(t *testing.T) {
	cfg, err := model.BuildLabelReplaceConfig("job_copy", "$1", "job", "(.*)")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyLabelReplaceRuntimeValue(model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     1,
	}}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vector := result.(model.VectorValue)
	if vector.Samples[0].Metric["job_copy"] != "clickhouse" {
		t.Fatalf("expected copied label, got %#v", vector.Samples[0].Metric)
	}
	if vector.Samples[0].Metric["__name__"] != "up" {
		t.Fatalf("expected __name__ to be preserved, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyLabelJoinRuntimeValueAddsJoinedLabel(t *testing.T) {
	cfg, err := model.BuildLabelJoinConfig("joined", "/", []string{"job", "namespace"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyLabelJoinRuntimeValue(model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse", "namespace": "example-namespace"},
		Timestamp: 1,
		Value:     1,
	}}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vector := result.(model.VectorValue)
	if vector.Samples[0].Metric["joined"] != "clickhouse/example-namespace" {
		t.Fatalf("expected joined label, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyLabelReplaceRuntimeValuePreservesMetricNameWhenRewritingName(t *testing.T) {
	cfg, err := model.BuildLabelReplaceConfig("__name__", "rate_$1", "__name__", "(.+)")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyLabelReplaceRuntimeValue(model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_total", "env": "1"},
		Timestamp: 1,
		Value:     0.2,
	}}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vector := result.(model.VectorValue)
	if vector.Samples[0].Metric["__name__"] != "rate_metric_total" {
		t.Fatalf("expected rewritten metric name, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyLabelJoinRuntimeValuePreservesMetricNameWhenJoiningName(t *testing.T) {
	cfg, err := model.BuildLabelJoinConfig("__name__", "_", []string{"__name__", "env"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyLabelJoinRuntimeValue(model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_total", "env": "1"},
		Timestamp: 1,
		Value:     0.2,
	}}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vector := result.(model.VectorValue)
	if vector.Samples[0].Metric["__name__"] != "metric_total_1" {
		t.Fatalf("expected joined metric name, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyLabelReplaceRuntimeValueRejectsDuplicateInstantLabelsets(t *testing.T) {
	cfg, err := model.BuildLabelReplaceConfig("job", "same", "job", ".*")
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyLabelReplaceRuntimeValue(model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "a", "instance": "x"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"job": "b", "instance": "x"}, Timestamp: 1, Value: 2},
	}}, cfg)
	if err == nil {
		t.Fatal("expected duplicate labelset error")
	}
}

func TestApplyLabelJoinRuntimeValueMergesNonOverlappingRangeSeries(t *testing.T) {
	cfg, err := model.BuildLabelJoinConfig("service", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyLabelJoinRuntimeValue(model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"job": "a", "instance": "x"}, Values: []model.RangePoint{{Timestamp: 1, Value: 1}}},
		{Metric: map[string]string{"job": "a", "instance": "x"}, Values: []model.RangePoint{{Timestamp: 2, Value: 2}}},
	}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	matrix := result.(model.MatrixValue)
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected merged range series, got %#v", matrix.Series)
	}
}
