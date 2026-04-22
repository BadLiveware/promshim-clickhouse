package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage/schema"
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
	var (
		sql    string
		params map[string]string
		err    error
	)
	switch selector.Kind {
	case SelectorKindRangeVector:
		sql, params, err = buildRangeMatrixSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS)
	default:
		sql, params, err = buildInstantSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS)
	}
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeSelectorQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64) (string, map[string]string, error) {
	sql, params, err := buildRangeSelectorSourceSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeWindowSelectorQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, windowValueExpr string, minimumSeriesLength int) (string, map[string]string, error) {
	if selector.Kind != SelectorKindRangeVector {
		return "", nil, fmt.Errorf("range-window selector SQL requires a range-vector selector, got %q", selector.Kind)
	}
	if stepMS <= 0 {
		return "", nil, fmt.Errorf("range-window selector SQL requires a positive step")
	}
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_window", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	params["param_lookback_ms"] = strconv.FormatInt(selector.LookbackMS, 10)
	params["param_offset_ms"] = strconv.FormatInt(selector.OffsetMS, 10)

	gridTagsExpr := sqlb.Expr(sqlb.Ident("series.tags"))
	windowTagsExpr := sqlb.Expr(sqlb.Ident("grid.tags"))
	finalTagsExpr := sqlb.Expr(sqlb.RawLit{V: "arrayFilter(tag -> tag.1 != '__name__', tags)"})
	groupByWindow := []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.tags"), sqlb.Ident("grid.eval_ts")}
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}}
	if !selector.NeedTags {
		gridTagsExpr = schema.EmptyTagsArrayExpr()
		windowTagsExpr = schema.EmptyTagsArrayExpr()
		finalTagsExpr = sqlb.Ident("tags")
		groupByWindow = []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.eval_ts")}
		orderBy = nil
	}

	grid := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("series.id"), Alias: "id"}, {Expr: gridTagsExpr, Alias: "tags"}, {Expr: sqlb.RawLit{V: "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64})))"}, Alias: "eval_ts"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
	}
	windowed := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: windowTagsExpr, Alias: "tags"}, {Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"}, {Expr: sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((d.timestamp, d.value)))"}, Alias: "window_series"}},
		From:    sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"}, Kind: "INNER", On: sqlb.RawLit{V: "d.id = grid.id"}},
		Where:   sqlb.RawLit{V: "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64}) AND d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64}) AND " + staleNaNFilterSQL("d.value")},
		GroupBy: groupByWindow,
	}
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: finalTagsExpr, Alias: "final_tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: windowValueExpr}, Alias: "value"}},
		From:    sqlb.SubSelect{S: windowed},
		Where:   sqlb.RawLit{V: "length(window_series) > " + strconv.Itoa(minimumSeriesLength)},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"}},
		From:    sqlb.SubSelect{S: perStep},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: orderBy,
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
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
	columns := []sqlb.ColExpr{
		{Expr: sqlb.Call{Name: "max", Args: []sqlb.Expr{sqlb.Ident("d.timestamp")}}, Alias: "timestamp"},
		{Expr: sqlb.Call{Name: "argMax", Args: []sqlb.Expr{sqlb.Ident("d.value"), sqlb.Ident("d.timestamp")}}, Alias: "value"},
	}
	groupBy := []sqlb.Expr{sqlb.Ident("d.id")}
	var orderBy []sqlb.OrderExpr
	if selector.NeedTags {
		columns = append([]sqlb.ColExpr{{Expr: sqlb.Ident("series.tags"), Alias: "tags"}}, columns...)
		groupBy = append(groupBy, sqlb.Ident("series.tags"))
		orderBy = append(orderBy, sqlb.OrderExpr{Expr: sqlb.Ident("tags")})
	} else {
		columns = append([]sqlb.ColExpr{{Expr: schema.EmptyTagsArrayExpr(), Alias: "tags"}}, columns...)
	}
	query := &sqlb.Select{
		Columns: columns,
		From: sqlb.Join{
			Left:  sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"},
			Right: sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
			Kind:  "INNER",
			On:    sqlb.RawLit{V: "d.id = series.id"},
		},
		Where:   sqlb.RawLit{V: "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})"},
		GroupBy: groupBy,
		Having:  sqlb.RawLit{V: "NOT isNaN(value)"},
		OrderBy: orderBy,
	}
	sql, _, err := query.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
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
	params["param_offset_ms"] = strconv.FormatInt(selector.OffsetMS, 10)

	gridTagsExpr := sqlb.Expr(sqlb.Ident("series.tags"))
	innerTagsExpr := sqlb.Expr(sqlb.Ident("grid.tags"))
	outerTagsExpr := sqlb.Expr(sqlb.Ident("tags"))
	groupByInner := []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.tags"), sqlb.Ident("grid.eval_ts")}
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}}
	if !selector.NeedTags {
		gridTagsExpr = schema.EmptyTagsArrayExpr()
		innerTagsExpr = schema.EmptyTagsArrayExpr()
		outerTagsExpr = schema.EmptyTagsArrayExpr()
		groupByInner = []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.eval_ts")}
		orderBy = nil
	}

	grid := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("series.id"), Alias: "id"},
			{Expr: gridTagsExpr, Alias: "tags"},
			{Expr: sqlb.RawLit{V: "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64})))"}, Alias: "eval_ts"},
		},
		From: sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
	}
	inner := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: innerTagsExpr, Alias: "tags"},
			{Expr: sqlb.Ident("grid.eval_ts"), Alias: "timestamp"},
			{Expr: sqlb.Call{Name: "argMax", Args: []sqlb.Expr{sqlb.Ident("d.value"), sqlb.Ident("d.timestamp")}}, Alias: "value"},
		},
		From: sqlb.Join{
			Left:  sqlb.SubSelect{S: grid, Alias: "grid"},
			Right: sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"},
			Kind:  "INNER",
			On:    sqlb.RawLit{V: "d.id = grid.id"},
		},
		Where:   sqlb.RawLit{V: "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64}) AND d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})"},
		GroupBy: groupByInner,
		Having:  sqlb.RawLit{V: "NOT isNaN(value)"},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: outerTagsExpr, Alias: "tags"},
			{Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"},
		},
		From:    sqlb.SubSelect{S: inner},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: orderBy,
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func buildRangeMatrixSelectorSourceSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_matrix", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	selectTagsExpr := sqlb.Expr(sqlb.Ident("series.tags"))
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}}
	if !selector.NeedTags {
		selectTagsExpr = schema.EmptyTagsArrayExpr()
		orderBy = nil
	}
	inner := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: selectTagsExpr, Alias: "tags"},
			{Expr: sqlb.Ident("d.timestamp"), Alias: "timestamp"},
			{Expr: sqlb.Ident("d.value"), Alias: "value"},
		},
		From: sqlb.Join{
			Left:  sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"},
			Right: sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
			Kind:  "INNER",
			On:    sqlb.RawLit{V: "d.id = series.id"},
		},
		Where: sqlb.RawLit{V: "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND " + staleNaNFilterSQL("d.value")},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"},
		},
		From:    sqlb.SubSelect{S: inner},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: orderBy,
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func selectorTagsExpr(selector SelectorSource, metricColumn, tagsColumn string) string {
	base := sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{
		sqlb.RawLit{V: "[tuple('__name__', " + metricColumn + ")]"},
		sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
			sqlb.RawLit{V: "(k, v) -> tuple(k, v)"},
			sqlb.Call{Name: "mapKeys", Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}}},
			sqlb.Call{Name: "mapValues", Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}}},
		}},
	}}
	if !selector.RequireFullTags && len(selector.RequiredTagLabels) > 0 {
		return renderStorageExprNoParams(sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
			sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
				sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(selector.RequiredTagLabels) + ", tag.1)"},
				base,
			}},
		}})
	}
	return renderStorageExprNoParams(base)
}

func buildMatchedSeriesSQL(cfg QueryConfig, selector SelectorSource, prefix string, requiredStartMS, requiredEndMS int64, addTimeOverlap bool) (string, map[string]string, error) {
	params := baseParams(cfg)
	params["param_required_start_ms"] = strconv.FormatInt(requiredStartMS, 10)
	params["param_required_end_ms"] = strconv.FormatInt(requiredEndMS, 10)
	metricColumn := "src.metric_name"
	tagsColumn := "src.tags"
	whereClauses := make([]string, 0, len(selector.Matchers)+3)
	matcherIndex := 0
	if selector.MetricName != "" {
		matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, selector.MetricName)
		if err != nil {
			return "", nil, err
		}
		clause, extraParams := compileMatcherClause(prefix, matcherIndex, metricColumn, tagsColumn, matcher)
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
		clause, extraParams := compileMatcherClause(prefix, matcherIndex, metricColumn, tagsColumn, matcher)
		matcherIndex++
		whereClauses = append(whereClauses, clause)
		mergeParams(params, extraParams)
	}
	if addTimeOverlap {
		whereClauses = append(whereClauses, "src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64})", "src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})")
	}
	columns := []sqlb.ColExpr{{Expr: sqlb.Ident("src.id")}}
	if selector.NeedTags {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: selectorTagsExpr(selector, metricColumn, tagsColumn)}, Alias: "tags"})
	}
	query := &sqlb.Select{
		Columns: columns,
		From:    sqlb.RawSource{SQL: schema.TimeSeriesTagsRef(timeSeriesTableRef(cfg)), Alias: "src"},
	}
	if len(whereClauses) > 0 {
		query.Where = sqlb.RawLit{V: strings.Join(whereClauses, " AND ")}
	}
	sql, _, err := query.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func compileMatcherClause(prefix string, matcherIndex int, metricColumn, tagsColumn string, matcher *labels.Matcher) (string, map[string]string) {
	columnExpr := sqlb.Expr(sqlb.RawLit{V: metricColumn})
	params := map[string]string{}
	if matcher.Name != labels.MetricName {
		keyName := prefix + "_matcher_" + strconv.Itoa(matcherIndex) + "_key"
		columnExpr = sqlb.Subscr{Array: sqlb.RawLit{V: tagsColumn}, Index: sqlb.Call{Name: "concat", Args: []sqlb.Expr{sqlb.RawLit{V: "''"}, sqlb.Param{Name: keyName, Type: "String", V: matcher.Name}}}}
	}
	valueName := prefix + "_matcher_" + strconv.Itoa(matcherIndex) + "_value"
	valueExpr := sqlb.Param{Name: valueName, Type: "String", V: matcherSQLPattern(matcher)}
	columnSQL, columnParams, err := sqlb.BuildExpr(columnExpr)
	if err != nil {
		panic(err)
	}
	mergeParams(params, columnParams)
	valueSQL, valueParams, err := sqlb.BuildExpr(valueExpr)
	if err != nil {
		panic(err)
	}
	mergeParams(params, valueParams)
	switch matcher.Type {
	case labels.MatchEqual:
		return columnSQL + " = " + valueSQL, params
	case labels.MatchNotEqual:
		return columnSQL + " != " + valueSQL, params
	case labels.MatchRegexp:
		return renderStorageExprNoParams(sqlb.Call{Name: "match", Args: []sqlb.Expr{sqlb.RawLit{V: columnSQL}, sqlb.RawLit{V: valueSQL}}}), params
	case labels.MatchNotRegexp:
		return "NOT " + renderStorageExprNoParams(sqlb.Call{Name: "match", Args: []sqlb.Expr{sqlb.RawLit{V: columnSQL}, sqlb.RawLit{V: valueSQL}}}), params
	default:
		return "1", params
	}
}

func matcherSQLPattern(matcher *labels.Matcher) string {
	if matcher == nil {
		return ""
	}
	switch matcher.Type {
	case labels.MatchRegexp, labels.MatchNotRegexp:
		return "^(?:" + matcher.Value + ")$"
	default:
		return matcher.Value
	}
}

func renderStorageExprNoParams(expr sqlb.Expr) string {
	sql, params, err := sqlb.BuildExpr(expr)
	if err != nil {
		panic(err)
	}
	if len(params) != 0 {
		panic(fmt.Errorf("storage expression unexpectedly produced params: %#v", params))
	}
	return sql
}

func mergeParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func timeSeriesTableRef(cfg QueryConfig) string {
	return "`" + escapeIdentifier(cfg.Database) + "`.`" + escapeIdentifier(cfg.Table) + "`"
}

func selectorSourceFromMatchers(metricName string, matchers []*labels.Matcher, lookback, offset time.Duration, kind SelectorKind) SelectorSource {
	return SelectorSource{Kind: kind, MetricName: metricName, Matchers: matchers, NeedTags: true, RequireFullTags: true, LookbackMS: lookback.Milliseconds(), OffsetMS: offset.Milliseconds()}
}
