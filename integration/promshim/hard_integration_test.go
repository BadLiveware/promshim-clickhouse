package promshim_test

import (
	"fmt"
	"net/url"
	"testing"
)

func TestHardVectorMatchingGroupLeftQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20*%20on(job)%20group_left%20sum%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected vector-matching group_left rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ label after arithmetic vector matching, got %#v", metric)
		}
		if _, ok := metric["job"]; !ok {
			t.Fatalf("expected job label in vector matching output, got %#v", metric)
		}
	}
}

func TestHardVectorMatchingGroupRightQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum%20by%20(job)%20(up)%20*%20on(job)%20group_right%20up")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected vector-matching group_right rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ label after arithmetic vector matching, got %#v", metric)
		}
		if _, ok := metric["job"]; !ok {
			t.Fatalf("expected job label in vector matching output, got %#v", metric)
		}
	}
}

func TestHardVectorMatchingGroupLeftRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=up%20*%20on(job)%20group_left%20sum%20by%20(job)%20(up)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected vector-matching group_left range rows")
	}
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["__name__"]; ok {
			t.Fatalf("did not expect __name__ label after arithmetic vector matching range query, got %#v", metric)
		}
	}
}

func TestHardVectorMatchingFillRightQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20%2B%20on(job,instance,namespace,pod,service)%20fill_right(0)%20up%7Bjob%3D%22__definitely_missing__%22%7D")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected fill_right rows")
	}
}

func TestHardVectorMatchingFillLeftQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%7Bjob%3D%22__definitely_missing__%22%7D%20%2B%20on(job,instance,namespace,pod,service)%20fill_left(0)%20up")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected fill_left rows")
	}
}

func TestHardVectorMatchingRejectsImplicitManyToOne(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20*%20on(job)%20sum%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "error")
	errorText, _ := payload["error"].(string)
	if !contains(errorText, "many-to-one matching must be explicit") {
		t.Fatalf("expected explicit many-to-one cardinality error, got %#v", payload)
	}
}

func TestHardSetOperatorAndQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20and%20on(job)%20sum%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected set-and rows")
	}
}

func TestHardSetOperatorOrQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%7Bjob%3D%22clickhouse%22%7D%20or%20on(job)%20sum%20by%20(job)%20(up)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected set-or rows")
	}
}

func TestHardSetOperatorUnlessQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20unless%20on(job)%20sum%20by%20(job)%20(up%7Bjob%3D%22definitely_missing%22%7D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected set-unless rows")
	}
}

func TestHardOffsetModifierQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20offset%205m")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected offset-modified rows")
	}
}

func TestHardAtModifierRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=up%20%40%20start()&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected @ start() range rows")
	}
}

func TestHardSubqueryRateLikeFunctionsQuery(t *testing.T) {
	f := requireFixture(t)
	for _, fn := range []string{"rate", "irate", "increase", "delta", "idelta", "deriv", "changes"} {
		query := fmt.Sprintf("%s(coredns_dns_request_size_bytes_count[5m:30s])", fn)
		payload, err := f.getJSON("/api/v1/query?query=" + url.QueryEscape(query))
		if err != nil {
			t.Fatal(err)
		}
		assertUnsupportedContains(t, payload, "function \""+fn+"\" with subquery arguments")
	}
}

func TestHardSubqueryRateLikeFunctionsWrappedInAggregateQuery(t *testing.T) {
	f := requireFixture(t)
	for _, fn := range []string{"rate", "irate", "increase", "delta", "idelta", "deriv", "changes"} {
		query := fmt.Sprintf("sum(%s(coredns_dns_request_size_bytes_count[5m:30s]))", fn)
		payload, err := f.getJSON("/api/v1/query?query=" + url.QueryEscape(query))
		if err != nil {
			t.Fatal(err)
		}
		assertUnsupportedContains(t, payload, "function \""+fn+"\" with subquery arguments")
	}
}

func TestHardSubqueryLocalChildMatrixQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=(up%20*%20100)%5B5m%3A30s%5D")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected local-child subquery matrix rows")
	}
}

func TestHardSubqueryLocalAggregationChildMatrixQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum(up)%5B5m%3A30s%5D")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected local-aggregation-child subquery matrix rows")
	}
}

func TestHardQueryRangeRejectsMatrixExpressionType(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=(up%20*%20100)%5B5m%3A30s%5D&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, payload["status"], "error")
	errorText, _ := payload["error"].(string)
	if !contains(errorText, "invalid expression type") || !contains(errorText, "range query") {
		t.Fatalf("expected range-query matrix type error, got %#v", payload)
	}
}

func TestHardQueryRangeRejectsRateLikeFunctionsWithSubqueryArgs(t *testing.T) {
	f := requireFixture(t)
	for _, fn := range []string{"rate", "irate", "increase", "delta", "idelta", "deriv", "changes"} {
		query := fmt.Sprintf("%s(coredns_dns_request_size_bytes_count[5m:30s])", fn)
		payload, err := f.getJSON("/api/v1/query_range?query=" + url.QueryEscape(query) + "&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
		if err != nil {
			t.Fatal(err)
		}
		assertUnsupportedContains(t, payload, "function \""+fn+"\" with subquery arguments")
	}
}

func TestHardQueryRangeRejectsRateLikeFunctionsWrappedInAggregateWithSubqueryArgs(t *testing.T) {
	f := requireFixture(t)
	for _, fn := range []string{"rate", "irate", "increase", "delta", "idelta", "deriv", "changes"} {
		query := fmt.Sprintf("sum(%s(coredns_dns_request_size_bytes_count[5m:30s]))", fn)
		payload, err := f.getJSON("/api/v1/query_range?query=" + url.QueryEscape(query) + "&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
		if err != nil {
			t.Fatal(err)
		}
		assertUnsupportedContains(t, payload, "function \""+fn+"\" with subquery arguments")
	}
}

func TestHardLastOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=last_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected last_over_time instant rows")
	}
}

func TestHardLastOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=last_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected last_over_time range rows")
	}
}

func TestHardSumOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected sum_over_time instant rows")
	}
}

func TestHardSumOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=sum_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected sum_over_time range rows")
	}
}

func TestHardAvgOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=avg_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected avg_over_time instant rows")
	}
}

func TestHardAvgOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=avg_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected avg_over_time range rows")
	}
}

func TestHardMaxOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=max_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected max_over_time instant rows")
	}
}

func TestHardMaxOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=max_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected max_over_time range rows")
	}
}

func TestHardMinOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=min_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected min_over_time instant rows")
	}
}

func TestHardMinOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=min_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected min_over_time range rows")
	}
}

func TestHardCountOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=count_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected count_over_time instant rows")
	}
}

func TestHardCountOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=count_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected count_over_time range rows")
	}
}

func TestHardQuantileOverTimeWithLocalSubqueryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=quantile_over_time(0.9,%20(up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected quantile_over_time instant rows")
	}
}

func TestHardQuantileOverTimeWithLocalSubqueryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=quantile_over_time(0.9,%20(up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected quantile_over_time range rows")
	}
}

func TestHardNestedSubqueryViaLastOverTimeInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=last_over_time(last_over_time((up%20*%20100)%5B5m%3A30s%5D)%5B10m%3A1m%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected nested subquery instant rows")
	}
}

func TestHardNestedSubqueryViaLastOverTimeRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=last_over_time(last_over_time((up%20*%20100)%5B5m%3A30s%5D)%5B10m%3A1m%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected nested subquery range rows")
	}
}

func TestHardNestedMatrixFunctionBinaryInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum_over_time((up%20*%20100)%5B5m%3A30s%5D)%20%2B%20count_over_time((up%20*%20100)%5B5m%3A30s%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected nested matrix-function binary instant rows")
	}
}

func TestHardNestedMatrixFunctionBinaryRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=sum_over_time((up%20*%20100)%5B5m%3A30s%5D)%20%2B%20count_over_time((up%20*%20100)%5B5m%3A30s%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected nested matrix-function binary range rows")
	}
}

func TestHardNestedQuantileOverTimeInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=quantile_over_time(0.9,%20sum_over_time((up%20*%20100)%5B5m%3A30s%5D)%5B10m%3A1m%5D)")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected nested quantile_over_time instant rows")
	}
}

func TestHardNestedQuantileOverTimeRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=quantile_over_time(0.9,%20sum_over_time((up%20*%20100)%5B5m%3A30s%5D)%5B10m%3A1m%5D)&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected nested quantile_over_time range rows")
	}
}

func TestHardSubqueryLocalChildAtStartOffsetQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=(up%20*%20100)%5B5m%3A30s%5D%20%40%20start()%20offset%201m")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected local-child subquery @start()+offset matrix rows")
	}
}

func TestHardSubqueryLocalAggregationChildAtStartOffsetQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=sum(up)%5B5m%3A30s%5D%20%40%20start()%20offset%201m")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected local-aggregation-child subquery @start()+offset matrix rows")
	}
}

func TestHardSubqueryMatrixRootSelectorInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=coredns_dns_request_size_bytes_count%5B5m%3A30s%5D")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	if len(rows) == 0 {
		t.Fatal("expected matrix-root subquery rows")
	}
}

func TestHardHistogramQuantileClassicBucketInstantQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=histogram_quantile(0.9,%20sum%20by%20(le,job)%20(rate(coredns_dns_request_size_bytes_bucket%5B5m%5D)))")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireVectorRows(t, payload)
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["le"]; ok {
			t.Fatalf("did not expect le label in histogram_quantile output, got %#v", metric)
		}
	}
}

func TestHardHistogramQuantileClassicBucketRangeQuery(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query_range?query=histogram_quantile(0.9,%20sum%20by%20(le,job)%20(rate(coredns_dns_request_size_bytes_bucket%5B5m%5D)))&start=2026-04-20T11:33:00Z&end=2026-04-20T11:35:00Z&step=30s")
	if err != nil {
		t.Fatal(err)
	}
	rows := requireMatrixRows(t, payload)
	for _, row := range rows {
		metric := row.(map[string]any)["metric"].(map[string]any)
		if _, ok := metric["le"]; ok {
			t.Fatalf("did not expect le label in histogram_quantile range output, got %#v", metric)
		}
	}
}
