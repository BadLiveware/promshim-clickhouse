package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/prometheus/prometheus/model/labels"
)

func TestBuildInstantSelectorQuerySQLCompilesMatchersAndBounds(t *testing.T) {
	jobRE, err := labels.NewMatcher(labels.MatchRegexp, "job", "api|worker")
	if err != nil {
		t.Fatal(err)
	}
	namespaceNEQ, err := labels.NewMatcher(labels.MatchNotEqual, "namespace", "dev")
	if err != nil {
		t.Fatal(err)
	}
	selector := selectorSourceFromMatchers("up", []*labels.Matcher{jobRE, namespaceNEQ}, 5*time.Minute, 0, SelectorKindInstantVector)

	sql, params, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, 1000, 2000)
	if err != nil {
		t.Fatalf("expected instant selector SQL, got error: %v", err)
	}
	if !strings.Contains(sql, "timeSeriesData(`observability`.`prometheus`)") || !strings.Contains(sql, "timeSeriesTags(`observability`.`prometheus`)") {
		t.Fatalf("expected repo-owned selector sources, got %q", sql)
	}
	for _, expected := range []string{"src.metric_name = {instant_matcher_0_value:String}", "match(src.tags[concat('', {instant_matcher_1_key:String})], {instant_matcher_1_value:String})", "src.tags[concat('', {instant_matcher_2_key:String})] != {instant_matcher_2_value:String}", "fromUnixTimestamp64Milli({required_start_ms:Int64})", "fromUnixTimestamp64Milli({required_end_ms:Int64})"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_instant_matcher_0_value"] != "up" || params["param_instant_matcher_1_value"] != "^(?:api|worker)$" || params["param_instant_matcher_2_value"] != "dev" {
		t.Fatalf("unexpected matcher params: %#v", params)
	}
}

func TestBuildInstantSelectorQuerySQLAnchorsRegexMatchers(t *testing.T) {
	jobRE, err := labels.NewMatcher(labels.MatchRegexp, "job", "a")
	if err != nil {
		t.Fatal(err)
	}
	envNotRE, err := labels.NewMatcher(labels.MatchNotRegexp, "env", "dev|qa")
	if err != nil {
		t.Fatal(err)
	}
	selector := selectorSourceFromMatchers("up", []*labels.Matcher{jobRE, envNotRE}, 5*time.Minute, 0, SelectorKindInstantVector)

	_, params, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, 1000, 2000)
	if err != nil {
		t.Fatalf("expected instant selector SQL, got error: %v", err)
	}
	if params["param_instant_matcher_1_value"] != "^(?:a)$" {
		t.Fatalf("expected anchored positive regex param, got %#v", params)
	}
	if params["param_instant_matcher_2_value"] != "^(?:dev|qa)$" {
		t.Fatalf("expected anchored negative regex param, got %#v", params)
	}
}

func TestBuildInstantSelectorQuerySQLSupportsEqualityAndNegativeRegex(t *testing.T) {
	instanceEQ, err := labels.NewMatcher(labels.MatchEqual, "instance", "a:9090")
	if err != nil {
		t.Fatal(err)
	}
	envNotRE, err := labels.NewMatcher(labels.MatchNotRegexp, "env", "dev|qa")
	if err != nil {
		t.Fatal(err)
	}
	selector := selectorSourceFromMatchers("", []*labels.Matcher{instanceEQ, envNotRE}, 5*time.Minute, 0, SelectorKindInstantVector)

	sql, params, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, 1000, 2000)
	if err != nil {
		t.Fatalf("expected instant selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"src.tags[concat('', {instant_matcher_0_key:String})] = {instant_matcher_0_value:String}", "NOT match(src.tags[concat('', {instant_matcher_1_key:String})], {instant_matcher_1_value:String})"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_instant_matcher_0_value"] != "a:9090" || params["param_instant_matcher_1_value"] != "^(?:dev|qa)$" {
		t.Fatalf("unexpected matcher params: %#v", params)
	}
}

func TestBuildInstantSelectorQuerySQLMatchesNormalizedBuilderShape(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)

	sql, _, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, 1000, 2000)
	if err != nil {
		t.Fatalf("expected instant selector SQL, got error: %v", err)
	}
	expected := "SELECT series.tags AS tags, max(d.timestamp) AS timestamp, argMax(d.value, d.timestamp) AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN ( SELECT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64}) ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) GROUP BY d.id, series.tags HAVING NOT isNaN(value) ORDER BY tags SETTINGS allow_experimental_time_series_table = 1 FORMAT JSONEachRow"
	if sqlb.NormalizeSQL(sql) != expected {
		t.Fatalf("unexpected normalized SQL:\nwant: %s\n got: %s", expected, sqlb.NormalizeSQL(sql))
	}
}

func TestSelectorTagsExprSkipsSortForSingleRequiredLabel(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)
	selector.RequireFullTags = false
	selector.RequiredTagLabels = []string{"job"}
	got := selectorTagsExpr(QueryConfig{}, selector, "metric_name", "tags")
	if !strings.Contains(got, "if(mapContains(tags, 'job'), [tuple('job', concat('', tags['job']))], CAST([], 'Array(Tuple(String, String))'))") {
		t.Fatalf("expected direct single-label selector tags expr, got %q", got)
	}
	for _, unwanted := range []string{"arrayFilter(", "arraySort(", "arrayConcat([tuple('__name__'", "mapKeys(tags)", "mapValues(tags)"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("expected single-label selector tags expr to avoid %q, got %q", unwanted, got)
		}
	}
}

func TestSelectorTagsExprIncludesPromotedColumnsInFullTags(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)
	got := selectorTagsExpr(QueryConfig{PromotedTagColumns: map[string]struct{}{"instance": {}, "job": {}}}, selector, "src.metric_name", "src.tags")
	for _, expected := range []string{
		"[tuple('__name__', src.metric_name)]",
		"if(src.`instance` != '', [tuple('instance', concat('', src.`instance`))], CAST([], 'Array(Tuple(String, String))'))",
		"if(src.`job` != '', [tuple('job', concat('', src.`job`))], CAST([], 'Array(Tuple(String, String))'))",
		"tag -> NOT has(['instance', 'job'], tag.1)",
		"mapKeys(src.tags)",
		"mapValues(src.tags)",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected full tags expression to contain %q, got %q", expected, got)
		}
	}
}

func TestBuildInstantSelectorQuerySQLUsesPromotedTagColumns(t *testing.T) {
	instanceEQ, err := labels.NewMatcher(labels.MatchEqual, "instance", "a:9090")
	if err != nil {
		t.Fatal(err)
	}
	jobRE, err := labels.NewMatcher(labels.MatchRegexp, "job", "api|worker")
	if err != nil {
		t.Fatal(err)
	}
	selector := selectorSourceFromMatchers("up", []*labels.Matcher{instanceEQ, jobRE}, 5*time.Minute, 0, SelectorKindInstantVector)
	selector.RequireFullTags = false
	selector.RequiredTagLabels = []string{"instance"}
	cfg := QueryConfig{Database: "observability", Table: "prometheus", PromotedTagColumns: map[string]struct{}{"instance": {}, "job": {}}}

	sql, params, err := BuildInstantSelectorQuerySQL(cfg, selector, 1000, 2000)
	if err != nil {
		t.Fatalf("expected instant selector SQL, got error: %v", err)
	}
	for _, expected := range []string{
		"src.`instance` = {instant_matcher_1_value:String}",
		"match(src.`job`, {instant_matcher_2_value:String})",
		"if(src.`instance` != '', [tuple('instance', concat('', src.`instance`))], CAST([], 'Array(Tuple(String, String))')) AS tags",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected promoted tag column SQL to contain %q, got %q", expected, sql)
		}
	}
	for _, unwanted := range []string{"src.tags[concat('', {instant_matcher_1_key:String})]", "src.tags[concat('', {instant_matcher_2_key:String})]", "concat('', src.tags['instance'])"} {
		if strings.Contains(sql, unwanted) {
			t.Fatalf("expected promoted tag column SQL to avoid %q, got %q", unwanted, sql)
		}
	}
	if _, ok := params["param_instant_matcher_1_key"]; ok {
		t.Fatalf("did not expect key param for promoted instance matcher: %#v", params)
	}
	if _, ok := params["param_instant_matcher_2_key"]; ok {
		t.Fatalf("did not expect key param for promoted job matcher: %#v", params)
	}
}

func TestBuildInstantSelectorQuerySQLOmitsTagsProjectionWhenUnneeded(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)
	selector.NeedTags = false
	selector.RequireFullTags = false

	sql, _, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, 1000, 2000)
	if err != nil {
		t.Fatalf("expected instant selector SQL, got error: %v", err)
	}
	if strings.Contains(sql, "arrayConcat([tuple('__name__', metric_name)]") || strings.Contains(sql, "series.tags AS tags") {
		t.Fatalf("expected omitted tag projection, got %q", sql)
	}
	if !strings.Contains(sql, "CAST([], 'Array(Tuple(String, String))') AS tags") {
		t.Fatalf("expected synthesized empty tags, got %q", sql)
	}
}

func TestBuildRangeWindowSelectorQuerySQLUsesStepGridAndRangeWindow(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, time.Minute, SelectorKindRangeVector)

	sql, params, err := BuildRangeWindowSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -360000, 300000, 0, 300000, 30000, "sum_over_time", "arraySum(arrayMap(point -> point.2, window_series))", 0)
	if err != nil {
		t.Fatalf("expected range window selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts", "arraySort(groupArray((d.timestamp, d.value))) AS window_series", "d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64})", "d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})", "GROUP BY grid.id, grid.tags, grid.eval_ts"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_lookback_ms"] != "300000" || params["param_offset_ms"] != "60000" {
		t.Fatalf("expected lookback/offset params, got %#v", params)
	}
}

func TestBuildRangeWindowSelectorQuerySQLUsesInclusiveTemporalBounds(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, time.Minute, SelectorKindRangeVector)

	sql, _, err := BuildRangeWindowSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -360000, 240000, 0, 300000, 30000, "sum_over_time", "arraySum(arrayMap(point -> point.2, window_series))", 0)
	if err != nil {
		t.Fatalf("expected range window selector SQL, got error: %v", err)
	}
	if !strings.Contains(sql, "d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64})") {
		t.Fatalf("expected inclusive right-edge bound in SQL, got %q", sql)
	}
	if !strings.Contains(sql, "d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})") {
		t.Fatalf("expected inclusive left-edge bound in SQL, got %q", sql)
	}
}

func TestBuildRangeWindowSelectorQuerySQLSkipsUnusedRateAliases(t *testing.T) {
	selector := selectorSourceFromMatchers("demo_cpu_usage_seconds_total", nil, 5*time.Minute, 0, SelectorKindRangeVector)

	sql, _, err := BuildRangeWindowSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -300000, 300000, 0, 300000, 30000, "rate", "if(arrayExists(v -> isNaN(v), window_values) OR (window_duration_ms) <= 0, nan, (counter_delta_sum) / (window_duration_ms))", 1)
	if err != nil {
		t.Fatalf("expected rate range window selector SQL, got error: %v", err)
	}
	if strings.Contains(sql, "window_timestamps") {
		t.Fatalf("expected rate SQL to skip unused window_timestamps alias, got %q", sql)
	}
	if strings.Contains(sql, "changes_count") {
		t.Fatalf("expected rate SQL to skip unused changes_count alias, got %q", sql)
	}
	if !strings.Contains(sql, "tupleElement(arrayElement(window_series, length(window_series)), 1) - tupleElement(arrayElement(window_series, 1), 1) AS window_duration_ms") {
		t.Fatalf("expected rate SQL to compute duration directly from window_series, got %q", sql)
	}
}

func TestBuildRangeWindowSelectorDirectAggregateQuerySQLUsesGroupedAvgAliases(t *testing.T) {
	selector := selectorSourceFromMatchers("demo_memory_usage_bytes", nil, 5*time.Minute, 0, SelectorKindRangeVector)

	sql, _, err := BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -300000, 300000, 0, 300000, 30000, "avg_over_time", "", 0)
	if err != nil {
		t.Fatalf("expected direct aggregate range window selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"count() AS sample_count", "countIf(isNaN(ifNull(toFloat64(d.value), nan))) AS nan_count", "countIf(NOT isNaN(ifNull(toFloat64(d.value), nan))) AS finite_count", "avgIf(ifNull(toFloat64(d.value), nan), NOT isNaN(ifNull(toFloat64(d.value), nan))) AS avg_value", "if(nan_count > 0 OR finite_count = 0, nan, avg_value) AS value", "GROUP BY grid.id, grid.eval_ts", "windowed.id = series.id", "tuple('__name__', src.metric_name)"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	for _, unwanted := range []string{"window_series", "window_values", "CROSS JOIN", "GROUP BY grid.id, grid.tags, grid.eval_ts"} {
		if strings.Contains(sql, unwanted) {
			t.Fatalf("expected direct aggregate SQL to avoid %q, got %q", unwanted, sql)
		}
	}
}

func TestBuildRangeWindowSelectorDirectAggregateRowsQuerySQLUsesGroupedRateAliases(t *testing.T) {
	selector := selectorSourceFromMatchers("demo_cpu_usage_seconds_total", nil, time.Hour, 0, SelectorKindRangeVector)

	sql, _, err := BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -3600000, 3600000, 0, 3600000, 14400000, "rate", "", 1)
	if err != nil {
		t.Fatalf("expected direct aggregate rows SQL for rate, got error: %v", err)
	}
	for _, expected := range []string{"count() AS sample_count", "countIf(isNaN(ifNull(toFloat64(d.value), nan))) AS nan_count", "max(d.timestamp) - min(d.timestamp) AS window_duration_ms", "deltaSumTimestamp(ifNull(toFloat64(d.value), nan), toUnixTimestamp64Milli(d.timestamp)) AS counter_delta_sum", "if(nan_count > 0 OR sample_count <= 1 OR window_duration_ms <= 0, nan, counter_delta_sum / window_duration_ms) AS value", "windowed.id = series.id", "GROUP BY grid.id, grid.eval_ts"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	for _, unwanted := range []string{"window_series", "window_values", "arrayPopBack(", "arrayPopFront(", "GROUP BY grid.id, grid.tags, grid.eval_ts"} {
		if strings.Contains(sql, unwanted) {
			t.Fatalf("expected direct aggregate rows SQL to avoid %q, got %q", unwanted, sql)
		}
	}
}

func TestBuildRangeSelectorQuerySQLUsesStepGridLookbackAndOffset(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, time.Minute, SelectorKindInstantVector)

	sql, params, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -360000, 240000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"ASOF INNER JOIN", "grid.eval_bound >= d.timestamp", "grid_base.eval_ts - toIntervalMillisecond({offset_ms:Int64}) AS eval_bound", "d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_lookback_ms"] != "300000" || params["param_offset_ms"] != "60000" {
		t.Fatalf("expected lookback/offset params, got %#v", params)
	}
}

func TestBuildRangeSelectorQuerySQLUsesStepGridAndLookback(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)

	sql, params, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -90000, 210000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts", "ASOF INNER JOIN", "grid.eval_bound >= d.timestamp", "toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})", "NOT isNaN(value)"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_lookback_ms"] != "300000" || params["param_offset_ms"] != "0" {
		t.Fatalf("expected 5m lookback and zero offset params, got %#v", params)
	}
}

func TestBuildRangeSelectorQuerySQLPreservesNegativeOffset(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, -1*time.Minute, SelectorKindInstantVector)

	sql, params, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -240000, 360000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range selector SQL, got error: %v", err)
	}
	if !strings.Contains(sql, "grid_base.eval_ts - toIntervalMillisecond({offset_ms:Int64}) AS eval_bound") {
		t.Fatalf("expected offset placeholder in SQL, got %q", sql)
	}
	if params["param_offset_ms"] != "-60000" {
		t.Fatalf("expected signed negative offset param, got %#v", params)
	}
}

func TestBuildRangeSelectorQuerySQLOmitsTagsProjectionWhenUnneeded(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)
	selector.NeedTags = false
	selector.RequireFullTags = false

	sql, _, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -90000, 210000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range selector SQL, got error: %v", err)
	}
	if strings.Contains(sql, "grid.tags AS tags") {
		t.Fatalf("expected tagless range selector SQL to avoid grouping on grid.tags projection, got %q", sql)
	}
	if !strings.Contains(sql, "CAST([], 'Array(Tuple(String, String))') AS tags") {
		t.Fatalf("expected synthesized empty tags in tagless range selector SQL, got %q", sql)
	}
	if strings.Contains(sql, "GROUP BY grid.id") || strings.Contains(sql, "argMax(") {
		t.Fatalf("expected tagless range selector SQL to use ASOF without inner grouping, got %q", sql)
	}
}

func TestBuildRangeMatrixSelectorQuerySQLOmitsTagsProjectionWhenUnneeded(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindRangeVector)
	selector.NeedTags = false
	selector.RequireFullTags = false

	sql, _, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -300000, 300000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range matrix selector SQL, got error: %v", err)
	}
	if strings.Contains(sql, "series.tags AS tags") {
		t.Fatalf("expected tagless range matrix selector SQL to avoid source tag projection, got %q", sql)
	}
	if !strings.Contains(sql, "CAST([], 'Array(Tuple(String, String))') AS tags") {
		t.Fatalf("expected synthesized empty tags in tagless range matrix selector SQL, got %q", sql)
	}
	if !strings.Contains(sql, "GROUP BY tags") {
		t.Fatalf("expected tagless range matrix selector SQL to still group by synthesized tags alias, got %q", sql)
	}
}
