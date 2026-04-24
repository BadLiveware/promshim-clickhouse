package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestRenderInstantQueryValueForVector(t *testing.T) {
	resultType, result, err := RenderInstantQueryValue(model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"job": "clickhouse"},
		Timestamp: 123.5,
		Value:     1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if resultType != "vector" {
		t.Fatalf("expected vector result type, got %q", resultType)
	}
	rows := result.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d", len(rows))
	}
	metric := rows[0]["metric"].(map[string]string)
	if metric["job"] != "clickhouse" {
		t.Fatalf("unexpected metric: %#v", metric)
	}
	value := rows[0]["value"].([]any)
	if value[1] != "1" {
		t.Fatalf("unexpected value payload: %#v", value)
	}
}

func TestWritePromSuccessInstantValueStreamsVectorJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := WritePromSuccessInstantValue(recorder, model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"job": "clickhouse"},
		Timestamp: 123.5,
		Value:     1,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("unexpected content type: %q", contentType)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode streamed JSON: %v", err)
	}
	if payload["status"] != "success" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	data := payload["data"].(map[string]any)
	if data["resultType"] != "vector" {
		t.Fatalf("unexpected result type: %#v", data)
	}
	rows := data["result"].([]any)
	if len(rows) != 1 {
		t.Fatalf("unexpected vector result rows: %#v", rows)
	}
}

func TestWritePromSuccessRangeValueStreamsMatrixJSON(t *testing.T) {
	recorder := httptest.NewRecorder()
	err := WritePromSuccessRangeValue(recorder, model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "clickhouse"},
		Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode streamed JSON: %v", err)
	}
	data := payload["data"].(map[string]any)
	if data["resultType"] != "matrix" {
		t.Fatalf("unexpected result type: %#v", data)
	}
	rows := data["result"].([]any)
	if len(rows) != 1 {
		t.Fatalf("unexpected matrix result rows: %#v", rows)
	}
}
