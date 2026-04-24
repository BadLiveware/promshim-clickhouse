package storage

import (
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildRangeAggregationRowsSubquerySQLKeepsRowShape(t *testing.T) {
	sourceSQL := "SELECT tags, timestamp, value FROM rate_rows"
	sql, _, err := BuildRangeAggregationRowsSubquerySQL(sourceSQL, nil, parser.SUM, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected row-oriented range aggregation rows SQL, got error: %v", err)
	}
	checks := []string{
		"arrayFilter(tag -> has(['job'], tag.1), tags) AS tags",
		"GROUP BY tags, timestamp",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if strings.Contains(sql, "SELECT grouping_tags AS tags") {
		t.Fatalf("expected row aggregation rows SQL to avoid the redundant grouping_tags wrapper subquery, got %q", sql)
	}
	if strings.Contains(sql, "ORDER BY grouping_tags") || strings.Contains(sql, "ORDER BY tags") {
		t.Fatalf("expected row aggregation rows SQL to avoid unnecessary row ordering, got %q", sql)
	}
	if strings.Contains(sql, "ARRAY JOIN time_series AS point") || strings.Contains(sql, "groupArray((timestamp, value))") {
		t.Fatalf("expected row aggregation rows SQL to stay row-oriented, got %q", sql)
	}
	if got := strings.Count(sql, schema.FormatLine); got != 1 {
		t.Fatalf("expected exactly one trailing FORMAT clause after trimming nested row source SQL, got count=%d sql=%q", got, sql)
	}
}

func TestBuildRangeRowsToMatrixSubquerySQLWrapsRowsWithoutReaggregation(t *testing.T) {
	sourceSQL := "SELECT tags, timestamp, value FROM grouped_rows"
	sql, _, err := BuildRangeRowsToMatrixSubquerySQL(sourceSQL, nil)
	if err != nil {
		t.Fatalf("expected row-to-matrix SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL("arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series")) {
		t.Fatalf("expected row-to-matrix SQL to materialize time_series, got %s", sqlb.NormalizeSQL(sql))
	}
	if strings.Contains(sql, "GROUP BY grouping_tags, timestamp") {
		t.Fatalf("expected row-to-matrix wrapper to avoid re-aggregating grouped rows, got %q", sql)
	}
}

func TestBuildInstantAggregationRowsSubquerySQLUsesEvaluationTimestamp(t *testing.T) {
	source := AggregationSource{ValueExpr: "{value}", TagsExpr: "{tags}"}
	sourceSQL := "SELECT tags, timestamp, value FROM instant_rows"
	sql, _, err := BuildInstantAggregationRowsSubquerySQL(source, sourceSQL, nil, 123456, parser.SUM, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected row-oriented instant aggregation rows SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL("fromUnixTimestamp64Milli(123456) AS timestamp")) {
		t.Fatalf("expected instant row aggregation SQL to stamp the evaluation time directly, got %s", sqlb.NormalizeSQL(sql))
	}
	if strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL("max(timestamp) AS timestamp")) {
		t.Fatalf("expected instant row aggregation SQL to avoid redundant max(timestamp), got %s", sqlb.NormalizeSQL(sql))
	}
	if strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL("SELECT tags AS tags, timestamp AS timestamp, value AS value FROM (")) {
		t.Fatalf("expected instant row aggregation SQL to avoid the identity source wrapper subquery, got %s", sqlb.NormalizeSQL(sql))
	}
}

func TestBuildRangeAggregationOverRowsSubquerySQLAvoidsTimeSeriesFlattening(t *testing.T) {
	sourceSQL := "SELECT tags, timestamp, value FROM rate_rows"
	sql, _, err := BuildRangeAggregationOverRowsSubquerySQL(sourceSQL, nil, parser.SUM, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected row-oriented range aggregation SQL, got error: %v", err)
	}
	checks := []string{
		"arrayFilter(tag -> has(['job'], tag.1), tags) AS tags",
		"GROUP BY tags, timestamp",
		"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if got := strings.Count(sql, "GROUP BY tags, timestamp"); got != 1 {
		t.Fatalf("expected only one aggregation over tags,timestamp, got count=%d sql=%q", got, sql)
	}
	if strings.Contains(sql, "ARRAY JOIN time_series AS point") {
		t.Fatalf("expected row-oriented range aggregation to avoid ARRAY JOIN time_series, got %q", sql)
	}
	if got := strings.Count(sql, schema.FormatLine); got != 1 {
		t.Fatalf("expected exactly one trailing FORMAT clause after trimming nested row source SQL, got count=%d sql=%q", got, sql)
	}
}
