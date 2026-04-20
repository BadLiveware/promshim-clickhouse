package promshim_test

import "testing"

func TestMediumSumQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum(up)")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "vector")
	rows := data["result"].([]any)
	assertEqual(t, len(rows), 1)
	metric := rows[0].(map[string]any)["metric"].(map[string]any)
	assertEqual(t, len(metric), 0)
}

func TestMediumSumByQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	rows := payload["data"].(map[string]any)["result"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected aggregated rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		assertEqual(t, len(metric), 1)
		if _, ok := metric["job"]; !ok {
			t.Fatalf("expected only job label, got %#v", metric)
		}
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ label, got %#v", metric)
		}
	}
}

func TestMediumSumWithoutQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum%20without%20(instance,pod)%20(up%7Bjob%3D%22clickhouse%22%7D)")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	rows := payload["data"].(map[string]any)["result"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected aggregated rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["instance"]; ok {
			t.Fatalf("did not expect instance label, got %#v", metric)
		}
		if _, ok := metric["pod"]; ok {
			t.Fatalf("did not expect pod label, got %#v", metric)
		}
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ label, got %#v", metric)
		}
		assertEqual(t, metric["job"], "clickhouse")
	}
}

func TestMediumSumByRateQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum%20by%20(job)%20(rate(coredns_dns_request_size_bytes_count%5B5m%5D))")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	rows := payload["data"].(map[string]any)["result"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected aggregated rate rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		assertEqual(t, len(metric), 1)
		if _, ok := metric["job"]; !ok {
			t.Fatalf("expected only job label, got %#v", metric)
		}
	}
}

func TestMediumSumByQueryRange(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=sum%20by%20(job)%20(up)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "success")
	data := payload["data"].(map[string]any)
	assertEqual(t, data["resultType"], "matrix")
	rows := data["result"].([]any)
	if len(rows) == 0 {
		t.Fatal("expected aggregated matrix rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		assertEqual(t, len(metric), 1)
		if _, ok := metric["job"]; !ok {
			t.Fatalf("expected only job label, got %#v", metric)
		}
	}
}

func TestMediumAvgQueryUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=avg(up)")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=medium", "aggregation operator", "avg")
}

func TestMediumBinaryQueryUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20%3D%3D%201")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=medium", "binary operators")
}

func TestMediumLabelReplaceUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=label_replace(up,%20%22foo%22,%20%22%241%22,%20%22job%22,%20%22(.*)%22)")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=medium", "label mutation helpers")
}
