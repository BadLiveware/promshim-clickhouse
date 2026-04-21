package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

type SelectorKind string

const (
	SelectorKindInstantVector SelectorKind = "instant_vector_selector"
	SelectorKindRangeVector   SelectorKind = "range_vector_selector"
)

type SelectorSource struct {
	Kind              SelectorKind
	MetricName        string
	Matchers          []*labels.Matcher
	NeedTags          bool
	RequireFullTags   bool
	RequiredTagLabels []string
	LookbackMS        int64
	OffsetMS          int64
}

func BuildInstantSelectorQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	sql, params, err := buildInstantSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS)
	if err != nil {
		return "", nil, err
	}
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", params, nil
}

func BuildRangeSelectorQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64) (string, map[string]string, error) {
	sql, params, err := buildRangeSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	if err != nil {
		return "", nil, err
	}
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", params, nil
}

func buildInstantSourceQuerySQL(cfg QueryConfig, source AggregationSource, evaluationTimeMS, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	if source.Selector != nil {
		endMS := requiredEndMS
		if endMS == 0 {
			endMS = evaluationTimeMS
		}
		startMS := requiredStartMS
		if startMS == 0 {
			startMS = evaluationTimeMS
		}
		return buildInstantSelectorSourceSQL(cfg, *source.Selector, startMS, endMS)
	}
	params := baseParams(cfg)
	params["param_promql"] = source.PromQLLeaf
	params["param_evaluation_ms"] = strconv.FormatInt(evaluationTimeMS, 10)
	return strings.TrimSpace(`
SELECT *
FROM prometheusQuery(
    {database:String},
    {table:String},
    {promql:String},
    fromUnixTimestamp64Milli({evaluation_ms:Int64})
)`), params, nil
}

func buildRangeSourceQuerySQL(cfg QueryConfig, source AggregationSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64) (string, map[string]string, error) {
	if source.Selector != nil {
		return buildRangeSelectorSourceSQL(cfg, *source.Selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	}
	params := baseParams(cfg)
	params["param_promql"] = source.PromQLLeaf
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	return strings.TrimSpace(`
SELECT *
FROM prometheusQueryRange(
    {database:String},
    {table:String},
    {promql:String},
    fromUnixTimestamp64Milli({start_ms:Int64}),
    fromUnixTimestamp64Milli({end_ms:Int64}),
    toDecimal64({step_ms:Int64}, 3) / 1000
)`), params, nil
}

func buildInstantSelectorSourceSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "instant", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	tableRef := timeSeriesTableRef(cfg)
	selectTags := "series.tags AS tags,"
	groupBy := "d.id, series.tags"
	orderBy := "ORDER BY tags"
	if !selector.NeedTags {
		selectTags = "CAST([], 'Array(Tuple(String, String))') AS tags,"
		groupBy = "d.id"
		orderBy = ""
	}
	return fmt.Sprintf(`
SELECT
    %s
    max(d.timestamp) AS timestamp,
    argMax(d.value, d.timestamp) AS value
FROM timeSeriesData(%s) AS d
INNER JOIN (
%s
) AS series ON d.id = series.id
WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64})
  AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})
GROUP BY %s
%s
`, selectTags, tableRef, indentSQL(matchedSeriesSQL, 4), groupBy, orderBy), params, nil
}

func buildRangeSelectorSourceSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64) (string, map[string]string, error) {
	if stepMS <= 0 {
		return "", nil, fmt.Errorf("range selector SQL requires a positive step")
	}
	switch selector.Kind {
	case SelectorKindInstantVector:
		return buildRangeInstantSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	case SelectorKindRangeVector:
		return buildRangeMatrixSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS)
	default:
		return "", nil, fmt.Errorf("unsupported selector kind %q", selector.Kind)
	}
}

func buildRangeInstantSelectorSourceSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64) (string, map[string]string, error) {
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_instant", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	params["param_lookback_ms"] = strconv.FormatInt(selector.LookbackMS, 10)
	tableRef := timeSeriesTableRef(cfg)
	gridTags := "series.tags AS tags,"
	outerTags := "tags"
	groupByInner := "grid.id, grid.tags, grid.eval_ts"
	groupByOuter := "tags"
	orderBy := "ORDER BY tags"
	if !selector.NeedTags {
		gridTags = "CAST([], 'Array(Tuple(String, String))') AS tags,"
		outerTags = "CAST([], 'Array(Tuple(String, String))') AS tags"
		groupByInner = "grid.id, grid.eval_ts"
		groupByOuter = "tags"
		orderBy = ""
	}
	return fmt.Sprintf(`
SELECT
    %s,
    arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
    SELECT
        grid.tags AS tags,
        grid.eval_ts AS timestamp,
        argMax(d.value, d.timestamp) AS value
    FROM (
        SELECT
            series.id AS id,
            %s
            arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts
        FROM (
%s
        ) AS series
    ) AS grid
    INNER JOIN timeSeriesData(%s) AS d ON d.id = grid.id
    WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64})
      AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})
      AND d.timestamp <= grid.eval_ts
      AND d.timestamp >= grid.eval_ts - toIntervalMillisecond({lookback_ms:Int64})
    GROUP BY %s
)
GROUP BY %s
%s
`, outerTags, gridTags, indentSQL(matchedSeriesSQL, 12), tableRef, groupByInner, groupByOuter, orderBy), params, nil
}

func buildRangeMatrixSelectorSourceSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_matrix", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	tableRef := timeSeriesTableRef(cfg)
	selectTags := "series.tags AS tags,"
	groupBy := "tags"
	orderBy := "ORDER BY tags"
	if !selector.NeedTags {
		selectTags = "CAST([], 'Array(Tuple(String, String))') AS tags,"
		groupBy = "tags"
		orderBy = ""
	}
	return fmt.Sprintf(`
SELECT
    tags,
    arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
    SELECT
        %s
        d.timestamp AS timestamp,
        d.value AS value
    FROM timeSeriesData(%s) AS d
    INNER JOIN (
%s
    ) AS series ON d.id = series.id
    WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64})
      AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})
)
GROUP BY %s
%s
`, selectTags, tableRef, indentSQL(matchedSeriesSQL, 4), groupBy, orderBy), params, nil
}

func selectorTagsExpr(selector SelectorSource) string {
	base := "arrayConcat([tuple('__name__', metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(tags), mapValues(tags)))"
	if !selector.RequireFullTags && len(selector.RequiredTagLabels) > 0 {
		return fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> has(%s, tag.1), %s))", sqlStringArrayLiteral(selector.RequiredTagLabels), base)
	}
	return base
}

func buildMatchedSeriesSQL(cfg QueryConfig, selector SelectorSource, prefix string, requiredStartMS, requiredEndMS int64, addTimeOverlap bool) (string, map[string]string, error) {
	params := baseParams(cfg)
	params["param_required_start_ms"] = strconv.FormatInt(requiredStartMS, 10)
	params["param_required_end_ms"] = strconv.FormatInt(requiredEndMS, 10)
	whereClauses := make([]string, 0, len(selector.Matchers)+3)
	matcherIndex := 0
	if selector.MetricName != "" {
		matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, selector.MetricName)
		if err != nil {
			return "", nil, err
		}
		clause, extraParams := compileMatcherClause(prefix, matcherIndex, "metric_name", "tags", matcher)
		matcherIndex++
		whereClauses = append(whereClauses, clause)
		mergeParams(params, extraParams)
	}
	for _, matcher := range selector.Matchers {
		if matcher == nil {
			continue
		}
		if selector.MetricName != "" && matcher.Name == labels.MetricName && matcher.Type == labels.MatchEqual && matcher.Value == selector.MetricName {
			continue
		}
		clause, extraParams := compileMatcherClause(prefix, matcherIndex, "metric_name", "tags", matcher)
		matcherIndex++
		whereClauses = append(whereClauses, clause)
		mergeParams(params, extraParams)
	}
	if addTimeOverlap {
		whereClauses = append(whereClauses, "max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64})", "min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})")
	}
	builder := strings.Builder{}
	builder.WriteString("SELECT id")
	if selector.NeedTags {
		builder.WriteString(", ")
		builder.WriteString(selectorTagsExpr(selector))
		builder.WriteString(" AS tags")
	}
	builder.WriteString(" FROM timeSeriesTags(")
	builder.WriteString(timeSeriesTableRef(cfg))
	builder.WriteString(")")
	if len(whereClauses) > 0 {
		builder.WriteString(" WHERE ")
		builder.WriteString(strings.Join(whereClauses, " AND "))
	}
	return builder.String(), params, nil
}

func compileMatcherClause(prefix string, matcherIndex int, metricColumn, tagsColumn string, matcher *labels.Matcher) (string, map[string]string) {
	params := map[string]string{}
	column := metricColumn
	if matcher.Name != labels.MetricName {
		keyName := fmt.Sprintf("%s_matcher_%d_key", prefix, matcherIndex)
		params["param_"+keyName] = matcher.Name
		column = fmt.Sprintf("%s[concat('', {%s:String})]", tagsColumn, keyName)
	}
	valueName := fmt.Sprintf("%s_matcher_%d_value", prefix, matcherIndex)
	params["param_"+valueName] = matcher.Value
	valueRef := fmt.Sprintf("{%s:String}", valueName)
	switch matcher.Type {
	case labels.MatchEqual:
		return fmt.Sprintf("%s = %s", column, valueRef), params
	case labels.MatchNotEqual:
		return fmt.Sprintf("%s != %s", column, valueRef), params
	case labels.MatchRegexp:
		return fmt.Sprintf("match(%s, %s)", column, valueRef), params
	case labels.MatchNotRegexp:
		return fmt.Sprintf("NOT match(%s, %s)", column, valueRef), params
	default:
		return "1", params
	}
}

func mergeParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func timeSeriesTableRef(cfg QueryConfig) string {
	return fmt.Sprintf("`%s`.`%s`", escapeIdentifier(cfg.Database), escapeIdentifier(cfg.Table))
}

func selectorSourceFromMatchers(metricName string, matchers []*labels.Matcher, lookback, offset time.Duration, kind SelectorKind) SelectorSource {
	return SelectorSource{Kind: kind, MetricName: metricName, Matchers: matchers, NeedTags: true, RequireFullTags: true, LookbackMS: lookback.Milliseconds(), OffsetMS: offset.Milliseconds()}
}
