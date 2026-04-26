package storage

import (
	"strings"
	"testing"
)

func TestBuildPromotedTagColumnsDiscoverySQLDescribesTagsFunction(t *testing.T) {
	sql := BuildPromotedTagColumnsDiscoverySQL(QueryConfig{Database: "observability", Table: "prometheus"})
	for _, expected := range []string{
		"DESCRIBE TABLE timeSeriesTags(`observability`.`prometheus`)",
		"name NOT IN ('id', 'metric_name', 'tags', 'min_time', 'max_time')",
		"ORDER BY name",
		"SETTINGS allow_experimental_time_series_table = 1",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected discovery SQL to contain %q, got %q", expected, sql)
		}
	}
}

func TestBuildTimeSeriesIDTypeDiscoverySQLDescribesDataFunction(t *testing.T) {
	sql := BuildTimeSeriesIDTypeDiscoverySQL(QueryConfig{Database: "observability", Table: "prometheus"})
	for _, expected := range []string{
		"DESCRIBE TABLE timeSeriesData(`observability`.`prometheus`)",
		"WHERE name = 'id'",
		"LIMIT 1",
		"SETTINGS allow_experimental_time_series_table = 1",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected id type discovery SQL to contain %q, got %q", expected, sql)
		}
	}
}

func TestBuildDenseRateRollupDiscoverySQLReadsSystemColumns(t *testing.T) {
	sql := BuildDenseRateRollupDiscoverySQL()
	for _, expected := range []string{
		"FROM system.columns",
		"database = {database:String}",
		"table = {rollup_table:String}",
		"name IN ('job', 'metric_name', 'timestamp', 'value')",
		"ORDER BY name",
		"SETTINGS allow_experimental_time_series_table = 1",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected rollup discovery SQL to contain %q, got %q", expected, sql)
		}
	}
}

func TestDenseRateRollupDiscoveryFromColumnsRequiresContract(t *testing.T) {
	available := denseRateRollupDiscoveryFromColumns([]string{"timestamp", "value", "job", "metric_name"})
	if !available.Available {
		t.Fatalf("expected rollup to be available: %#v", available)
	}
	if len(available.MissingColumns) != 0 {
		t.Fatalf("expected no missing columns, got %#v", available.MissingColumns)
	}

	missing := denseRateRollupDiscoveryFromColumns([]string{"timestamp", "value"})
	if missing.Available {
		t.Fatalf("expected rollup to be unavailable: %#v", missing)
	}
	if strings.Join(missing.MissingColumns, ",") != "job,metric_name" {
		t.Fatalf("unexpected missing columns: %#v", missing.MissingColumns)
	}
}

func TestPromotedTagColumnSetFromNamesFiltersSystemColumns(t *testing.T) {
	got := promotedTagColumnSetFromNames([]string{"id", "instance", "", "tags", "pod", "metric_name"})
	if len(got) != 2 {
		t.Fatalf("expected two promoted columns, got %#v", got)
	}
	if _, ok := got["instance"]; !ok {
		t.Fatalf("expected instance column in %#v", got)
	}
	if _, ok := got["pod"]; !ok {
		t.Fatalf("expected pod column in %#v", got)
	}
}

func TestSortedPromotedTagColumnNames(t *testing.T) {
	got := SortedPromotedTagColumnNames(map[string]struct{}{"pod": {}, "instance": {}})
	if len(got) != 2 || got[0] != "instance" || got[1] != "pod" {
		t.Fatalf("unexpected sorted names: %#v", got)
	}
}
