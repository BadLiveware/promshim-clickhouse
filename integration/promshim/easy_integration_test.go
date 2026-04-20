package promshim_test

import (
	"strings"
	"testing"
)

func TestEasyInstantSelectorQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "vector")
	if len(data["result"].([]any)) == 0 {
		t.Fatal("expected at least one result")
	}
}

func TestEasyEqualityMatcherQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%7Bjob%3D%22clickhouse%22%7D")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	rows := payload["data"].(map[string]any)["result"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected clickhouse results")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		assertEqual(t, metric["job"], "clickhouse")
	}
}

func TestEasyRegexMatcherQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%7Bjob%3D~%22click.*%22%7D")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	rows := payload["data"].(map[string]any)["result"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected regex-matched results")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if !strings.HasPrefix(metric["job"].(string), "click") {
			t.Fatalf("unexpected job value: %v", metric["job"])
		}
	}
}

func TestEasyRangeFunctionQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=rate(coredns_dns_request_size_bytes_count%5B5m%5D)")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	assertEqual(t, payload["data"].(map[string]any)["resultType"], "vector")
}

func TestEasyQueryRange(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=up&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "matrix")
	if len(data["result"].([]any)) == 0 {
		t.Fatal("expected matrix rows")
	}
}

func TestEasyLabelsEndpoint(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/labels")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	values := toStringSet(payload["data"].([]any))
	if _, ok := values["__name__"]; !ok {
		t.Fatal("expected __name__ label")
	}
	if _, ok := values["job"]; !ok {
		t.Fatal("expected job label")
	}
}

func TestEasyLabelValuesEndpoint(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/label/job/values")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	values := toStringSet(payload["data"].([]any))
	if _, ok := values["clickhouse"]; !ok {
		t.Fatal("expected clickhouse label value")
	}
}

func TestEasySeriesEndpoint(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/series?match%5B%5D=up%7Bjob%3D%22clickhouse%22%7D")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	rows := payload["data"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected clickhouse series")
	}
	for _, row := range rows {
		series := row.(map[string]any)
		assertEqual(t, series["__name__"], "up")
		assertEqual(t, series["job"], "clickhouse")
	}
}
