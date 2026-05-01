package storage

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildCappedSelectorStatsQueryLimitsMatchedSeriesProbe(t *testing.T) {
	sql, params, err := BuildCappedSelectorStatsQuery(QueryConfig{Database: "observability", Table: "prometheus"}, SelectorSource{Kind: SelectorKindRangeVector, MetricName: "up"}, 100000, 200000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"count() AS matched_series", "timeSeriesTags(`observability`.`prometheus`)", "LIMIT 1001", "FORMAT JSONEachRow"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("SQL missing %q:\n%s", fragment, sql)
		}
	}
	if params["param_required_start_ms"] != "100000" || params["param_required_end_ms"] != "200000" {
		t.Fatalf("missing time params: %+v", params)
	}
}

func TestBuildSelectorStatsQueryUsesTimeSeriesTagsAndTimeOverlap(t *testing.T) {
	req := httptest.NewRequest("GET", "/?match[]=up%7Bjob%3D%22api%22%7D&start=100&end=200", nil)
	sql, params, err := BuildSelectorStatsQuery(QueryConfig{Database: "observability", Table: "prometheus"}, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"count() AS matched_series", "timeSeriesTags(`observability`.`prometheus`)", "max_time >=", "min_time <=", "FORMAT JSONEachRow"} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("SQL missing %q:\n%s", fragment, sql)
		}
	}
	if params["param_start_ms"] == "" || params["param_end_ms"] == "" {
		t.Fatalf("missing time params: %+v", params)
	}
}
