package storage

import (
	"strings"
	"testing"
)

func TestBuildDenseRateRollupRangeQuerySQL(t *testing.T) {
	sql, params := BuildDenseRateRollupRangeQuerySQL(QueryConfig{Database: "observability"}, 1000, 2000)
	for _, expected := range []string{
		"FROM `observability`.`rollup_cpu_rate_5m_1m_by_job`",
		"metric_name = {metric_name:String}",
		"timestamp >= fromUnixTimestamp64Milli({start_ms:Int64})",
		"timestamp <= fromUnixTimestamp64Milli({end_ms:Int64})",
		"GROUP BY job",
		"ORDER BY tags",
		"FORMAT JSONEachRow",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected rollup SQL to contain %q, got %q", expected, sql)
		}
	}
	if params["param_metric_name"] != "demo_cpu_usage_seconds_total" || params["param_start_ms"] != "1000" || params["param_end_ms"] != "2000" {
		t.Fatalf("unexpected params: %#v", params)
	}
}
