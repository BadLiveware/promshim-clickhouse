package storage

import (
	"strings"
	"testing"
	"time"

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
	for _, expected := range []string{"metric_name = {instant_matcher_0_value:String}", "match(tags[concat('', {instant_matcher_1_key:String})], {instant_matcher_1_value:String})", "tags[concat('', {instant_matcher_2_key:String})] != {instant_matcher_2_value:String}", "fromUnixTimestamp64Milli({required_start_ms:Int64})", "fromUnixTimestamp64Milli({required_end_ms:Int64})"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_instant_matcher_0_value"] != "up" || params["param_instant_matcher_1_value"] != "api|worker" || params["param_instant_matcher_2_value"] != "dev" {
		t.Fatalf("unexpected matcher params: %#v", params)
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
	for _, expected := range []string{"tags[concat('', {instant_matcher_0_key:String})] = {instant_matcher_0_value:String}", "NOT match(tags[concat('', {instant_matcher_1_key:String})], {instant_matcher_1_value:String})"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_instant_matcher_0_value"] != "a:9090" || params["param_instant_matcher_1_value"] != "dev|qa" {
		t.Fatalf("unexpected matcher params: %#v", params)
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

func TestBuildRangeSelectorQuerySQLUsesStepGridAndLookback(t *testing.T) {
	selector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)

	sql, params, err := BuildRangeSelectorQuerySQL(QueryConfig{Database: "observability", Table: "prometheus"}, selector, -90000, 210000, 0, 300000, 30000)
	if err != nil {
		t.Fatalf("expected range selector SQL, got error: %v", err)
	}
	for _, expected := range []string{"arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts", "argMax(d.value, d.timestamp)", "toIntervalMillisecond({lookback_ms:Int64})", "GROUP BY grid.id, grid.tags, grid.eval_ts"} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if params["param_lookback_ms"] != "300000" {
		t.Fatalf("expected 5m lookback param, got %#v", params)
	}
}
