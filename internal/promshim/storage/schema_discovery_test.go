package storage

import (
	"strings"
	"testing"
)

func TestBuildPromotedTagColumnsDiscoverySQLQueriesSystemColumns(t *testing.T) {
	sql := BuildPromotedTagColumnsDiscoverySQL(QueryConfig{Database: "observability", Table: "prometheus"})
	for _, expected := range []string{
		"SELECT c.name AS value FROM system.columns AS c",
		"INNER JOIN system.tables AS t ON t.database = c.database AND t.name = c.table",
		"c.database = 'observability'",
		"c.table = 'prometheus'",
		"c.name NOT IN ('all_tags', 'help', 'id', 'max_time', 'metric_family_name', 'metric_name', 'min_time', 'tags', 'timestamp', 'type', 'unit', 'value')",
		"match(t.engine_full, concat('''', regexpQuoteMeta(c.name), '''\\\\s*:\\\\s*''', regexpQuoteMeta(c.name), ''''))",
		"ORDER BY c.name",
		"FORMAT JSONEachRow",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected discovery SQL to contain %q, got %q", expected, sql)
		}
	}
	if strings.Contains(sql, "DESCRIBE TABLE") || strings.Contains(sql, "timeSeriesTags") {
		t.Fatalf("discovery SQL should avoid DESCRIBE subqueries, got %q", sql)
	}
}

func TestBuildTimeSeriesIDTypeDiscoverySQLQueriesSystemColumns(t *testing.T) {
	sql := BuildTimeSeriesIDTypeDiscoverySQL(QueryConfig{Database: "observability", Table: "prometheus"})
	for _, expected := range []string{
		"SELECT type AS value FROM system.columns",
		"database = 'observability'",
		"table = 'prometheus'",
		"WHERE",
		"name = 'id'",
		"LIMIT 1",
		"FORMAT JSONEachRow",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected id type discovery SQL to contain %q, got %q", expected, sql)
		}
	}
	if strings.Contains(sql, "DESCRIBE TABLE") || strings.Contains(sql, "timeSeriesData") {
		t.Fatalf("id type discovery SQL should avoid DESCRIBE subqueries, got %q", sql)
	}
}

func TestPromotedTagColumnSetFromNamesFiltersSystemColumns(t *testing.T) {
	got := promotedTagColumnSetFromNames([]string{"id", "timestamp", "value", "metric_name", "tags", "all_tags", "min_time", "max_time", "metric_family_name", "type", "unit", "help", "instance", "", "pod"})
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
