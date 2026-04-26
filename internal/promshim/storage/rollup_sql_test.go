package storage

import (
	"strings"
	"testing"
)

func TestBuildDenseRateRollupCoverageSQL(t *testing.T) {
	sql, params := BuildDenseRateRollupCoverageSQL(QueryConfig{Database: "observability"})
	for _, expected := range []string{
		"FROM `observability`.`rollup_cpu_rate_5m_1m_by_job`",
		"count() = 0",
		"toUnixTimestamp64Milli(min(timestamp))",
		"toUnixTimestamp64Milli(max(timestamp))",
		"metric_name = {metric_name:String}",
		"FORMAT JSONEachRow",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected coverage SQL to contain %q, got %q", expected, sql)
		}
	}
	if params["param_metric_name"] != "demo_cpu_usage_seconds_total" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestDenseRateRollupCoverageCovers(t *testing.T) {
	coverage := DenseRateRollupCoverage{HasRows: true, MinMS: 1000, MaxMS: 5000}
	if !coverage.Covers(1000, 5000) || !coverage.Covers(2000, 4000) {
		t.Fatalf("expected coverage to cover contained ranges")
	}
	if coverage.Covers(999, 4000) || coverage.Covers(2000, 5001) {
		t.Fatalf("expected coverage to reject partial ranges")
	}
	if (DenseRateRollupCoverage{}).Covers(1000, 1000) {
		t.Fatalf("empty coverage must not cover")
	}
}

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
