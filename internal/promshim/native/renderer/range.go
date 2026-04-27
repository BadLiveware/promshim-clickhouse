package renderer

import (
	"fmt"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"strconv"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func preferDirectSelectorWindowJoin(lookbackMS, stepMS int64) bool {
	if lookbackMS <= 0 || stepMS <= 0 {
		return false
	}
	// The direct window-join path duplicates raw points into every overlapping
	// step bucket. That is a good trade when overlap is shallow (for example
	// 1m windows on a 30s step), but once each point fan-outs across many step
	// buckets the older materialize-then-window path is cheaper. Keep the generic
	// fast path for low-overlap windows only; aggregate-specific helpers can opt
	// into direct grouped aggregates when they avoid per-step array materialization.
	overlapSlots := ((lookbackMS + stepMS - 1) / stepMS) + 1
	return overlapSlots <= 4
}

func preferDirectSelectorWindowAggregate(fn string, lookbackMS, stepMS int64) bool {
	if lookbackMS <= 0 || stepMS <= 0 {
		return false
	}
	if fn == "avg_over_time" {
		return true
	}
	return preferDirectSelectorWindowJoin(lookbackMS, stepMS)
}

func buildWindowedArraysSourceSQL(sourceSQL, fn string, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: emit.GridEvalTSLiteral(startMS, endMS, stepMS)}, Alias: "eval_ts"}}}
	windowColumns := []sqlb.ColExpr{
		{Expr: sqlb.Ident("source.tags"), Alias: "tags"},
		{Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"},
		{Expr: sqlb.RawLit{V: "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS, 10) + ") AND tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS+rangeMS, 10) + "), source.time_series)"}, Alias: "window_series"},
	}
	if rangeWindowSourceNeedsTimestamps(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: emit.WindowPointTimestamps("window_series")}, Alias: "window_timestamps"})
	}
	if rangeWindowSourceNeedsDuration(fn) {
		durationExpr := "tupleElement(arrayElement(window_series, length(window_series)), 1) - tupleElement(arrayElement(window_series, 1), 1)"
		if rangeWindowSourceNeedsTimestamps(fn) {
			durationExpr = "arrayElement(window_timestamps, length(window_series)) - arrayElement(window_timestamps, 1)"
		}
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: durationExpr}, Alias: "window_duration_ms"})
	}
	if rangeWindowSourceNeedsValues(fn) {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: emit.WindowPointValues("window_series")}, Alias: "window_values"})
	}
	needsChangesCount := rangeWindowSourceNeedsChangesCount(fn)
	if rangeWindowSourceNeedsPairwiseNeighbors(fn) && needsChangesCount {
		windowColumns = append(windowColumns,
			sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayPopBack(window_values)"}, Alias: "window_values_prev"},
			sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayPopFront(window_values)"}, Alias: "window_values_cur"},
		)
	}
	if rangeWindowSourceNeedsCounterDelta(fn) {
		counterDeltaExpr := "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), arrayPopBack(window_values), arrayPopFront(window_values)))"
		if needsChangesCount {
			counterDeltaExpr = "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), window_values_prev, window_values_cur))"
		}
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: counterDeltaExpr}, Alias: "counter_delta_sum"})
	}
	if needsChangesCount {
		windowColumns = append(windowColumns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "toFloat64(arraySum(arrayMap((p, c) -> if(c != p, 1, 0), window_values_prev, window_values_cur)))"}, Alias: "changes_count"})
	}
	stepWindows := &sqlb.Select{
		Columns: windowColumns,
		From:    sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: rawRenderedSubquerySourceWithAlias(sourceSQL, "source"), Kind: "CROSS"},
	}
	return buildNativeWrapperSQL(stepWindows)
}

func buildWindowedRowsSourceSQL(sourceRowsSQL string, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: emit.GridEvalTSLiteral(startMS, endMS, stepMS)}, Alias: "eval_ts"}}}
	stepWindows := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("source.tags"), Alias: "tags"},
			{Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"},
			{Expr: sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((source.timestamp, source.value)))"}, Alias: "window_series"},
			{Expr: sqlb.RawLit{V: emit.WindowPointTimestamps("window_series")}, Alias: "window_timestamps"},
			{Expr: sqlb.RawLit{V: "arrayElement(window_timestamps, length(window_series)) - arrayElement(window_timestamps, 1)"}, Alias: "window_duration_ms"},
			{Expr: sqlb.RawLit{V: emit.WindowPointValues("window_series")}, Alias: "window_values"},
			{Expr: sqlb.RawLit{V: "arrayPopBack(window_values)"}, Alias: "window_values_prev"},
			{Expr: sqlb.RawLit{V: "arrayPopFront(window_values)"}, Alias: "window_values_cur"},
			{Expr: sqlb.RawLit{V: "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), window_values_prev, window_values_cur))"}, Alias: "counter_delta_sum"},
			{Expr: sqlb.RawLit{V: "toFloat64(arraySum(arrayMap((p, c) -> if(c != p, 1, 0), window_values_prev, window_values_cur)))"}, Alias: "changes_count"},
		},
		From:    sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: rawRenderedSubquerySourceWithAlias(sourceRowsSQL, "source"), Kind: "CROSS"},
		Where:   sqlb.RawLit{V: "source.timestamp <= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS, 10) + ") AND source.timestamp >= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS+rangeMS, 10) + ")"},
		GroupBy: []sqlb.Expr{sqlb.Ident("source.tags"), sqlb.Ident("grid.eval_ts")},
	}
	return buildNativeWrapperSQL(stepWindows)
}

func buildRangeFunctionOverWindowedArraysSQL(sourceSQL, fn, finalTagsExpr string, paramNumber *float64, paramNumbers []*float64, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	windowedSourceSQL, err := buildWindowedArraysSourceSQL(sourceSQL, fn, startMS, endMS, stepMS, rangeMS, offsetMS)
	if err != nil {
		return "", err
	}
	return buildRangeFunctionOverWindowedSourceSQL(windowedSourceSQL, fn, finalTagsExpr, paramNumber, paramNumbers, rangeMS)
}

func buildRangeFunctionOverWindowedRowsSQL(sourceRowsSQL, fn string, paramNumber *float64, paramNumbers []*float64, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	windowedSourceSQL, err := buildWindowedRowsSourceSQL(sourceRowsSQL, startMS, endMS, stepMS, rangeMS, offsetMS)
	if err != nil {
		return "", err
	}
	return buildRangeFunctionOverWindowedSourceSQL(windowedSourceSQL, fn, rangeFunctionTagsExpr(fn), paramNumber, paramNumbers, rangeMS)
}

func rangeWindowSourceNeedsTimestamps(fn string) bool {
	switch fn {
	case "rate", "irate", "increase", "delta", "deriv", "predict_linear", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		return true
	default:
		return false
	}
}

func rangeWindowSourceNeedsDuration(fn string) bool {
	return fn == "rate"
}

func rangeWindowSourceNeedsValues(fn string) bool {
	switch fn {
	case "first_over_time", "last_over_time", "count_over_time", "present_over_time", "ts_of_first_over_time", "ts_of_last_over_time", "absent_over_time":
		return false
	default:
		return true
	}
}

func rangeWindowSourceNeedsPairwiseNeighbors(fn string) bool {
	switch fn {
	case "rate", "increase", "changes", "resets":
		return true
	default:
		return false
	}
}

func rangeWindowSourceNeedsCounterDelta(fn string) bool {
	switch fn {
	case "rate", "increase":
		return true
	default:
		return false
	}
}

func rangeWindowSourceNeedsChangesCount(fn string) bool {
	return fn == "changes"
}

func buildRangeFunctionOverWindowedSourceSQL(windowedSourceSQL, fn, finalTagsExpr string, paramNumber *float64, paramNumbers []*float64, rangeMS int64) (string, error) {
	valueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", rangeMS)
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: finalTagsExpr}, Alias: "final_tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"}},
		From:    rawRenderedSubquerySourceWithAlias(trimRenderedQuerySQL(windowedSourceSQL), "step_windows"),
		Where:   sqlb.RawLit{V: "length(window_series) > " + strconv.Itoa(minimumSeriesLengthForRangeFunction(fn))},
	}
	timeSeriesExpr := emit.SortedTimeSeriesGroupArray()
	if fn == "rate" {
		timeSeriesExpr = sqlb.RawLit{V: "arraySort(groupArray((timestamp, value)))"}
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: timeSeriesExpr, Alias: "time_series"}},
		From:    sqlb.SubSelect{S: perStep},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}},
	}
	return buildNativeWrapperSQL(outer)
}

func canUseInstantRangeFunctionRowsFastPath(fn string) bool {
	switch fn {
	case "sum_over_time", "avg_over_time", "max_over_time":
		return true
	default:
		return false
	}
}

func canUseRangeFunctionRowsFastPath(fn string) bool {
	switch fn {
	case "sum_over_time", "max_over_time":
		return true
	default:
		return false
	}
}

func supportsDirectSelectorWindowAggregate(fn string, lookbackMS int64) bool {
	return fn == "avg_over_time"
}

func canUseNativeGridRangeFunction(fn string, lookbackMS, offsetMS int64) bool {
	if lookbackMS <= 0 || offsetMS != 0 {
		return false
	}
	switch fn {
	case "rate", "irate", "delta", "idelta", "last_over_time":
		// Very short windows have compliance-sensitive empty-window behavior in
		// Prometheus. Keep them on promshim's SQL kernel until targeted fixtures
		// prove ClickHouse's grid functions are identical there too.
		return lookbackMS >= 60_000
	default:
		return false
	}
}

func buildInstantRangeFunctionOverRowsSQL(sourceRowsSQL, fn, finalTagsExpr string, evaluationTimeMS int64) (string, error) {
	valueExpr, err := rangeFunctionRowsFastPathValueExpr(fn, "value")
	if err != nil {
		return "", fmt.Errorf("instant row fast path for %s is not implemented yet", fn)
	}
	prepared := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: finalTagsExpr}, Alias: "final_tags"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
		From:    rawRenderedSubquerySource(trimRenderedQuerySQL(sourceRowsSQL)),
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: sqlb.RawLit{V: "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"}, Alias: "timestamp"}, {Expr: valueExpr, Alias: "value"}},
		From:    sqlb.SubSelect{S: prepared},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}},
	}
	return buildNativeWrapperSQL(outer)
}

func buildInstantRateOverRowsSQL(sourceRowsSQL, finalTagsExpr string, evaluationTimeMS, rangeMS int64) (string, error) {
	valueExpr := emit.NullableFloatCoerce("value")
	prepared := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.RawLit{V: finalTagsExpr}, Alias: "final_tags"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"},
		},
		From: rawRenderedSubquerySource(trimRenderedQuerySQL(sourceRowsSQL)),
	}
	groupedFrom := sqlb.Source(sqlb.SubSelect{S: prepared})
	counterDeltaExpr := "deltaSumTimestamp(" + valueExpr + ", toUnixTimestamp64Milli(timestamp))"
	if rangeMS < 60_000 {
		annotated := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.Ident("final_tags"), Alias: "final_tags"},
				{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
				{Expr: sqlb.Ident("value"), Alias: "value"},
				{Expr: sqlb.RawLit{V: "lagInFrame(value, 1, nan) OVER (PARTITION BY final_tags ORDER BY timestamp)"}, Alias: "prev_value"},
			},
			From: sqlb.SubSelect{S: prepared},
		}
		groupedFrom = sqlb.SubSelect{S: annotated}
		counterDeltaExpr = "sum(if(isNaN(value) OR isNaN(prev_value), toFloat64(0), if(value < prev_value, value, value - prev_value)))"
	}
	grouped := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("final_tags"), Alias: "final_tags"},
			{Expr: sqlb.RawLit{V: "count()"}, Alias: "sample_count"},
			{Expr: sqlb.RawLit{V: "countIf(isNaN(value))"}, Alias: "nan_count"},
			{Expr: sqlb.RawLit{V: "toUnixTimestamp64Milli(min(timestamp))"}, Alias: "first_timestamp_ms"},
			{Expr: sqlb.RawLit{V: "toUnixTimestamp64Milli(max(timestamp))"}, Alias: "last_timestamp_ms"},
			{Expr: sqlb.RawLit{V: "toUnixTimestamp64Milli(max(timestamp)) - toUnixTimestamp64Milli(min(timestamp))"}, Alias: "range_duration_ms"},
			{Expr: sqlb.RawLit{V: counterDeltaExpr}, Alias: "range_counter_delta_sum"},
		},
		From:    groupedFrom,
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
	}
	factor := scalarExtrapolationFactorSQL("first_timestamp_ms", "last_timestamp_ms", "sample_count", strconv.FormatInt(evaluationTimeMS, 10), rangeMS)
	rangeSeconds := storage.NativeFloatLiteral(float64(rangeMS) / 1000.0)
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("final_tags"), Alias: "tags"},
			{Expr: sqlb.RawLit{V: "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"}, Alias: "timestamp"},
			{Expr: sqlb.RawLit{V: "if(nan_count > 0 OR sample_count <= 1 OR range_duration_ms <= 0, nan, (range_counter_delta_sum) * (" + factor + ") / (" + rangeSeconds + "))"}, Alias: "value"},
		},
		From:    sqlb.SubSelect{S: grouped},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}},
	}
	return buildNativeWrapperSQL(outer)
}

func buildRangeFunctionOverRowsSQL(sourceRowsSQL, fn, finalTagsExpr string, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	valueExpr, err := rangeFunctionRowsFastPathValueExpr(fn, "source.value")
	if err != nil {
		return "", fmt.Errorf("range row fast path for %s is not implemented yet", fn)
	}
	grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: emit.GridEvalTSLiteral(startMS, endMS, stepMS)}, Alias: "eval_ts"}}}
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.RawLit{V: finalTagsExpr}, Alias: "final_tags"},
			{Expr: sqlb.Ident("grid.eval_ts"), Alias: "timestamp"},
			{Expr: valueExpr, Alias: "value"},
		},
		From:    sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: rawRenderedSubquerySourceWithAlias(sourceRowsSQL, "source"), Kind: "CROSS"},
		Where:   sqlb.RawLit{V: "source.timestamp <= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS, 10) + ") AND source.timestamp >= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS+rangeMS, 10) + ")"},
		GroupBy: []sqlb.Expr{sqlb.Ident("source.tags"), sqlb.Ident("grid.eval_ts")},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
		From:    sqlb.SubSelect{S: perStep},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}},
	}
	return buildNativeWrapperSQL(outer)
}

func rangeFunctionRowsFastPathValueExpr(fn, valueIdent string) (sqlb.Expr, error) {
	switch fn {
	case "sum_over_time":
		return sqlb.RawLit{V: "if(countIf(isNaN(" + valueIdent + ")) > 0, nan, sumIf(" + valueIdent + ", NOT isNaN(" + valueIdent + ")))"}, nil
	case "avg_over_time":
		return sqlb.RawLit{V: "if(countIf(isNaN(" + valueIdent + ")) > 0 OR countIf(NOT isNaN(" + valueIdent + ")) = 0, nan, avgIf(" + valueIdent + ", NOT isNaN(" + valueIdent + ")))"}, nil
	case "max_over_time":
		return sqlb.RawLit{V: "if(countIf(isNaN(" + valueIdent + ")) > 0 OR countIf(NOT isNaN(" + valueIdent + ")) = 0, nan, maxIf(" + valueIdent + ", NOT isNaN(" + valueIdent + ")))"}, nil
	default:
		return nil, fmt.Errorf("row fast path for %s is not implemented yet", fn)
	}
}

func buildInstantRangeFunctionSQL(sourceSQL, fn, tagsExpr string, paramNumber *float64, paramNumbers []*float64, evaluationTimeMS int64, rangeMS int64) (string, error) {
	timestampExpr := "tupleElement(arrayElement(time_series, length(time_series)), 1)"
	if fn == "predict_linear" {
		timestampExpr = "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"
	}
	columns := []sqlb.ColExpr{
		{Expr: sqlb.Ident("time_series"), Alias: "time_series"},
		{Expr: sqlb.RawLit{V: tagsExpr}, Alias: "tags"},
		{Expr: sqlb.RawLit{V: timestampExpr}, Alias: "timestamp"},
	}
	if instantRangeFunctionNeedsTimestamps(fn) {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayMap(point -> tupleElement(point, 1), time_series)"}, Alias: "range_timestamps"})
	}
	if instantRangeFunctionNeedsDuration(fn) {
		durationExpr := "tupleElement(arrayElement(time_series, length(time_series)), 1) - tupleElement(arrayElement(time_series, 1), 1)"
		if instantRangeFunctionNeedsTimestamps(fn) {
			durationExpr = "arrayElement(range_timestamps, length(time_series)) - arrayElement(range_timestamps, 1)"
		}
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: durationExpr}, Alias: "range_duration_ms"})
	}
	if instantRangeFunctionNeedsValues(fn) {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: emit.WindowPointValues("time_series")}, Alias: "range_values"})
	}
	if instantRangeFunctionNeedsHasNaN(fn) {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayExists(v -> isNaN(v), range_values)"}, Alias: "range_has_nan"})
	}
	if instantRangeFunctionNeedsFiniteValues(fn) {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayFilter(v -> NOT isNaN(v), range_values)"}, Alias: "range_values_finite"})
	}
	needsChangesCount := instantRangeFunctionNeedsChangesCount(fn)
	if instantRangeFunctionNeedsPairwiseNeighbors(fn) && needsChangesCount {
		columns = append(columns,
			sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayPopBack(range_values)"}, Alias: "range_values_prev"},
			sqlb.ColExpr{Expr: sqlb.RawLit{V: "arrayPopFront(range_values)"}, Alias: "range_values_cur"},
		)
	}
	if instantRangeFunctionNeedsCounterDelta(fn) {
		counterDeltaExpr := "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), arrayPopBack(range_values), arrayPopFront(range_values)))"
		if needsChangesCount {
			counterDeltaExpr = "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), range_values_prev, range_values_cur))"
		}
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: counterDeltaExpr}, Alias: "range_counter_delta_sum"})
	}
	if needsChangesCount {
		columns = append(columns, sqlb.ColExpr{Expr: sqlb.RawLit{V: "toFloat64(arraySum(arrayMap((p, c) -> if(c != p, 1, 0), range_values_prev, range_values_cur)))"}, Alias: "range_changes_count"})
	}
	bound := &sqlb.Select{
		Columns: columns,
		From:    rawRenderedSubquerySource(sourceSQL),
		Where:   sqlb.RawLit{V: "length(time_series) > " + strconv.Itoa(minimumSeriesLengthForRangeFunction(fn))},
	}
	valuesSource := ""
	if instantRangeFunctionNeedsValues(fn) {
		valuesSource = "range_values"
	}
	timestampsSource := "arrayMap(point -> tupleElement(point, 1), time_series)"
	if instantRangeFunctionNeedsTimestamps(fn) {
		timestampsSource = "range_timestamps"
	}
	valueExpr := rangeFunctionValueExpr(fn, "time_series", valuesSource, paramNumber, paramNumbers, timestampsSource, strconv.FormatInt(evaluationTimeMS, 10), rangeMS)
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"}},
		From:    sqlb.SubSelect{S: bound},
	}
	return buildNativeWrapperSQL(query)
}

func instantRangeFunctionNeedsValues(fn string) bool {
	switch fn {
	case "first_over_time", "last_over_time", "count_over_time", "present_over_time", "ts_of_first_over_time", "ts_of_last_over_time":
		return false
	default:
		return true
	}
}

func instantRangeFunctionNeedsTimestamps(fn string) bool {
	switch fn {
	case "rate", "irate", "increase", "delta", "deriv", "predict_linear", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		return true
	default:
		return false
	}
}

func instantRangeFunctionNeedsDuration(fn string) bool {
	return fn == "rate"
}

func instantRangeFunctionNeedsHasNaN(fn string) bool {
	switch fn {
	case "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "stddev_over_time", "stdvar_over_time", "rate", "irate", "increase", "changes", "deriv":
		return true
	default:
		return false
	}
}

func instantRangeFunctionNeedsFiniteValues(fn string) bool {
	switch fn {
	case "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "resets":
		return true
	default:
		return false
	}
}

func instantRangeFunctionNeedsPairwiseNeighbors(fn string) bool {
	switch fn {
	case "rate", "increase", "changes":
		return true
	default:
		return false
	}
}

func instantRangeFunctionNeedsCounterDelta(fn string) bool {
	switch fn {
	case "rate", "increase":
		return true
	default:
		return false
	}
}

func instantRangeFunctionNeedsChangesCount(fn string) bool {
	return fn == "changes"
}

func rangeFunctionTagsExpr(fn string) string {
	if native.RangeFunctionPreservesMetricName(fn) {
		return "tags"
	}
	return emit.StripMetricName("tags")
}

func rangeFunctionTagsExprFromInput(fn string, inputHasMetricName bool) string {
	if native.RangeFunctionPreservesMetricName(fn) || !inputHasMetricName {
		return "tags"
	}
	return rangeFunctionTagsExpr(fn)
}

// paramsInputHasMetricName reports whether the storage selector produced
// under the given RenderParams narrowing retains __name__ in its tags
// output. After 13c-14e native.SelectorSource no longer narrows; the
// tag-set observable at a range-function boundary is therefore a pure
// function of RenderParams:
//
//   - params.RequireFullTags=true or no narrowing at all → the selector
//     emits full tags (base path in selector_sql.go:selectorTagsExpr),
//     which include __name__.
//   - params.RequiredTagLabels non-empty → the selector narrows to
//     exactly that label set, so __name__ is present iff the set
//     contains "__name__".
func paramsInputHasMetricName(params RenderParams) bool {
	if params.RequireFullTags {
		return true
	}
	if len(params.RequiredTagLabels) == 0 {
		return true
	}
	for _, label := range params.RequiredTagLabels {
		if label == "__name__" {
			return true
		}
	}
	return false
}

// extrapolationFactorSQL builds a ClickHouse expression for Prometheus's
// rate/delta/increase extrapolation factor. Returns 1.0 when rangeMS <= 0
// (caller unable to supply window duration) or the sample count is insufficient.
// Mirrors extrapolatedRate in prometheus/promql/functions.go.
func extrapolationFactorSQL(timestampsExpr sqlb.Expr, seriesLength sqlb.Expr, evalTimeMSExpr string, rangeMS int64) string {
	if rangeMS <= 0 {
		return "1.0"
	}
	tsSQL := renderSQLExprNoParams(timestampsExpr)
	lenSQL := renderSQLExprNoParams(seriesLength)
	firstMS := "toUnixTimestamp64Milli(arrayElement(" + tsSQL + ", 1))"
	lastMS := "toUnixTimestamp64Milli(arrayElement(" + tsSQL + ", " + lenSQL + "))"
	return scalarExtrapolationFactorSQL(firstMS, lastMS, lenSQL, evalTimeMSExpr, rangeMS)
}

func scalarExtrapolationFactorSQL(firstMSExpr, lastMSExpr, sampleCountExpr, evalTimeMSExpr string, rangeMS int64) string {
	if rangeMS <= 0 {
		return "1.0"
	}
	firstMS := "toFloat64(" + firstMSExpr + ")"
	lastMS := "toFloat64(" + lastMSExpr + ")"
	sampledMS := "((" + lastMS + ") - (" + firstMS + "))"
	avgMS := "((" + sampledMS + ") / greatest(toFloat64(" + sampleCountExpr + ") - 1, 1))"
	threshold := "((" + avgMS + ") * 1.1)"
	rangeStart := "((" + evalTimeMSExpr + ") - " + strconv.FormatInt(rangeMS, 10) + ".0)"
	rangeEnd := "(" + evalTimeMSExpr + ")"
	gapStart := "((" + firstMS + ") - " + rangeStart + ")"
	gapEnd := "(" + rangeEnd + " - (" + lastMS + "))"
	addStart := "if(" + gapStart + " < " + threshold + ", " + gapStart + ", (" + avgMS + ") / 2)"
	addEnd := "if(" + gapEnd + " < " + threshold + ", " + gapEnd + ", (" + avgMS + ") / 2)"
	extrapolateTo := "((" + sampledMS + ") + (" + addStart + ") + (" + addEnd + "))"
	return "if((" + sampledMS + ") <= 0, 1.0, (" + extrapolateTo + ") / (" + sampledMS + "))"
}

func rangeFunctionValueExpr(fn, seriesExpr, valuesSourceExpr string, paramNumber *float64, paramNumbers []*float64, timestampsSourceExpr string, interceptTimeMSExpr string, rangeMS int64) string {
	series := sqlb.RawLit{V: seriesExpr}
	valuesExpr := sqlb.Expr(sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "point -> " + emit.NullableFloatCoerce("tupleElement(point, 2)")}, series}})
	if valuesSourceExpr != "" {
		valuesExpr = sqlb.RawLit{V: valuesSourceExpr}
	}
	timestampsExpr := sqlb.RawLit{V: timestampsSourceExpr}
	hasNaN := sqlb.Expr(sqlb.Call{Name: "arrayExists", Args: []sqlb.Expr{sqlb.Lambda{Params: []sqlb.Ident{"v"}, Body: sqlb.Call{Name: "isNaN", Args: []sqlb.Expr{sqlb.Ident("v")}}}, valuesExpr}})
	finiteValues := sqlb.Expr(sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.Lambda{Params: []sqlb.Ident{"v"}, Body: sqlb.RawLit{V: "NOT isNaN(v)"}}, valuesExpr}})
	seriesLength := sqlb.Expr(sqlb.Call{Name: "length", Args: []sqlb.Expr{series}})
	if valuesSourceExpr == "range_values" {
		hasNaN = sqlb.Ident("range_has_nan")
		finiteValues = sqlb.Ident("range_values_finite")
	}
	lenMinusOne := sqlb.RawLit{V: renderSQLExprNoParams(seriesLength) + " - 1"}

	arrayElementAtLength := func(expr sqlb.Expr) sqlb.Expr {
		return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{expr, seriesLength}}
	}
	arrayElementAtLengthMinusOne := func(expr sqlb.Expr) sqlb.Expr {
		return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{expr, lenMinusOne}}
	}
	prevValues := sqlb.Expr(sqlb.Call{Name: "arrayPopBack", Args: []sqlb.Expr{valuesExpr}})
	curValues := sqlb.Expr(sqlb.Call{Name: "arrayPopFront", Args: []sqlb.Expr{valuesExpr}})
	counterDeltaExpr := sqlb.Expr(sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(p, c) -> if(c < p, c, c - p)"}, prevValues, curValues}}}})
	changesExpr := sqlb.Expr(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(p, c) -> if(c != p, 1, 0)"}, prevValues, curValues}}}}}})
	if valuesSourceExpr == "window_values" {
		counterDeltaExpr = sqlb.Ident("counter_delta_sum")
		changesExpr = sqlb.Ident("changes_count")
	}
	if valuesSourceExpr == "range_values" {
		counterDeltaExpr = sqlb.Ident("range_counter_delta_sum")
		changesExpr = sqlb.Ident("range_changes_count")
	}
	lenFiniteIsZero := sqlb.Binary{Op: "=", L: sqlb.Call{Name: "length", Args: []sqlb.Expr{finiteValues}}, R: sqlb.RawLit{V: "0"}}
	interpolatedQuantileExpr := func(q float64, valuesSQL string) string {
		qLit := storage.NativeFloatLiteral(q)
		sortedExpr := "arrayConcat(arrayFilter(v -> isNaN(v), " + valuesSQL + "), arraySort(arrayFilter(v -> NOT isNaN(v), " + valuesSQL + ")))"
		lengthExpr := "length(" + sortedExpr + ")"
		rankExpr := "(" + qLit + ") * (toFloat64(" + lengthExpr + ") - 1)"
		lowerIndexExpr := "greatest(1, toInt64(floor(" + rankExpr + ")) + 1)"
		upperIndexExpr := "least(toInt64(" + lengthExpr + "), (" + lowerIndexExpr + ") + 1)"
		weightExpr := "(" + rankExpr + ") - floor(" + rankExpr + ")"
		lowerValueExpr := "toFloat64(arrayElement(" + sortedExpr + ", " + lowerIndexExpr + "))"
		upperValueExpr := "toFloat64(arrayElement(" + sortedExpr + ", " + upperIndexExpr + "))"
		return "multiIf(" + lengthExpr + " = 0, nan, isNaN(" + qLit + "), nan, (" + qLit + ") < 0, -inf, (" + qLit + ") > 1, inf, (" + lowerValueExpr + ") * (1 - (" + weightExpr + ")) + (" + upperValueExpr + ") * (" + weightExpr + "))"
	}

	switch fn {
	case "last_over_time":
		return renderSQLExprNoParams(sqlb.TupleElem{X: sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{series, seriesLength}}, K: 2})
	case "first_over_time":
		return renderSQLExprNoParams(sqlb.TupleElem{X: sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{series, sqlb.RawLit{V: "1"}}}, K: 2})
	case "sum_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{hasNaN, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{finiteValues}}}})
	case "avg_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "OR", L: hasNaN, R: lenFiniteIsZero}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayAvg", Args: []sqlb.Expr{finiteValues}}}})
	case "min_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "OR", L: hasNaN, R: lenFiniteIsZero}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayMin", Args: []sqlb.Expr{finiteValues}}}})
	case "max_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "OR", L: hasNaN, R: lenFiniteIsZero}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayMax", Args: []sqlb.Expr{finiteValues}}}})
	case "count_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{seriesLength}})
	case "stddev_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR " + renderSQLExprNoParams(seriesLength) + " = 0"}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'stddevPop'"}, valuesExpr}}}})
	case "stdvar_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR " + renderSQLExprNoParams(seriesLength) + " = 0"}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'varPop'"}, valuesExpr}}}})
	case "present_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.RawLit{V: "1"}}})
	case "ts_of_first_over_time":
		return "toFloat64(toUnixTimestamp64Milli(arrayElement(" + renderSQLExprNoParams(timestampsExpr) + ", 1))) / 1000.0"
	case "ts_of_last_over_time":
		return "toFloat64(toUnixTimestamp64Milli(arrayElement(" + renderSQLExprNoParams(timestampsExpr) + ", " + renderSQLExprNoParams(seriesLength) + "))) / 1000.0"
	case "ts_of_max_over_time", "ts_of_min_over_time":
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		timestampSecondsSQL := "arrayMap(ts -> toFloat64(toUnixTimestamp64Milli(ts)) / 1000.0, " + renderSQLExprNoParams(timestampsExpr) + ")"
		compare := "v >= tupleElement(acc, 1)"
		if fn == "ts_of_min_over_time" {
			compare = "v <= tupleElement(acc, 1)"
		}
		foldExpr := "arrayFold((acc, ts, v) -> if((" + compare + ") OR isNaN(tupleElement(acc, 1)), (v, ts), acc), " + timestampSecondsSQL + ", " + valuesSQL + ", (nan, toFloat64(0)))"
		return "tupleElement(" + foldExpr + ", 2)"
	case "mad_over_time":
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		medianExpr := interpolatedQuantileExpr(0.5, valuesSQL)
		deviationsExpr := "arrayMap(x -> abs(x - (" + medianExpr + ")), " + valuesSQL + ")"
		return interpolatedQuantileExpr(0.5, deviationsExpr)
	case "quantile_over_time":
		if paramNumber == nil {
			return "nan"
		}
		return interpolatedQuantileExpr(*paramNumber, renderSQLExprNoParams(valuesExpr))
	case "increase":
		factor := extrapolationFactorSQL(timestampsExpr, seriesLength, interceptTimeMSExpr, rangeMS)
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(counterDeltaExpr) + ") * (" + factor + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{hasNaN, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "changes":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{hasNaN, sqlb.RawLit{V: "nan"}, changesExpr}})
	case "resets":
		finitePairsExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(prev, cur) -> if(cur < prev, 1, 0)"}, sqlb.Call{Name: "arrayPopBack", Args: []sqlb.Expr{finiteValues}}, sqlb.Call{Name: "arrayPopFront", Args: []sqlb.Expr{finiteValues}}}}
		return renderSQLExprNoParams(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{finitePairsExpr}}}})
	case "rate":
		durationExpr := sqlb.Expr(sqlb.RawLit{V: renderSQLExprNoParams(arrayElementAtLength(timestampsExpr)) + " - arrayElement(" + renderSQLExprNoParams(timestampsExpr) + ", 1)"})
		if valuesSourceExpr == "window_values" {
			durationExpr = sqlb.Ident("window_duration_ms")
		}
		if valuesSourceExpr == "range_values" {
			durationExpr = sqlb.Ident("range_duration_ms")
		}
		factor := extrapolationFactorSQL(timestampsExpr, seriesLength, interceptTimeMSExpr, rangeMS)
		rangeSeconds := storage.NativeFloatLiteral(float64(rangeMS) / 1000.0)
		condition := sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR (" + renderSQLExprNoParams(durationExpr) + ") <= 0"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(counterDeltaExpr) + ") * (" + factor + ") / (" + rangeSeconds + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "irate":
		lastValue := arrayElementAtLength(valuesExpr)
		prevValue := arrayElementAtLengthMinusOne(valuesExpr)
		lastTS := arrayElementAtLength(timestampsExpr)
		prevTS := arrayElementAtLengthMinusOne(timestampsExpr)
		durationExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(lastTS) + ") - (" + renderSQLExprNoParams(prevTS) + ")"}
		deltaExpr := sqlb.RawLit{V: "if((" + renderSQLExprNoParams(lastValue) + ") < (" + renderSQLExprNoParams(prevValue) + "), " + renderSQLExprNoParams(lastValue) + ", (" + renderSQLExprNoParams(lastValue) + ") - (" + renderSQLExprNoParams(prevValue) + "))"}
		condition := sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR (" + renderSQLExprNoParams(durationExpr) + ") <= 0"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(deltaExpr) + ") / (" + renderSQLExprNoParams(durationExpr) + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "delta":
		firstValue := sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{valuesExpr, sqlb.RawLit{V: "1"}}}
		lastValue := arrayElementAtLength(valuesExpr)
		condition := sqlb.RawLit{V: "isNaN(" + renderSQLExprNoParams(firstValue) + ") OR isNaN(" + renderSQLExprNoParams(lastValue) + ")"}
		factor := extrapolationFactorSQL(timestampsExpr, seriesLength, interceptTimeMSExpr, rangeMS)
		resultExpr := sqlb.RawLit{V: "((" + renderSQLExprNoParams(lastValue) + ") - (" + renderSQLExprNoParams(firstValue) + ")) * (" + factor + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "idelta":
		lastValue := arrayElementAtLength(valuesExpr)
		prevValue := arrayElementAtLengthMinusOne(valuesExpr)
		condition := sqlb.RawLit{V: "isNaN(" + renderSQLExprNoParams(prevValue) + ") OR isNaN(" + renderSQLExprNoParams(lastValue) + ")"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(lastValue) + ") - (" + renderSQLExprNoParams(prevValue) + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "deriv":
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		timestampsSQL := renderSQLExprNoParams(timestampsExpr)
		xsSQL := "arrayMap(ts -> (toFloat64(toUnixTimestamp64Milli(ts)) - (" + interceptTimeMSExpr + ")) / 1000.0, " + timestampsSQL + ")"
		lrExpr := "arrayReduce('simpleLinearRegression', " + xsSQL + ", " + valuesSQL + ")"
		condition := sqlb.RawLit{V: "length(" + valuesSQL + ") < 2 OR " + renderSQLExprNoParams(hasNaN)}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, sqlb.RawLit{V: "tupleElement(" + lrExpr + ", 1)"}}})
	case "predict_linear":
		if paramNumber == nil {
			return "nan"
		}
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		timestampsSQL := renderSQLExprNoParams(timestampsExpr)
		firstValueExpr := "arrayElement(" + valuesSQL + ", 1)"
		constExpr := "arrayAll(v -> v = " + firstValueExpr + ", " + valuesSQL + ")"
		lrExpr := "arrayReduce('simpleLinearRegression', arrayMap(ts -> (toFloat64(toUnixTimestamp64Milli(ts)) - (" + interceptTimeMSExpr + ")) / 1000.0, " + timestampsSQL + "), " + valuesSQL + ")"
		predictionExpr := "tupleElement(" + lrExpr + ", 1) * " + storage.NativeFloatLiteral(*paramNumber) + " + tupleElement(" + lrExpr + ", 2)"
		return "multiIf(length(" + valuesSQL + ") < 2, nan, (" + constExpr + ") AND NOT isInfinite(" + firstValueExpr + "), " + firstValueExpr + ", (" + constExpr + ") AND isInfinite(" + firstValueExpr + "), nan, " + predictionExpr + ")"
	case "double_exponential_smoothing", "holt_winters":
		if len(paramNumbers) != 2 || paramNumbers[0] == nil || paramNumbers[1] == nil {
			return "nan"
		}
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		sf := storage.NativeFloatLiteral(*paramNumbers[0])
		tf := storage.NativeFloatLiteral(*paramNumbers[1])
		thirdOnwardExpr := "arraySlice(" + valuesSQL + ", 3)"
		secondValueExpr := "arrayElement(" + valuesSQL + ", 2)"
		firstValueExpr := "arrayElement(" + valuesSQL + ", 1)"
		newS1Expr := sf + " * v + (1 - " + sf + ") * (tupleElement(acc, 1) + tupleElement(acc, 2))"
		newBExpr := tf + " * ((" + newS1Expr + ") - tupleElement(acc, 1)) + (1 - " + tf + ") * tupleElement(acc, 2)"
		foldExpr := "arrayFold((acc, v) -> ((" + newS1Expr + "), (" + newBExpr + ")), " + thirdOnwardExpr + ", (" + secondValueExpr + ", (" + secondValueExpr + ") - (" + firstValueExpr + ")))"
		return "multiIf(length(" + valuesSQL + ") < 2, nan, length(" + valuesSQL + ") = 2, " + secondValueExpr + ", tupleElement(" + foldExpr + ", 1))"
	default:
		return "nan"
	}
}

func minimumSeriesLengthForRangeFunction(fn string) int {
	switch fn {
	case "increase", "rate", "irate", "delta", "idelta", "deriv", "predict_linear", "double_exponential_smoothing", "holt_winters":
		return 1
	default:
		return 0
	}
}
