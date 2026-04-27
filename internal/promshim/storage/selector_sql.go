package storage

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
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

func BuildRangeSelectorRowsQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64) (string, map[string]string, error) {
	if selector.Kind != SelectorKindInstantVector {
		return "", nil, fmt.Errorf("range selector rows SQL requires an instant-vector selector, got %q", selector.Kind)
	}
	sql, params, err := buildRangeInstantSelectorRowsSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, "range_instant_rows")
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeMatrixSelectorRowsQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	if selector.Kind != SelectorKindRangeVector {
		return "", nil, fmt.Errorf("range matrix selector rows SQL requires a range-vector selector, got %q", selector.Kind)
	}
	sql, params, err := buildRangeMatrixSelectorRowsSQL(cfg, selector, requiredStartMS, requiredEndMS)
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeWindowSelectorQuerySQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, windowValueExpr string, minimumSeriesLength int) (string, map[string]string, error) {
	return BuildRangeWindowSelectorQuerySQLWithFinalTags(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, windowValueExpr, "", minimumSeriesLength)
}

func BuildRangeWindowSelectorRowsQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, windowValueExpr, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, error) {
	perStep, params, _, err := buildRangeWindowSelectorPerStepQuery(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, windowValueExpr, finalTagsSQL, minimumSeriesLength)
	if err != nil {
		return "", nil, err
	}
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
		From:    sqlb.SubSelect{S: perStep},
	}
	sql, _, err := rows.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeWindowSelectorQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, windowValueExpr, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, error) {
	perStep, params, orderBy, err := buildRangeWindowSelectorPerStepQuery(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, windowValueExpr, finalTagsSQL, minimumSeriesLength)
	if err != nil {
		return "", nil, err
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
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

func BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, error) {
	perStep, params, orderBy, err := buildRangeWindowSelectorDirectAggregatePerStepQuery(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, finalTagsSQL, minimumSeriesLength)
	if err != nil {
		return "", nil, err
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
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

func BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, error) {
	perStep, params, _, err := buildRangeWindowSelectorDirectAggregatePerStepQuery(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, finalTagsSQL, minimumSeriesLength)
	if err != nil {
		return "", nil, err
	}
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
		From:    sqlb.SubSelect{S: perStep},
	}
	sql, _, err := rows.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeWindowSelectorCumulativeAvgQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, error) {
	perStepSQL, params, orderBy, err := buildRangeWindowSelectorCumulativeAvgPerStepSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, finalTagsSQL, minimumSeriesLength)
	if err != nil {
		return "", nil, err
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(perStepSQL)},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: orderBy,
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeWindowSelectorCumulativeAvgRowsQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, error) {
	perStepSQL, params, _, err := buildRangeWindowSelectorCumulativeAvgPerStepSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, finalTagsSQL, minimumSeriesLength)
	if err != nil {
		return "", nil, err
	}
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(perStepSQL)},
	}
	sql, _, err := rows.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeNativeGridSelectorQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string) (string, map[string]string, error) {
	inner, params, finalTagsExpr, err := buildRangeNativeGridSelectorInner(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, finalTagsSQL)
	if err != nil {
		return "", nil, err
	}
	timeGridExpr := nativeGridTimestampValueZipExpr()
	series := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: finalTagsExpr, Alias: "tags"},
			{Expr: sqlb.RawLit{V: "arrayMap(point -> (point.1, toFloat64(assumeNotNull(point.2))), arrayFilter(point -> isNotNull(point.2), " + timeGridExpr + "))"}, Alias: "time_series"},
		},
		From:    sqlb.SubSelect{S: inner},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
	}
	sql, _, err := series.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeRateSelectorNativeGridQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, finalTagsSQL string) (string, map[string]string, error) {
	return BuildRangeNativeGridSelectorQuerySQLWithFinalTags(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, "rate", finalTagsSQL)
}

func BuildRangeNativeGridSelectorSumAggregationQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string, grouping []string, without bool) (string, map[string]string, error) {
	inner, params, finalTagsExpr, err := buildRangeNativeGridSelectorInner(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, finalTagsSQL)
	if err != nil {
		return "", nil, err
	}
	perSeries := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: finalTagsExpr, Alias: "final_tags"},
			{Expr: sqlb.Ident("values"), Alias: "values"},
		},
		From: sqlb.SubSelect{S: inner},
	}
	timeGridExpr := "arrayMap(i -> fromUnixTimestamp64Milli({start_ms:Int64}) + toIntervalMillisecond((i - 1) * {step_ms:Int64}), arrayEnumerate(group_values))"
	sumValuesExpr := "arrayReduce('sumForEach', groupArray(arrayMap(v -> if(isNull(v), 0., toFloat64(assumeNotNull(v))), values)))"
	presentCountsExpr := "arrayReduce('sumForEach', groupArray(arrayMap(v -> if(isNull(v), 0, 1), values)))"
	nanCountsExpr := "arrayReduce('sumForEach', groupArray(arrayMap(v -> if(isNotNull(v) AND isNaN(assumeNotNull(v)), 1, 0), values)))"
	groupedValues := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: buildAggregationTagsExpr(sqlb.Ident("final_tags"), grouping, without), Alias: "tags"},
			{Expr: sqlb.RawLit{V: sumValuesExpr}, Alias: "group_values"},
			{Expr: sqlb.RawLit{V: presentCountsExpr}, Alias: "present_counts"},
			{Expr: sqlb.RawLit{V: nanCountsExpr}, Alias: "nan_counts"},
		},
		From:    sqlb.SubSelect{S: perSeries},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
	}
	series := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: sqlb.RawLit{V: "arrayMap(point -> (point.1, if(point.4 > 0, nan, point.2)), arrayFilter(point -> point.3 > 0, arrayZip(" + timeGridExpr + ", group_values, present_counts, nan_counts)))"}, Alias: "time_series"},
		},
		From:    sqlb.SubSelect{S: groupedValues},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
	}
	sql, _, err := series.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeRateSelectorNativeGridSumAggregationQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, finalTagsSQL string, grouping []string, without bool) (string, map[string]string, error) {
	return BuildRangeNativeGridSelectorSumAggregationQuerySQLWithFinalTags(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, "rate", finalTagsSQL, grouping, without)
}

func BuildRangeNativeGridSelectorRowsQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string) (string, map[string]string, error) {
	inner, params, finalTagsExpr, err := buildRangeNativeGridSelectorInner(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, fn, finalTagsSQL)
	if err != nil {
		return "", nil, err
	}
	points := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: finalTagsExpr, Alias: "tags"},
			{Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"},
			{Expr: sqlb.RawLit{V: "toFloat64(assumeNotNull(point.2))"}, Alias: "value"},
		},
		From: sqlb.ArrayJoin{
			Base:  sqlb.SubSelect{S: inner},
			Expr:  sqlb.RawLit{V: nativeGridTimestampValueZipExpr()},
			Alias: "point",
		},
		Where: sqlb.RawLit{V: "isNotNull(point.2)"},
	}
	sql, _, err := points.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func BuildRangeRateSelectorNativeGridRowsQuerySQLWithFinalTags(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, finalTagsSQL string) (string, map[string]string, error) {
	return BuildRangeNativeGridSelectorRowsQuerySQLWithFinalTags(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, "rate", finalTagsSQL)
}

func buildRangeNativeGridSelectorInner(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string) (*sqlb.Select, map[string]string, sqlb.Expr, error) {
	if selector.Kind != SelectorKindRangeVector {
		return nil, nil, nil, fmt.Errorf("native-grid %s selector SQL requires a range-vector selector, got %q", fn, selector.Kind)
	}
	if stepMS <= 0 {
		return nil, nil, nil, fmt.Errorf("native-grid %s selector SQL requires a positive step", fn)
	}
	chFunction, ok := nativeGridRangeFunctionName(fn)
	if !ok {
		return nil, nil, nil, fmt.Errorf("native-grid selector SQL does not support range function %q", fn)
	}
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "native_grid_"+fn, requiredStartMS, requiredEndMS, true)
	if err != nil {
		return nil, nil, nil, err
	}
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	params["param_lookback_ms"] = strconv.FormatInt(selector.LookbackMS, 10)

	resolvedFinalTagsExpr := sqlb.Expr(sqlb.RawLit{V: emit.StripMetricName("tags")})
	if strings.TrimSpace(finalTagsSQL) != "" {
		resolvedFinalTagsExpr = sqlb.RawLit{V: finalTagsSQL}
	}
	inner := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("series.id"), Alias: "id"},
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("series.tags")}}, Alias: "tags"},
			{Expr: sqlb.RawLit{V: chFunction + "(fromUnixTimestamp64Milli({start_ms:Int64}), fromUnixTimestamp64Milli({end_ms:Int64}), toDecimal64({step_ms:Int64}, 3) / 1000, toDecimal64({lookback_ms:Int64}, 3) / 1000)(d.timestamp, d.value)"}, Alias: "values"},
		},
		From: sqlb.Join{
			Left:  sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"},
			Right: sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
			Kind:  "INNER",
			On:    sqlb.RawLit{V: "d.id = series.id"},
		},
		Where:   sqlb.RawLit{V: "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND " + staleNaNFilterSQL("d.value")},
		GroupBy: []sqlb.Expr{sqlb.Ident("series.id")},
	}
	return inner, params, resolvedFinalTagsExpr, nil
}

func nativeGridRangeFunctionName(fn string) (string, bool) {
	switch fn {
	case "rate":
		return "timeSeriesRateToGrid", true
	case "irate":
		return "timeSeriesInstantRateToGrid", true
	case "delta":
		return "timeSeriesDeltaToGrid", true
	case "idelta":
		return "timeSeriesInstantDeltaToGrid", true
	case "last_over_time":
		return "timeSeriesLastToGrid", true
	default:
		return "", false
	}
}

func nativeGridTimestampValueZipExpr() string {
	return "arrayZip(arrayMap(i -> fromUnixTimestamp64Milli({start_ms:Int64}) + toIntervalMillisecond((i - 1) * {step_ms:Int64}), arrayEnumerate(values)), values)"
}

func buildRangeWindowSelectorPerStepQuery(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, windowValueExpr, finalTagsSQL string, minimumSeriesLength int) (*sqlb.Select, map[string]string, []sqlb.OrderExpr, error) {
	if selector.Kind != SelectorKindRangeVector {
		return nil, nil, nil, fmt.Errorf("range-window selector SQL requires a range-vector selector, got %q", selector.Kind)
	}
	if stepMS <= 0 {
		return nil, nil, nil, fmt.Errorf("range-window selector SQL requires a positive step")
	}
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_window", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return nil, nil, nil, err
	}
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	params["param_lookback_ms"] = strconv.FormatInt(selector.LookbackMS, 10)
	params["param_offset_ms"] = strconv.FormatInt(selector.OffsetMS, 10)

	gridTagsExpr := sqlb.Expr(sqlb.Ident("series.tags"))
	windowTagsExpr := sqlb.Expr(sqlb.Ident("grid.tags"))
	resolvedFinalTagsExpr := sqlb.Expr(sqlb.RawLit{V: emit.StripMetricName("tags")})
	groupByWindow := []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.tags"), sqlb.Ident("grid.eval_ts")}
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}}
	if !selector.NeedTags {
		gridTagsExpr = emit.EmptyTagsArray()
		windowTagsExpr = emit.EmptyTagsArray()
		resolvedFinalTagsExpr = sqlb.Ident("tags")
		groupByWindow = []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.eval_ts")}
		orderBy = nil
	} else if strings.TrimSpace(finalTagsSQL) != "" {
		resolvedFinalTagsExpr = sqlb.RawLit{V: finalTagsSQL}
	}

	grid := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("series.id"), Alias: "id"}, {Expr: gridTagsExpr, Alias: "tags"}, {Expr: sqlb.RawLit{V: emit.GridEvalTSParams()}, Alias: "eval_ts"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
	}
	windowColumns := []sqlb.ColExpr{
		{Expr: windowTagsExpr, Alias: "tags"},
		{Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"},
		{Expr: sqlb.RawLit{V: "arraySort(groupArray((d.timestamp, d.value)))"}, Alias: "window_series"},
	}
	if rangeWindowFunctionNeedsTimestamps(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: emit.WindowPointTimestamps("window_series")}, Alias: "window_timestamps"})
	}
	if rangeWindowFunctionNeedsDuration(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "tupleElement(arrayElement(window_series, length(window_series)), 1) - tupleElement(arrayElement(window_series, 1), 1)"}, Alias: "window_duration_ms"})
	}
	if rangeWindowFunctionNeedsValues(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: emit.WindowPointValues("window_series")}, Alias: "window_values"})
	}
	if rangeWindowFunctionNeedsPairwiseNeighbors(fn) {
		windowColumns = append(windowColumns,
			sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayPopBack(window_values)"}, Alias: "window_values_prev"},
			sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayPopFront(window_values)"}, Alias: "window_values_cur"},
		)
	}
	if rangeWindowFunctionNeedsCounterDelta(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), window_values_prev, window_values_cur))"}, Alias: "counter_delta_sum"})
	}
	if rangeWindowFunctionNeedsChangesCount(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "toFloat64(arraySum(arrayMap((p, c) -> if(c != p, 1, 0), window_values_prev, window_values_cur)))"}, Alias: "changes_count"})
	}
	windowed := &sqlb.Select{
		Columns: windowColumns,
		From:    sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"}, Kind: "INNER", On: sqlb.RawLit{V: "d.id = grid.id"}},
		Where:   sqlb.RawLit{V: emit.RangeWindowTimeFilter("d.value")},
		GroupBy: groupByWindow,
	}
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: resolvedFinalTagsExpr, Alias: "final_tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: windowValueExpr}, Alias: "value"}},
		From:    sqlb.SubSelect{S: windowed},
		Where:   sqlb.RawLit{V: "length(window_series) > " + strconv.Itoa(minimumSeriesLength)},
	}
	return perStep, params, orderBy, nil
}

func buildRangeWindowSelectorDirectAggregatePerStepQuery(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, fn, finalTagsSQL string, minimumSeriesLength int) (*sqlb.Select, map[string]string, []sqlb.OrderExpr, error) {
	if selector.Kind != SelectorKindRangeVector {
		return nil, nil, nil, fmt.Errorf("range-window selector SQL requires a range-vector selector, got %q", selector.Kind)
	}
	if stepMS <= 0 {
		return nil, nil, nil, fmt.Errorf("range-window selector SQL requires a positive step")
	}
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_window_direct", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return nil, nil, nil, err
	}
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	params["param_lookback_ms"] = strconv.FormatInt(selector.LookbackMS, 10)
	params["param_offset_ms"] = strconv.FormatInt(selector.OffsetMS, 10)

	resolvedFinalTagsExpr := sqlb.Expr(sqlb.RawLit{V: emit.StripMetricName("tags")})
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}}
	if !selector.NeedTags {
		resolvedFinalTagsExpr = emit.EmptyTagsArray()
		orderBy = nil
	} else if strings.TrimSpace(finalTagsSQL) != "" {
		resolvedFinalTagsExpr = sqlb.RawLit{V: finalTagsSQL}
	}

	aggregateColumns, aggregateValueExpr, err := directRangeWindowAggregateSpec(fn)
	if err != nil {
		return nil, nil, nil, err
	}

	grid := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("series.id"), Alias: "id"}, {Expr: sqlb.RawLit{V: emit.GridEvalTSParams()}, Alias: "eval_ts"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
	}
	windowColumns := []sqlb.ColExpr{
		{Expr: sqlb.Ident("grid.id"), Alias: "id"},
		{Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"},
		{Expr: sqlb.RawLit{V: "count()"}, Alias: "sample_count"},
	}
	windowColumns = append(windowColumns, aggregateColumns...)
	windowed := &sqlb.Select{
		Columns: windowColumns,
		From:    sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: sqlb.RawSource{SQL: schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)), Alias: "d"}, Kind: "INNER", On: sqlb.RawLit{V: "d.id = grid.id"}},
		Where:   sqlb.RawLit{V: emit.RangeWindowTimeFilter("d.value")},
		GroupBy: []sqlb.Expr{sqlb.Ident("grid.id"), sqlb.Ident("grid.eval_ts")},
	}

	perStepFrom := sqlb.Source(sqlb.SubSelect{S: windowed})
	if selector.NeedTags {
		taggedColumns := []sqlb.ColExpr{
			{Expr: sqlb.Ident("series.tags"), Alias: "tags"},
			{Expr: sqlb.Ident("windowed.eval_ts"), Alias: "eval_ts"},
			{Expr: sqlb.Ident("windowed.sample_count"), Alias: "sample_count"},
		}
		for _, col := range aggregateColumns {
			taggedColumns = append(taggedColumns, sqlb.ColExpr{Expr: sqlb.Ident("windowed." + col.Alias), Alias: col.Alias})
		}
		tagged := &sqlb.Select{
			Columns: taggedColumns,
			From: sqlb.Join{
				Left:  sqlb.SubSelect{S: windowed, Alias: "windowed"},
				Right: sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
				Kind:  "INNER",
				On:    sqlb.RawLit{V: "windowed.id = series.id"},
			},
		}
		perStepFrom = sqlb.SubSelect{S: tagged}
	}
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: resolvedFinalTagsExpr, Alias: "final_tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: aggregateValueExpr}, Alias: "value"}},
		From:    perStepFrom,
		Where:   sqlb.RawLit{V: "sample_count > " + strconv.Itoa(minimumSeriesLength)},
	}
	return perStep, params, orderBy, nil
}

func buildRangeWindowSelectorCumulativeAvgPerStepSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, finalTagsSQL string, minimumSeriesLength int) (string, map[string]string, []sqlb.OrderExpr, error) {
	if selector.Kind != SelectorKindRangeVector {
		return "", nil, nil, fmt.Errorf("cumulative avg range-window selector SQL requires a range-vector selector, got %q", selector.Kind)
	}
	if stepMS <= 0 {
		return "", nil, nil, fmt.Errorf("cumulative avg range-window selector SQL requires a positive step")
	}
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_window_cumulative_avg", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, nil, err
	}
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	params["param_lookback_ms"] = strconv.FormatInt(selector.LookbackMS, 10)
	params["param_offset_ms"] = strconv.FormatInt(selector.OffsetMS, 10)

	resolvedFinalTagsExpr := emit.StripMetricName("tags")
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}}
	if !selector.NeedTags {
		resolvedFinalTagsExpr = "CAST([], '" + schema.TagsArrayType + "')"
		orderBy = nil
	} else if strings.TrimSpace(finalTagsSQL) != "" {
		resolvedFinalTagsExpr = finalTagsSQL
	}

	matchedSeries := rawSubquerySQL(matchedSeriesSQL)
	dataRef := schema.TimeSeriesDataRef(timeSeriesTableRef(cfg))
	statesSQL := "SELECT d.id AS id, d.timestamp AS timestamp, " +
		"sum(if(NOT isNaN(ifNull(toFloat64(d.value), nan)), ifNull(toFloat64(d.value), nan), 0.)) OVER (PARTITION BY d.id ORDER BY d.timestamp ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS finite_sum, " +
		"countIf(NOT isNaN(ifNull(toFloat64(d.value), nan))) OVER (PARTITION BY d.id ORDER BY d.timestamp ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS finite_count, " +
		"countIf(isNaN(ifNull(toFloat64(d.value), nan))) OVER (PARTITION BY d.id ORDER BY d.timestamp ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS nan_count " +
		"FROM " + dataRef + " AS d INNER JOIN " + matchedSeries + " AS series ON d.id = series.id " +
		"WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND " + staleNaNFilterSQL("d.value") + " " +
		"ORDER BY id, timestamp"
	gridSQL := "SELECT series.id AS id, arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts, " +
		"eval_ts - toIntervalMillisecond({offset_ms:Int64}) AS upper_bound, " +
		"eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64} + 1) AS lower_prev_bound " +
		"FROM " + matchedSeries + " AS series ORDER BY id, upper_bound"
	upperSQL := "SELECT grid.id AS id, grid.eval_ts AS eval_ts, ifNull(u.finite_sum, 0.) AS finite_sum, ifNull(u.finite_count, 0) AS finite_count, ifNull(u.nan_count, 0) AS nan_count " +
		"FROM " + rawSubquerySQL(gridSQL) + " AS grid ASOF LEFT JOIN " + rawSubquerySQL(statesSQL) + " AS u ON grid.id = u.id AND grid.upper_bound >= u.timestamp"
	lowerGridSQL := "SELECT series.id AS id, arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts, " +
		"eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64} + 1) AS lower_prev_bound " +
		"FROM " + matchedSeries + " AS series ORDER BY id, lower_prev_bound"
	lowerSQL := "SELECT grid.id AS id, grid.eval_ts AS eval_ts, ifNull(l.finite_sum, 0.) AS finite_sum, ifNull(l.finite_count, 0) AS finite_count, ifNull(l.nan_count, 0) AS nan_count " +
		"FROM " + rawSubquerySQL(lowerGridSQL) + " AS grid ASOF LEFT JOIN " + rawSubquerySQL(statesSQL) + " AS l ON grid.id = l.id AND grid.lower_prev_bound >= l.timestamp"
	windowedSQL := "SELECT upper.id AS id, upper.eval_ts AS eval_ts, " +
		"upper.finite_sum - lower.finite_sum AS finite_sum, " +
		"upper.finite_count - lower.finite_count AS finite_count, " +
		"upper.nan_count - lower.nan_count AS nan_count " +
		"FROM " + rawSubquerySQL(upperSQL) + " AS upper INNER JOIN " + rawSubquerySQL(lowerSQL) + " AS lower ON upper.id = lower.id AND upper.eval_ts = lower.eval_ts"
	if selector.NeedTags {
		windowedSQL = "SELECT series.tags AS tags, windowed.eval_ts AS eval_ts, windowed.finite_sum AS finite_sum, windowed.finite_count AS finite_count, windowed.nan_count AS nan_count FROM " + rawSubquerySQL(windowedSQL) + " AS windowed INNER JOIN " + matchedSeries + " AS series ON windowed.id = series.id"
	} else {
		windowedSQL = "SELECT CAST([], '" + schema.TagsArrayType + "') AS tags, eval_ts, finite_sum, finite_count, nan_count FROM " + rawSubquerySQL(windowedSQL)
	}
	perStepSQL := "SELECT " + resolvedFinalTagsExpr + " AS final_tags, eval_ts AS timestamp, if(nan_count > 0 OR finite_count = 0, nan, finite_sum / finite_count) AS value FROM " + rawSubquerySQL(windowedSQL) + " WHERE finite_count + nan_count > " + strconv.Itoa(minimumSeriesLength)
	return perStepSQL, params, orderBy, nil
}

func directRangeWindowAggregateSpec(fn string) ([]sqlb.ColExpr, string, error) {
	valueExpr := emit.NullableFloatCoerce("d.value")
	switch fn {
	case "avg_over_time":
		return []sqlb.ColExpr{
			{Expr: sqlb.RawLit{V: "countIf(isNaN(" + valueExpr + "))"}, Alias: "nan_count"},
			{Expr: sqlb.RawLit{V: "countIf(NOT isNaN(" + valueExpr + "))"}, Alias: "finite_count"},
			{Expr: sqlb.RawLit{V: "avgIf(" + valueExpr + ", NOT isNaN(" + valueExpr + "))"}, Alias: "avg_value"},
		}, "if(nan_count > 0 OR finite_count = 0, nan, avg_value)", nil
	case "rate":
		return []sqlb.ColExpr{
			{Expr: sqlb.RawLit{V: "countIf(isNaN(" + valueExpr + "))"}, Alias: "nan_count"},
			{Expr: sqlb.RawLit{V: "max(d.timestamp) - min(d.timestamp)"}, Alias: "window_duration_ms"},
			{Expr: sqlb.RawLit{V: "deltaSumTimestamp(" + valueExpr + ", toUnixTimestamp64Milli(d.timestamp))"}, Alias: "counter_delta_sum"},
		}, "if(nan_count > 0 OR sample_count <= 1 OR window_duration_ms <= 0, nan, counter_delta_sum / window_duration_ms)", nil
	default:
		return nil, "", fmt.Errorf("direct aggregate range-window selector SQL does not support %q", fn)
	}
}

func rangeWindowFunctionNeedsTimestamps(fn string) bool {
	switch fn {
	case "irate", "increase", "delta", "deriv", "predict_linear", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		return true
	default:
		return false
	}
}

func rangeWindowFunctionNeedsDuration(fn string) bool {
	return fn == "rate"
}

func rangeWindowFunctionNeedsValues(fn string) bool {
	switch fn {
	case "first_over_time", "last_over_time", "count_over_time", "present_over_time", "ts_of_first_over_time", "ts_of_last_over_time":
		return false
	default:
		return true
	}
}

func rangeWindowFunctionNeedsPairwiseNeighbors(fn string) bool {
	switch fn {
	case "rate", "increase", "changes":
		return true
	default:
		return false
	}
}

func rangeWindowFunctionNeedsCounterDelta(fn string) bool {
	switch fn {
	case "rate", "increase":
		return true
	default:
		return false
	}
}

func rangeWindowFunctionNeedsChangesCount(fn string) bool {
	return fn == "changes"
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
		columns = append([]sqlb.ColExpr{{Expr: emit.EmptyTagsArray(), Alias: "tags"}}, columns...)
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
	innerSQL, params, err := buildRangeInstantSelectorRowsSQL(cfg, selector, requiredStartMS, requiredEndMS, startMS, endMS, stepMS, "range_instant")
	if err != nil {
		return "", nil, err
	}
	outerTagsExpr := sqlb.Expr(sqlb.Ident("tags"))
	orderBy := []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}}
	if !selector.NeedTags {
		outerTagsExpr = emit.EmptyTagsArray()
		orderBy = nil
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: outerTagsExpr, Alias: "tags"},
			{Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"},
		},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(innerSQL)},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: orderBy,
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func buildRangeInstantSelectorRowsSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, startMS, endMS, stepMS int64, matcherPrefix string) (string, map[string]string, error) {
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, matcherPrefix, requiredStartMS, requiredEndMS, true)
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
	if !selector.NeedTags {
		gridTagsExpr = emit.EmptyTagsArray()
		innerTagsExpr = emit.EmptyTagsArray()
	}

	gridBase := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("series.id"), Alias: "id"},
			{Expr: gridTagsExpr, Alias: "tags"},
			{Expr: sqlb.RawLit{V: emit.GridEvalTSParams()}, Alias: "eval_ts"},
		},
		From: sqlb.RawSource{SQL: rawSubquerySQL(matchedSeriesSQL), Alias: "series"},
	}
	grid := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("grid_base.id"), Alias: "id"},
			{Expr: sqlb.Ident("grid_base.tags"), Alias: "tags"},
			{Expr: sqlb.Ident("grid_base.eval_ts"), Alias: "eval_ts"},
			{Expr: sqlb.RawLit{V: "grid_base.eval_ts - toIntervalMillisecond({offset_ms:Int64})"}, Alias: "eval_bound"},
		},
		From: sqlb.SubSelect{S: gridBase, Alias: "grid_base"},
	}
	rightRowsSQL := "SELECT id, timestamp, value FROM " + schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)) + " WHERE timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND id IN (SELECT id FROM (" + matchedSeriesSQL + ") AS matched_series_ids)"
	inner := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: innerTagsExpr, Alias: "tags"},
			{Expr: sqlb.Ident("grid.eval_ts"), Alias: "timestamp"},
			{Expr: sqlb.Ident("d.value"), Alias: "value"},
		},
		From: sqlb.Join{
			Left:  sqlb.SubSelect{S: grid, Alias: "grid"},
			Right: sqlb.RawSource{SQL: rawSubquerySQL(rightRowsSQL), Alias: "d"},
			Kind:  "ASOF INNER",
			On:    sqlb.RawLit{V: "grid.id = d.id AND grid.eval_bound >= d.timestamp"},
		},
		Where: sqlb.RawLit{V: "d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64}) AND NOT isNaN(value)"},
	}
	sql, _, err := inner.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func buildRangeMatrixSelectorSourceSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	innerSQL, params, err := buildRangeMatrixSelectorRowsSQL(cfg, selector, requiredStartMS, requiredEndMS)
	if err != nil {
		return "", nil, err
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"},
		},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(innerSQL)},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func buildRangeMatrixSelectorRowsSQL(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS int64) (string, map[string]string, error) {
	matchedSeriesSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "range_matrix", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	selectTagsExpr := sqlb.Expr(sqlb.Ident("series.tags"))
	if !selector.NeedTags {
		selectTagsExpr = emit.EmptyTagsArray()
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
	sql, _, err := inner.Build()
	if err != nil {
		return "", nil, err
	}
	return sql, params, nil
}

func selectorTagsExpr(cfg QueryConfig, selector SelectorSource, metricColumn, tagsColumn string) string {
	baseArgs := []sqlb.Expr{sqlb.RawLit{V: "[tuple('__name__', " + metricColumn + ")]"}}
	promotedLabels := make([]string, 0, len(cfg.PromotedTagColumns))
	for _, label := range SortedPromotedTagColumnNames(cfg.PromotedTagColumns) {
		if label == "__name__" {
			continue
		}
		if promoted := promotedTagColumn(cfg, label); promoted != "" {
			promotedLabels = append(promotedLabels, label)
			labelLit := sqlStringLiteral(label)
			baseArgs = append(baseArgs, sqlb.RawLit{V: "if(" + promoted + " != '', [tuple(" + labelLit + ", concat('', " + promoted + "))], CAST([], '" + schema.TagsArrayType + "'))"})
		}
	}
	mapEntries := sqlb.Expr(sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.RawLit{V: "(k, v) -> tuple(k, v)"},
		sqlb.Call{Name: "mapKeys", Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}}},
		sqlb.Call{Name: "mapValues", Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}}},
	}})
	if len(promotedLabels) > 0 {
		mapEntries = sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
			sqlb.RawLit{V: "tag -> NOT has(" + sqlStringArrayLiteral(promotedLabels) + ", tag.1)"},
			mapEntries,
		}}
	}
	baseArgs = append(baseArgs, mapEntries)
	base := sqlb.Call{Name: "arrayConcat", Args: baseArgs}
	if !selector.RequireFullTags && len(selector.RequiredTagLabels) > 0 {
		if len(selector.RequiredTagLabels) == 1 {
			label := selector.RequiredTagLabels[0]
			if label == "__name__" {
				return "[tuple('__name__', " + metricColumn + ")]"
			}
			labelLit := sqlStringLiteral(label)
			if promoted := promotedTagColumn(cfg, label); promoted != "" {
				return "if(" + promoted + " != '', [tuple(" + labelLit + ", concat('', " + promoted + "))], CAST([], '" + schema.TagsArrayType + "'))"
			}
			valueExpr := tagsColumn + "[" + labelLit + "]"
			return "if(mapContains(" + tagsColumn + ", " + labelLit + "), [tuple(" + labelLit + ", concat('', " + valueExpr + "))], CAST([], '" + schema.TagsArrayType + "'))"
		}
		filtered := sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
			sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(selector.RequiredTagLabels) + ", tag.1)"},
			base,
		}}
		return renderStorageExprNoParams(sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
			filtered,
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
		matcher, err := labels.NewMatcher(labels.MatchEqual, "__name__", selector.MetricName)
		if err != nil {
			return "", nil, err
		}
		clause, extraParams := compileMatcherClause(cfg, prefix, matcherIndex, metricColumn, tagsColumn, matcher)
		matcherIndex++
		whereClauses = append(whereClauses, clause)
		mergeParams(params, extraParams)
	}
	for _, matcher := range selector.Matchers {
		if matcher == nil {
			continue
		}
		if selector.MetricName != "" && matcher.Name == "__name__" && matcher.Type == labels.MatchEqual && matcher.Value == selector.MetricName {
			continue
		}
		clause, extraParams := compileMatcherClause(cfg, prefix, matcherIndex, metricColumn, tagsColumn, matcher)
		matcherIndex++
		whereClauses = append(whereClauses, clause)
		mergeParams(params, extraParams)
	}
	if addTimeOverlap {
		whereClauses = append(whereClauses, "src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64})", "src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})")
	}
	columns := []sqlb.ColExpr{{Expr: sqlb.Ident("src.id")}}
	if selector.NeedTags {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: selectorTagsExpr(cfg, selector, metricColumn, tagsColumn)}, Alias: "tags"})
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

func compileMatcherClause(cfg QueryConfig, prefix string, matcherIndex int, metricColumn, tagsColumn string, matcher *labels.Matcher) (string, map[string]string) {
	columnExpr := sqlb.Expr(sqlb.RawLit{V: metricColumn})
	params := map[string]string{}
	if matcher.Name != "__name__" {
		if promoted := promotedTagColumn(cfg, matcher.Name); promoted != "" {
			columnExpr = sqlb.RawLit{V: promoted}
		} else {
			keyName := prefix + "_matcher_" + strconv.Itoa(matcherIndex) + "_key"
			columnExpr = sqlb.Subscr{Array: sqlb.RawLit{V: tagsColumn}, Index: sqlb.Call{Name: "concat", Args: []sqlb.Expr{sqlb.RawLit{V: "''"}, sqlb.Param{Name: keyName, Type: "String", V: matcher.Name}}}}
		}
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

func promotedTagColumn(cfg QueryConfig, label string) string {
	if cfg.PromotedTagColumns == nil {
		return ""
	}
	if _, ok := cfg.PromotedTagColumns[label]; !ok {
		return ""
	}
	return "src.`" + escapeIdentifier(label) + "`"
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
