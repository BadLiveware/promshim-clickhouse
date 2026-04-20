package promshim_test

import (
	"strings"
	"testing"
)

func TestMediumSumQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum(up)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	assertEqual(t, len(rows), 1)
	metric := rows[0].(map[string]any)["metric"].(map[string]any)
	assertEqual(t, len(metric), 0)
}

func TestMediumCountQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=count(up)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	assertEqual(t, len(rows), 1)
	metric := rows[0].(map[string]any)["metric"].(map[string]any)
	assertEqual(t, len(metric), 0)
}

func TestMediumGroupedAggregationsOnUp(t *testing.T) {
	f := requireFixture(t)
	sumPayload, err := f.getJSON("/api/v1/query?query=sum%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	countPayload, err := f.getJSON("/api/v1/query?query=count%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	minPayload, err := f.getJSON("/api/v1/query?query=min%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	maxPayload, err := f.getJSON("/api/v1/query?query=max%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	avgPayload, err := f.getJSON("/api/v1/query?query=avg%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}

	sumValues := vectorValuesByLabel(t, requireVectorRows(t, sumPayload), "job")
	countValues := vectorValuesByLabel(t, requireVectorRows(t, countPayload), "job")
	minValues := vectorValuesByLabel(t, requireVectorRows(t, minPayload), "job")
	maxValues := vectorValuesByLabel(t, requireVectorRows(t, maxPayload), "job")
	avgValues := vectorValuesByLabel(t, requireVectorRows(t, avgPayload), "job")

	if len(sumValues) == 0 {
		t.Fatal("expected grouped aggregation rows")
	}
	assertEqual(t, len(sumValues), len(countValues))
	assertEqual(t, len(sumValues), len(minValues))
	assertEqual(t, len(sumValues), len(maxValues))
	assertEqual(t, len(sumValues), len(avgValues))

	for job, sumValue := range sumValues {
		countValue, ok := countValues[job]
		if !ok {
			t.Fatalf("missing count result for job %q", job)
		}
		minValue, ok := minValues[job]
		if !ok {
			t.Fatalf("missing min result for job %q", job)
		}
		maxValue, ok := maxValues[job]
		if !ok {
			t.Fatalf("missing max result for job %q", job)
		}
		avgValue, ok := avgValues[job]
		if !ok {
			t.Fatalf("missing avg result for job %q", job)
		}
		if countValue < sumValue {
			t.Fatalf("expected count >= sum for job %q, got count=%v sum=%v", job, countValue, sumValue)
		}
		if avgValue < minValue || avgValue > maxValue {
			t.Fatalf("expected min <= avg <= max for job %q, got min=%v avg=%v max=%v", job, minValue, avgValue, maxValue)
		}
	}
}

func TestMediumSumWithoutQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum%20without%20(instance,pod)%20(up%7Bjob%3D%22clickhouse%22%7D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
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
	rows := requireVectorRows(t, payload)
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

func TestMediumCountByQueryRange(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=count%20by%20(job)%20(up)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
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

func TestMediumScalarInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=1%20%2B%202")
	if err != nil {
		t.Fatal(err)
	}
	value := requireScalarValue(t, payload)
	assertEqual(t, value, 3.0)
}

func TestMediumVectorScalarArithmeticQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20*%20100")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected arithmetic rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ after arithmetic op, got %#v", metric)
		}
	}
}

func TestMediumVectorScalarComparisonQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20%3D%3D%201")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected filtered comparison rows")
	}
	for _, row := range rows {
		rowMap := row.(map[string]any)
		metric := rowMap["metric"].(map[string]any)
		if _, ok := metric["__name__"]; !ok {
			t.Fatalf("expected __name__ to be preserved for non-bool comparison, got %#v", metric)
		}
		value := rowMap["value"].([]any)
		assertEqual(t, value[1], "1")
	}
}

func TestMediumVectorScalarComparisonBoolQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20%3D%3D%20bool%201")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected bool comparison rows")
	}
	for _, row := range rows {
		rowMap := row.(map[string]any)
		metric := rowMap["metric"].(map[string]any)
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ for bool comparison, got %#v", metric)
		}
		value := rowMap["value"].([]any)[1].(string)
		if value != "0" && value != "1" {
			t.Fatalf("expected bool comparison value 0 or 1, got %q", value)
		}
	}
}

func TestMediumVectorScalarArithmeticRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=up%20*%20100&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected arithmetic matrix rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ after arithmetic range op, got %#v", metric)
		}
	}
}

func TestMediumTopkQueryUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=topk(3,%20up)")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=medium", "aggregation operator", "topk")
}

func TestMediumVectorVectorBinaryQueryUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20%2B%20up")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=hard", "vector matching")
}

func TestMediumLabelReplaceQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=label_replace(up%7Bjob%3D%22clickhouse%22%7D,%20%22job_copy%22,%20%22%241%22,%20%22job%22,%20%22(.*)%22)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected label_replace rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		assertEqual(t, metric["job"], "clickhouse")
		assertEqual(t, metric["job_copy"], "clickhouse")
	}
}

func TestMediumLabelJoinQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=label_join(up%7Bjob%3D%22clickhouse%22%7D,%20%22joined%22,%20%22/%22,%20%22job%22,%20%22namespace%22)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected label_join rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		joined, ok := metric["joined"].(string)
		if !ok || !strings.HasPrefix(joined, "clickhouse/") {
			t.Fatalf("expected joined label to start with clickhouse/, got %#v", metric)
		}
	}
}
