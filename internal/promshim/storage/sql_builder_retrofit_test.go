package storage

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/native/sqlb"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildInstantQuerySQLUsesSQLBuilderContract(t *testing.T) {
	sql, params := BuildInstantQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, "up", 123456)
	expectedSQL := "SELECT * FROM prometheusQuery( {database:String}, {table:String}, {promql:String}, fromUnixTimestamp64Milli({evaluation_ms:Int64}) ) SETTINGS allow_experimental_time_series_table = 1 FORMAT JSONEachRow"
	if sqlb.NormalizeSQL(sql) != expectedSQL {
		t.Fatalf("unexpected SQL:\nwant: %s\n got: %s", expectedSQL, sqlb.NormalizeSQL(sql))
	}
	expectedParams := map[string]string{
		"param_database":      "observability",
		"param_table":         "prometheus",
		"param_promql":        "up",
		"param_evaluation_ms": "123456",
	}
	if !reflect.DeepEqual(params, expectedParams) {
		t.Fatalf("unexpected params: want %#v got %#v", expectedParams, params)
	}
}

func TestBuildRangeQuerySQLUsesSQLBuilderContract(t *testing.T) {
	sql, params := BuildRangeQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, "up", 1000, 5000, 60)
	expectedSQL := "SELECT * FROM prometheusQueryRange( {database:String}, {table:String}, {promql:String}, fromUnixTimestamp64Milli({start_ms:Int64}), fromUnixTimestamp64Milli({end_ms:Int64}), toDecimal64({step_ms:Int64}, 3) / 1000 ) SETTINGS allow_experimental_time_series_table = 1 FORMAT JSONEachRow"
	if sqlb.NormalizeSQL(sql) != expectedSQL {
		t.Fatalf("unexpected SQL:\nwant: %s\n got: %s", expectedSQL, sqlb.NormalizeSQL(sql))
	}
	expectedParams := map[string]string{
		"param_database": "observability",
		"param_table":    "prometheus",
		"param_promql":   "up",
		"param_start_ms": "1000",
		"param_end_ms":   "5000",
		"param_step_ms":  "60",
	}
	if !reflect.DeepEqual(params, expectedParams) {
		t.Fatalf("unexpected params: want %#v got %#v", expectedParams, params)
	}
}

func TestBuildInstantAggregationQuerySQLWithBoundsCompilesSourceTemplatesWithoutPlaceholders(t *testing.T) {
	source := AggregationSource{
		Selector:  &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", NeedTags: true, RequireFullTags: true, LookbackMS: int64((5 * time.Minute) / time.Millisecond)},
		ValueExpr: "({value}) * 100",
		TagsExpr:  "arrayFilter(tag -> tag.1 != '__name__', {tags})",
	}
	sql, params, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.SUM, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected instant aggregation SQL, got error: %v", err)
	}
	if strings.Contains(sql, "{value}") || strings.Contains(sql, "{tags}") {
		t.Fatalf("expected compiled aggregation templates, got %q", sql)
	}
	checks := []string{
		"arrayFilter(tag -> tag.1 != '__name__', tags) AS tags",
		"arraySort(tag -> tag.1, arrayFilter(tag -> has(['job'], tag.1), tags)) AS grouping_tags",
		"(value) * 100",
		"if(countIf(isNaN(value)) > 0, CAST(NULL, 'Nullable(Float64)'), sum(value)) AS value",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if params["param_instant_matcher_0_value"] != "up" {
		t.Fatalf("expected existing selector param naming, got %#v", params)
	}
}

func TestBuildRangeAggregationQuerySQLWithBoundsCompilesSourceTemplatesWithoutPlaceholders(t *testing.T) {
	source := AggregationSource{
		Selector:  &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", NeedTags: true, RequireFullTags: true, LookbackMS: int64((5 * time.Minute) / time.Millisecond)},
		ValueExpr: "({value}) * 100",
		TagsExpr:  "arrayFilter(tag -> tag.1 != '__name__', {tags})",
	}
	sql, params, err := BuildRangeAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 0, 300000, 30000, -90000, 300000, parser.SUM, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected range aggregation SQL, got error: %v", err)
	}
	if strings.Contains(sql, "{value}") || strings.Contains(sql, "{tags}") {
		t.Fatalf("expected compiled aggregation templates, got %q", sql)
	}
	checks := []string{
		"arrayFilter(tag -> tag.1 != '__name__', tags) AS tags",
		"arraySort(tag -> tag.1, arrayFilter(tag -> has(['job'], tag.1), tags)) AS grouping_tags",
		"arrayMap(point -> (point.1, (point.2) * 100), time_series) AS time_series",
		"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if params["param_range_instant_matcher_0_value"] != "up" {
		t.Fatalf("expected existing selector param naming, got %#v", params)
	}
}

func TestBuildInstantAggregationQuerySQLWithBoundsSupportsTier1Reducers(t *testing.T) {
	source := AggregationSource{
		Selector:  &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", NeedTags: true, RequireFullTags: true, LookbackMS: int64((5 * time.Minute) / time.Millisecond)},
		ValueExpr: "{value}",
		TagsExpr:  "{tags}",
	}
	stddevSQL, _, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.STDDEV, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected stddev aggregation SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(stddevSQL), sqlb.NormalizeSQL("stddevPop(value)")) {
		t.Fatalf("expected stddev SQL, got %s", sqlb.NormalizeSQL(stddevSQL))
	}
	stdvarSQL, _, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.STDVAR, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected stdvar aggregation SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(stdvarSQL), sqlb.NormalizeSQL("varPop(value)")) {
		t.Fatalf("expected stdvar SQL, got %s", sqlb.NormalizeSQL(stdvarSQL))
	}
	quantile := 0.9
	quantileSQL, _, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.QUANTILE, []string{"job"}, false, &quantile, "")
	if err != nil {
		t.Fatalf("expected quantile aggregation SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(quantileSQL), sqlb.NormalizeSQL("quantile(0.9)(value)")) {
		t.Fatalf("expected quantile SQL, got %s", sqlb.NormalizeSQL(quantileSQL))
	}
	groupSQL, _, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.GROUP, []string{"job"}, false, nil, "")
	if err != nil {
		t.Fatalf("expected group aggregation SQL, got error: %v", err)
	}
	if !strings.Contains(sqlb.NormalizeSQL(groupSQL), sqlb.NormalizeSQL("toFloat64(1) AS value")) {
		t.Fatalf("expected group SQL, got %s", sqlb.NormalizeSQL(groupSQL))
	}
	topk := 3.0
	topkSQL, _, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.TOPK, []string{"job"}, false, &topk, "")
	if err != nil {
		t.Fatalf("expected topk aggregation SQL, got error: %v", err)
	}
	for _, check := range []string{"row_number() OVER (PARTITION BY grouping_tags ORDER BY isNaN(value) ASC, value DESC, tags ASC)", "WHERE rank <= 3", "SELECT tags AS tags, timestamp AS timestamp, value AS value"} {
		if !strings.Contains(sqlb.NormalizeSQL(topkSQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected topk SQL to contain %q, got %s", check, sqlb.NormalizeSQL(topkSQL))
		}
	}
	countValuesSQL, _, err := BuildInstantAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 2000, 1000, 2000, parser.COUNT_VALUES, []string{"job"}, false, nil, "sample_value")
	if err != nil {
		t.Fatalf("expected count_values aggregation SQL, got error: %v", err)
	}
	for _, check := range []string{"tuple('sample_value',", "tag -> tag.1 != '__name__' AND tag.1 != 'sample_value'", "toFloat64(count()) AS value", "GROUP BY grouping_tags"} {
		if !strings.Contains(sqlb.NormalizeSQL(countValuesSQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected count_values SQL to contain %q, got %s", check, sqlb.NormalizeSQL(countValuesSQL))
		}
	}
}

func TestBuildRangeAggregationQuerySQLWithBoundsSupportsTopKSelection(t *testing.T) {
	source := AggregationSource{
		Selector:  &SelectorSource{Kind: SelectorKindInstantVector, MetricName: "up", NeedTags: true, RequireFullTags: true, LookbackMS: int64((5 * time.Minute) / time.Millisecond)},
		ValueExpr: "{value}",
		TagsExpr:  "{tags}",
	}
	topk := 2.0
	sql, _, err := BuildRangeAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 0, 300000, 30000, -90000, 300000, parser.TOPK, []string{"job"}, false, &topk, "")
	if err != nil {
		t.Fatalf("expected range topk aggregation SQL, got error: %v", err)
	}
	for _, check := range []string{"row_number() OVER (PARTITION BY grouping_tags, timestamp ORDER BY isNaN(value) ASC, value DESC, tags ASC)", "WHERE rank <= 2", "arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series", "GROUP BY tags ORDER BY tags"} {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected range topk SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	countValuesSQL, _, err := BuildRangeAggregationQuerySQLWithBounds(QueryConfig{Database: "observability", Table: "prometheus"}, source, 0, 300000, 30000, -90000, 300000, parser.COUNT_VALUES, []string{"job"}, false, nil, "sample_value")
	if err != nil {
		t.Fatalf("expected range count_values aggregation SQL, got error: %v", err)
	}
	for _, check := range []string{"tuple('sample_value',", "tag -> tag.1 != '__name__' AND tag.1 != 'sample_value'", "toFloat64(count()) AS value", "GROUP BY grouping_tags, timestamp"} {
		if !strings.Contains(sqlb.NormalizeSQL(countValuesSQL), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected range count_values SQL to contain %q, got %s", check, sqlb.NormalizeSQL(countValuesSQL))
		}
	}
}

func TestBuildLabelsQueryUsesSQLBuilderContract(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/labels?match[]=up", nil)
	sql, params, err := BuildLabelsQuery(QueryConfig{Database: "observability", Table: "prometheus"}, request)
	if err != nil {
		t.Fatalf("expected labels SQL, got error: %v", err)
	}
	checks := []string{
		"arrayJoin(arrayMap(tag -> tag.1, series_tags)) AS label",
		"GROUP BY label",
		"ORDER BY label",
		"FORMAT JSONEachRow",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if params["param_selector_0_matcher_0_value"] != "up" {
		t.Fatalf("expected selector params, got %#v", params)
	}
}

func TestBuildLabelValuesQueryUsesSQLBuilderContract(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/label/job/values?match[]=up", nil)
	sql, params, err := BuildLabelValuesQuery(QueryConfig{Database: "observability", Table: "prometheus"}, request, "job")
	if err != nil {
		t.Fatalf("expected label values SQL, got error: %v", err)
	}
	checks := []string{
		"arrayJoin(series_tags) AS tag",
		"WHERE tag.1 = {label_name:String}",
		"GROUP BY tag.2",
		"ORDER BY value",
		"FORMAT JSONEachRow",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if params["param_label_name"] != "job" || params["param_selector_0_matcher_0_value"] != "up" {
		t.Fatalf("expected label name and selector params, got %#v", params)
	}
}

func TestBuildSeriesQueryUsesSQLBuilderContract(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/series?match[]=up", nil)
	sql, params, err := BuildSeriesQuery(QueryConfig{Database: "observability", Table: "prometheus"}, request)
	if err != nil {
		t.Fatalf("expected series SQL, got error: %v", err)
	}
	checks := []string{
		"series_tags AS tags",
		"GROUP BY series_tags",
		"FORMAT JSONEachRow",
	}
	for _, check := range checks {
		if !strings.Contains(sqlb.NormalizeSQL(sql), sqlb.NormalizeSQL(check)) {
			t.Fatalf("expected SQL to contain %q, got %s", check, sqlb.NormalizeSQL(sql))
		}
	}
	if params["param_selector_0_matcher_0_value"] != "up" {
		t.Fatalf("expected selector params, got %#v", params)
	}
}
