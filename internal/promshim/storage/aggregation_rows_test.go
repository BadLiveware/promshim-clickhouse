package storage

import (
	"strings"
	"testing"

	"ch-observability/internal/promshim/native/sqlb"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildRangeAggregationOverRowsSubquerySQLAvoidsTimeSeriesFlattening(t *testing.T) {
	sourceSQL := "SELECT tags, timestamp, value FROM rate_rows"
	sql, _, err := BuildRangeAggregationOverRowsSubquerySQL(sourceSQL, nil, parser.SUM, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected row-oriented range aggregation SQL, got error: %v", err)
	}
	checks := []string{
		"arraySort(tag -> tag.1, arrayFilter(tag -> has(['job'], tag.1), tags)) AS grouping_tags",
		"GROUP BY grouping_tags, timestamp",
		"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if strings.Contains(sql, "ARRAY JOIN time_series AS point") {
		t.Fatalf("expected row-oriented range aggregation to avoid ARRAY JOIN time_series, got %q", sql)
	}
}
