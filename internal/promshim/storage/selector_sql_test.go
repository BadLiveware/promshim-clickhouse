package storage

import (
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/native/sqlb"
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

	sql, params, err := BuildRangeWindowSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -360000, 300000, 0, 300000, 30000, "arraySum(arrayMap(point -> point.2, window_series))", 0)
	if err != nil {
		t.Fatalf("expected range window selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts", "arraySort(item -> item.1, groupArray((d.timestamp, d.value))) AS window_series", "d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64})", "d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})", "GROUP BY grid.id, grid.tags, grid.eval_ts"} {
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

	sql, _, err := BuildRangeWindowSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -360000, 240000, 0, 300000, 30000, "arraySum(arrayMap(point -> point.2, window_series))", 0)
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

func TestBuildRangeSelectorQuerySQLUsesStepGridLookbackAndOffset(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, time.Minute, SelectorKindInstantVector)

	sql, params, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -360000, 240000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64})", "d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})"} {
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
	for _, expected := range []string{"arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts", "argMax(d.value, d.timestamp)", "toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})", "GROUP BY grid.id, grid.tags, grid.eval_ts"} {
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
	if !strings.Contains(sql, "d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64})") {
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
	if !strings.Contains(sql, "GROUP BY grid.id, grid.eval_ts") {
		t.Fatalf("expected tagless range selector SQL to group without grid.tags, got %q", sql)
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
