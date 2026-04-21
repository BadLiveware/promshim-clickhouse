package storage

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	modelpkg "ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type QueryConfig struct{ Database, Table string }

type AggregationSource struct {
	PromQLLeaf string
	Selector   *SelectorSource
	ValueExpr  string
	TagsExpr   string
}

const instantQuerySQL = `
SELECT *
FROM prometheusQuery(
    {database:String},
    {table:String},
    {promql:String},
    fromUnixTimestamp64Milli({evaluation_ms:Int64})
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`

const rangeQuerySQL = `
SELECT *
FROM prometheusQueryRange(
    {database:String},
    {table:String},
    {promql:String},
    fromUnixTimestamp64Milli({start_ms:Int64}),
    fromUnixTimestamp64Milli({end_ms:Int64}),
    toDecimal64({step_ms:Int64}, 3) / 1000
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`

func BuildInstantQuerySQL(cfg QueryConfig, promql string, evaluationTimeMS int64) (string, map[string]string) {
	params := baseParams(cfg)
	params["param_promql"] = promql
	params["param_evaluation_ms"] = strconv.FormatInt(evaluationTimeMS, 10)
	return instantQuerySQL, params
}

func BuildRangeQuerySQL(cfg QueryConfig, promql string, startMS, endMS, stepMS int64) (string, map[string]string) {
	params := baseParams(cfg)
	params["param_promql"] = promql
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	return rangeQuerySQL, params
}

func BuildInstantAggregationQuerySQL(cfg QueryConfig, source AggregationSource, evaluationTimeMS int64, op parser.ItemType, grouping []string, without bool) (string, map[string]string, error) {
	return BuildInstantAggregationQuerySQLWithBounds(cfg, source, evaluationTimeMS, evaluationTimeMS, evaluationTimeMS, op, grouping, without)
}

func BuildInstantAggregationQuerySQLWithBounds(cfg QueryConfig, source AggregationSource, evaluationTimeMS, requiredStartMS, requiredEndMS int64, op parser.ItemType, grouping []string, without bool) (string, map[string]string, error) {
	aggExpr, err := buildAggregationValueSQL(op, "value")
	if err != nil {
		return "", nil, err
	}
	tagsExpr := buildAggregationTagsSQL("tags", grouping, without)
	sourceSQL, params, err := buildInstantSourceQuerySQL(cfg, source, evaluationTimeMS, requiredStartMS, requiredEndMS)
	if err != nil {
		return "", nil, err
	}
	sourceSubquery := renderAggregationInstantSourceSubquery(source, sourceSQL)
	return fmt.Sprintf(`
SELECT
    grouping_tags AS tags,
    max(timestamp) AS timestamp,
    %s AS value
FROM (
    SELECT
        %s AS grouping_tags,
        timestamp,
        value
    FROM (
%s
    )
)
GROUP BY grouping_tags
ORDER BY grouping_tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, aggExpr, tagsExpr, indentSQL(sourceSubquery, 8)), params, nil
}

func BuildRangeAggregationQuerySQL(cfg QueryConfig, source AggregationSource, startMS, endMS, stepMS int64, op parser.ItemType, grouping []string, without bool) (string, map[string]string, error) {
	return BuildRangeAggregationQuerySQLWithBounds(cfg, source, startMS, endMS, stepMS, startMS, endMS, op, grouping, without)
}

func BuildRangeAggregationQuerySQLWithBounds(cfg QueryConfig, source AggregationSource, startMS, endMS, stepMS, requiredStartMS, requiredEndMS int64, op parser.ItemType, grouping []string, without bool) (string, map[string]string, error) {
	aggExpr, err := buildAggregationValueSQL(op, "point.2")
	if err != nil {
		return "", nil, err
	}
	tagsExpr := buildAggregationTagsSQL("tags", grouping, without)
	sourceSQL, params, err := buildRangeSourceQuerySQL(cfg, source, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	if err != nil {
		return "", nil, err
	}
	sourceSubquery := renderAggregationRangeSourceSubquery(source, sourceSQL)
	return fmt.Sprintf(`
SELECT
    tags,
    arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
    SELECT
        grouping_tags AS tags,
        point.1 AS timestamp,
        %s AS value
    FROM (
        SELECT
            %s AS grouping_tags,
            arrayJoin(time_series) AS point
        FROM (
%s
        )
    )
    GROUP BY grouping_tags, timestamp
)
GROUP BY tags
ORDER BY tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, aggExpr, tagsExpr, indentSQL(sourceSubquery, 8)), params, nil
}

func BuildLabelsQuery(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf(`
SELECT DISTINCT label
FROM (
    SELECT arrayJoin(arrayMap(tag -> tag.1, series_tags)) AS label
    FROM (
%s
    )
)
ORDER BY label
FORMAT JSONEachRow
`, indentSQL(sourceSQL, 8)), params, nil
}

func BuildLabelValuesQuery(cfg QueryConfig, request *http.Request, labelName string) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	params["param_label_name"] = labelName
	return fmt.Sprintf(`
SELECT DISTINCT tag.2 AS value
FROM (
    SELECT arrayJoin(series_tags) AS tag
    FROM (
%s
    )
)
WHERE tag.1 = {label_name:String}
ORDER BY value
FORMAT JSONEachRow
`, indentSQL(sourceSQL, 8)), params, nil
}

func BuildSeriesQuery(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf(`
SELECT DISTINCT series_tags AS tags
FROM (
%s
)
FORMAT JSONEachRow
`, indentSQL(sourceSQL, 4)), params, nil
}

func baseParams(cfg QueryConfig) map[string]string {
	return map[string]string{"param_database": cfg.Database, "param_table": cfg.Table}
}

func renderAggregationInstantSourceSubquery(source AggregationSource, sourceSQL string) string {
	sourceValueExpr := strings.ReplaceAll(source.ValueExpr, "{value}", "value")
	if !aggregationSourceNeedsTags(source) {
		return fmt.Sprintf(`
SELECT
    timestamp,
    %s AS value
FROM (
%s
)
`, sourceValueExpr, indentSQL(sourceSQL, 4))
	}
	sourceTagsExpr := strings.ReplaceAll(source.TagsExpr, "{tags}", "tags")
	return fmt.Sprintf(`
SELECT
    %s AS tags,
    timestamp,
    %s AS value
FROM (
%s
)
`, sourceTagsExpr, sourceValueExpr, indentSQL(sourceSQL, 4))
}

func renderAggregationRangeSourceSubquery(source AggregationSource, sourceSQL string) string {
	sourceValueExpr := strings.ReplaceAll(source.ValueExpr, "{value}", "point.2")
	if !aggregationSourceNeedsTags(source) {
		return fmt.Sprintf(`
SELECT
    arrayMap(point -> (point.1, %s), time_series) AS time_series
FROM (
%s
)
`, sourceValueExpr, indentSQL(sourceSQL, 4))
	}
	sourceTagsExpr := strings.ReplaceAll(source.TagsExpr, "{tags}", "tags")
	return fmt.Sprintf(`
SELECT
    %s AS tags,
    arrayMap(point -> (point.1, %s), time_series) AS time_series
FROM (
%s
)
`, sourceTagsExpr, sourceValueExpr, indentSQL(sourceSQL, 4))
}

func aggregationSourceNeedsTags(source AggregationSource) bool {
	if source.Selector == nil {
		return true
	}
	return source.Selector.NeedTags
}

func buildAggregationTagsSQL(column string, grouping []string, without bool) string {
	if without {
		labels := append([]string{labels.MetricName}, grouping...)
		return fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> NOT has(%s, tag.1), %s))", sqlStringArrayLiteral(labels), column)
	}
	if len(grouping) == 0 {
		return "CAST([], 'Array(Tuple(String, String))')"
	}
	return fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> has(%s, tag.1), %s))", sqlStringArrayLiteral(grouping), column)
}

func buildAggregationValueSQL(op parser.ItemType, valueRef string) (string, error) {
	switch op {
	case parser.SUM:
		return fmt.Sprintf("if(countIf(isNaN(%s)) > 0, CAST(NULL, 'Nullable(Float64)'), sum(%s))", valueRef, valueRef), nil
	case parser.COUNT:
		return fmt.Sprintf("toFloat64(count(%s))", valueRef), nil
	case parser.MIN:
		return fmt.Sprintf("if(countIf(NOT isNaN(%s)) = 0, CAST(NULL, 'Nullable(Float64)'), minIf(%s, NOT isNaN(%s)))", valueRef, valueRef, valueRef), nil
	case parser.MAX:
		return fmt.Sprintf("if(countIf(NOT isNaN(%s)) = 0, CAST(NULL, 'Nullable(Float64)'), maxIf(%s, NOT isNaN(%s)))", valueRef, valueRef, valueRef), nil
	case parser.AVG:
		return fmt.Sprintf("if(countIf(isNaN(%s)) > 0 OR count() = 0, CAST(NULL, 'Nullable(Float64)'), avg(%s))", valueRef, valueRef), nil
	default:
		return "", fmt.Errorf("native SQL aggregation for operator %q is not implemented yet", op.String())
	}
}

func buildSeriesTagsSource(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	params := baseParams(cfg)
	tableRef := fmt.Sprintf("`%s`.`%s`", escapeIdentifier(cfg.Database), escapeIdentifier(cfg.Table))
	matchers := request.URL.Query()["match[]"]
	if len(matchers) == 0 {
		matchers = request.URL.Query()["match"]
	}
	start, end, hasRange, err := readOptionalRange(request)
	if err != nil {
		return "", nil, err
	}
	selectors := [][]*labels.Matcher{{}}
	if len(matchers) > 0 {
		parsed, err := parser.NewParser(parser.Options{}).ParseMetricSelectors(matchers)
		if err != nil {
			return "", nil, err
		}
		selectors = parsed
	}
	parts := make([]string, 0, len(selectors))
	for index, selector := range selectors {
		whereClauses := make([]string, 0, len(selector)+2)
		for matcherIndex, matcher := range selector {
			clause, extraParams := compileMatcher(index, matcherIndex, matcher)
			whereClauses = append(whereClauses, clause)
			for key, value := range extraParams {
				params[key] = value
			}
		}
		if hasRange {
			params["param_start_ms"] = strconv.FormatInt(start.UnixMilli(), 10)
			params["param_end_ms"] = strconv.FormatInt(end.UnixMilli(), 10)
			whereClauses = append(whereClauses, "max_time >= fromUnixTimestamp64Milli({start_ms:Int64})", "min_time <= fromUnixTimestamp64Milli({end_ms:Int64})")
		}
		part := strings.Builder{}
		part.WriteString("SELECT arrayConcat([tuple('__name__', metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(tags), mapValues(tags))) AS series_tags FROM timeSeriesTags(")
		part.WriteString(tableRef)
		part.WriteString(")")
		if len(whereClauses) > 0 {
			part.WriteString(" WHERE ")
			part.WriteString(strings.Join(whereClauses, " AND "))
		}
		parts = append(parts, part.String())
	}
	return strings.Join(parts, "\nUNION ALL\n"), params, nil
}

func compileMatcher(selectorIndex, matcherIndex int, matcher *labels.Matcher) (string, map[string]string) {
	params := map[string]string{}
	column := "metric_name"
	if matcher.Name != labels.MetricName {
		keyName := fmt.Sprintf("selector_%d_matcher_%d_key", selectorIndex, matcherIndex)
		params["param_"+keyName] = matcher.Name
		column = fmt.Sprintf("tags[concat('', {%s:String})]", keyName)
	}
	valueName := fmt.Sprintf("selector_%d_matcher_%d_value", selectorIndex, matcherIndex)
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

func readOptionalRange(request *http.Request) (start, end time.Time, hasRange bool, err error) {
	query := request.URL.Query()
	startRaw := query.Get("start")
	endRaw := query.Get("end")
	if startRaw == "" && endRaw == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	if startRaw == "" || endRaw == "" {
		return time.Time{}, time.Time{}, false, fmt.Errorf("start and end must be provided together")
	}
	start, err = modelpkg.ParsePrometheusTimestamp(startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	end, err = modelpkg.ParsePrometheusTimestamp(endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	return start, end, true, nil
}

func indentSQL(sql string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimSpace(sql), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
func escapeIdentifier(value string) string { return strings.ReplaceAll(value, "`", "``") }
func sqlStringArrayLiteral(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, sqlStringLiteral(v))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
func sqlStringLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}
func NativeFloatLiteral(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
