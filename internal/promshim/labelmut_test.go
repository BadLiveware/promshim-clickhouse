package promshim

import (
	"testing"
)

func TestApplyLabelReplaceRuntimeValueAddsLabel(t *testing.T) {
	cfg, err := buildLabelReplaceConfig("job_copy", "$1", "job", "(.*)")
	if err != nil {
		t.Fatal(err)
	}
	value, err := applyLabelReplaceRuntimeValue(vectorValue{Samples: []instantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse"},
		Timestamp: 1,
		Value:     1,
	}}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(vectorValue)
	if vector.Samples[0].Metric["job_copy"] != "clickhouse" {
		t.Fatalf("expected copied label, got %#v", vector.Samples[0].Metric)
	}
	if vector.Samples[0].Metric["__name__"] != "up" {
		t.Fatalf("expected __name__ to be preserved, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyLabelJoinRuntimeValueAddsJoinedLabel(t *testing.T) {
	cfg, err := buildLabelJoinConfig("joined", "/", []string{"job", "namespace"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := applyLabelJoinRuntimeValue(vectorValue{Samples: []instantSample{{
		Metric:    map[string]string{"__name__": "up", "job": "clickhouse", "namespace": "monitoring-v2"},
		Timestamp: 1,
		Value:     1,
	}}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(vectorValue)
	if vector.Samples[0].Metric["joined"] != "clickhouse/monitoring-v2" {
		t.Fatalf("expected joined label, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyLabelReplaceRuntimeValueRejectsDuplicateInstantLabelsets(t *testing.T) {
	cfg, err := buildLabelReplaceConfig("job", "same", "job", ".*")
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyLabelReplaceRuntimeValue(vectorValue{Samples: []instantSample{
		{Metric: map[string]string{"job": "a", "instance": "x"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"job": "b", "instance": "x"}, Timestamp: 1, Value: 2},
	}}, cfg)
	if err == nil {
		t.Fatal("expected duplicate labelset error")
	}
}

func TestApplyLabelJoinRuntimeValueMergesNonOverlappingRangeSeries(t *testing.T) {
	cfg, err := buildLabelJoinConfig("service", "", []string{"job"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := applyLabelJoinRuntimeValue(matrixValue{Series: []rangeSeries{
		{Metric: map[string]string{"job": "a", "instance": "x"}, Values: []rangePoint{{Timestamp: 1, Value: 1}}},
		{Metric: map[string]string{"job": "a", "instance": "x"}, Values: []rangePoint{{Timestamp: 2, Value: 2}}},
	}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	matrix := value.(matrixValue)
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected merged range series, got %#v", matrix.Series)
	}
}
