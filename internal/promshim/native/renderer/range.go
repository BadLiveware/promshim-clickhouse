package renderer

import (
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"fmt"
	"strconv"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage/schema"
)

func renderSubqueryFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment.Subquery == nil || fragment.Subquery.Child == nil {
		return renderedFragment{}, fmt.Errorf("subquery fragment is missing subquery metadata")
	}
	startMS, endMS, stepMS, err := subqueryRenderEnvelope(fragment.Subquery, params)
	if err != nil {
		return renderedFragment{}, err
	}
	childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(fragment.Subquery.Child, startMS, endMS)
	return renderFragment(cfg, fragment.Subquery.Child, RenderParams{
		Mode:                native.RenderModeRange,
		StartMS:             startMS,
		EndMS:               endMS,
		StepMS:              stepMS,
		RequiredStartMS:     childRequiredStartMS,
		RequiredEndMS:       childRequiredEndMS,
		ResolveSourcePromQL: params.ResolveSourcePromQL,
	})
}

func subqueryRenderEnvelope(fragment *native.SubqueryFragment, params RenderParams) (int64, int64, int64, error) {
	if fragment == nil {
		return 0, 0, 0, fmt.Errorf("subquery fragment is missing subquery metadata")
	}
	if fragment.Range <= 0 {
		return 0, 0, 0, fmt.Errorf("subquery range must be greater than zero")
	}
	step := fragment.Step
	if step <= 0 {
		step = time.Minute
	}
	stepMS := step.Milliseconds()
	if stepMS <= 0 {
		return 0, 0, 0, fmt.Errorf("subquery step must be greater than zero")
	}

	rangeMS := fragment.Range.Milliseconds()
	offsetMS := fragment.Offset.Milliseconds()

	switch params.Mode {
	case native.RenderModeInstant:
		endMS := params.EvaluationTimeMS
		if fragment.Timestamp != nil {
			endMS = *fragment.Timestamp
		}
		endMS -= offsetMS
		startMS := alignSubqueryStepStart(endMS-rangeMS, stepMS)
		return startMS, endMS, stepMS, nil
	case native.RenderModeRange:
		// Range-mode subquery rendering materializes the inner step-grid over the
		// current outer query envelope. Fixed @ anchors are rejected earlier by the
		// planner's range-mode temporal-anchor guard, so the expanding envelope here
		// intentionally follows the outer range bounds.
		endMS := params.EndMS - offsetMS
		startMS := alignSubqueryStepStart(params.StartMS-offsetMS-rangeMS, stepMS)
		return startMS, endMS, stepMS, nil
	default:
		return 0, 0, 0, fmt.Errorf("native subquery rendering in %s mode is not implemented yet", params.Mode)
	}
}

func renderRangeFunctionFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return renderedFragment{}, fmt.Errorf("range function fragment is missing range metadata")
	}
	switch params.Mode {
	case native.RenderModeInstant:
		childRendered, err := RenderFragment(cfg, fragment.RangeFunction.Child, params)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, err := buildInstantRangeFunctionSQL(childRendered.SQL, fragment.RangeFunction.Func, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.EvaluationTimeMS, rangeFunctionChildRangeMS(fragment.RangeFunction.Child))
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
	case native.RenderModeRange:
		selectorFragment := fragment.RangeFunction.Child
		if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector {
			childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
			childRendered, err := RenderFragment(cfg, selectorFragment, RenderParams{
				Mode:                native.RenderModeRange,
				StartMS:             params.StartMS,
				EndMS:               params.EndMS,
				StepMS:              params.StepMS,
				RequiredStartMS:     childRequiredStartMS,
				RequiredEndMS:       childRequiredEndMS,
				ResolveSourcePromQL: params.ResolveSourcePromQL,
			})
			if err != nil {
				return renderedFragment{}, err
			}
			sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fragment.RangeFunction.Func, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Selector.Lookback.Milliseconds(), selectorFragment.Selector.Offset.Milliseconds())
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
		}
		if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindSubquery && selectorFragment.Subquery != nil && selectorFragment.Subquery.Child != nil {
			childRendered, err := RenderFragment(cfg, selectorFragment, RenderParams{
				Mode:                native.RenderModeRange,
				StartMS:             params.StartMS,
				EndMS:               params.EndMS,
				StepMS:              params.StepMS,
				RequiredStartMS:     params.RequiredStartMS,
				RequiredEndMS:       params.RequiredEndMS,
				ResolveSourcePromQL: params.ResolveSourcePromQL,
			})
			if err != nil {
				return renderedFragment{}, err
			}
			sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fragment.RangeFunction.Func, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Subquery.Range.Milliseconds(), selectorFragment.Subquery.Offset.Milliseconds())
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
		}
		return renderedFragment{}, fmt.Errorf("native range-mode rendering for %s currently requires a direct range-vector selector child or supported subquery child", fragment.RangeFunction.Func)
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func buildWindowedArraysSourceSQL(sourceSQL string, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range(" + strconv.FormatInt(startMS, 10) + ", " + strconv.FormatInt(endMS, 10) + " + 1, " + strconv.FormatInt(stepMS, 10) + ")))"}, Alias: "eval_ts"}}}
	stepWindows := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("source.tags"), Alias: "tags"},
			{Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"},
			{Expr: sqlb.RawLit{V: "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS, 10) + ") AND tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS+rangeMS, 10) + "), source.time_series)"}, Alias: "window_series"},
			{Expr: sqlb.RawLit{V: "arrayMap(point -> tupleElement(point, 1), window_series)"}, Alias: "window_timestamps"},
			{Expr: sqlb.RawLit{V: "arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series)"}, Alias: "window_values"},
		},
		From: sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: rawRenderedSubquerySourceWithAlias(sourceSQL, "source"), Kind: "CROSS"},
	}
	return buildNativeWrapperSQL(stepWindows)
}

func buildRangeFunctionOverWindowedArraysSQL(sourceSQL, fn string, paramNumber *float64, paramNumbers []*float64, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	windowedSourceSQL, err := buildWindowedArraysSourceSQL(sourceSQL, startMS, endMS, stepMS, rangeMS, offsetMS)
	if err != nil {
		return "", err
	}
	valueExpr := rangeFunctionValueExpr(fn, "window_series", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", rangeMS)
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: rangeFunctionTagsExpr(fn)}, Alias: "final_tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"}},
		From:    rawRenderedSubquerySourceWithAlias(trimRenderedQuerySQL(windowedSourceSQL), "step_windows"),
		Where:   sqlb.RawLit{V: "length(window_series) > " + strconv.Itoa(minimumSeriesLengthForRangeFunction(fn))},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"}},
		From:    sqlb.SubSelect{S: perStep},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}},
	}
	return buildNativeWrapperSQL(outer)
}

func buildInstantRangeFunctionSQL(sourceSQL, fn string, paramNumber *float64, paramNumbers []*float64, evaluationTimeMS int64, rangeMS int64) (string, error) {
	timestampExpr := "tupleElement(arrayElement(time_series, length(time_series)), 1)"
	if fn == "predict_linear" {
		timestampExpr = "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: rangeFunctionTagsExpr(fn)}, Alias: "tags"}, {Expr: sqlb.RawLit{V: timestampExpr}, Alias: "timestamp"}, {Expr: sqlb.RawLit{V: rangeFunctionValueExpr(fn, "time_series", paramNumber, paramNumbers, "arrayMap(point -> tupleElement(point, 1), time_series)", strconv.FormatInt(evaluationTimeMS, 10), rangeMS)}, Alias: "value"}},
		From:    rawRenderedSubquerySource(sourceSQL),
		Where:   sqlb.RawLit{V: "length(time_series) > " + strconv.Itoa(minimumSeriesLengthForRangeFunction(fn))},
	}
	return buildNativeWrapperSQL(query)
}

func rangeFunctionTagsExpr(fn string) string {
	if native.RangeFunctionPreservesMetricName(fn) {
		return "tags"
	}
	return "arrayFilter(tag -> tag.1 != '__name__', tags)"
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
	firstMS := "toFloat64(toUnixTimestamp64Milli(arrayElement(" + tsSQL + ", 1)))"
	lastMS := "toFloat64(toUnixTimestamp64Milli(arrayElement(" + tsSQL + ", " + lenSQL + ")))"
	sampledMS := "((" + lastMS + ") - (" + firstMS + "))"
	avgMS := "((" + sampledMS + ") / greatest(toFloat64(" + lenSQL + ") - 1, 1))"
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

func rangeFunctionChildRangeMS(child *native.NativeFragment) int64 {
	if child == nil {
		return 0
	}
	if child.Selector != nil && child.Selector.Kind == native.SelectorKindRangeVector {
		return child.Selector.Lookback.Milliseconds()
	}
	if child.Subquery != nil {
		return child.Subquery.Range.Milliseconds()
	}
	return 0
}

func rangeFunctionValueExpr(fn, seriesExpr string, paramNumber *float64, paramNumbers []*float64, timestampsSourceExpr string, interceptTimeMSExpr string, rangeMS int64) string {
	series := sqlb.RawLit{V: seriesExpr}
	valuesExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "point -> ifNull(toFloat64(tupleElement(point, 2)), nan)"}, series}}
	timestampsExpr := sqlb.RawLit{V: timestampsSourceExpr}
	hasNaN := sqlb.Call{Name: "arrayExists", Args: []sqlb.Expr{sqlb.Lambda{Params: []sqlb.Ident{"v"}, Body: sqlb.Call{Name: "isNaN", Args: []sqlb.Expr{sqlb.Ident("v")}}}, valuesExpr}}
	finiteValues := sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.Lambda{Params: []sqlb.Ident{"v"}, Body: sqlb.RawLit{V: "NOT isNaN(v)"}}, valuesExpr}}
	seriesLength := sqlb.Call{Name: "length", Args: []sqlb.Expr{series}}
	lenMinusOne := sqlb.RawLit{V: renderSQLExprNoParams(seriesLength) + " - 1"}

	arrayElementAtLength := func(expr sqlb.Expr) sqlb.Expr {
		return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{expr, seriesLength}}
	}
	arrayElementAtLengthMinusOne := func(expr sqlb.Expr) sqlb.Expr {
		return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{expr, lenMinusOne}}
	}
	prevValues := sqlb.Call{Name: "arraySlice", Args: []sqlb.Expr{valuesExpr, sqlb.RawLit{V: "1"}, lenMinusOne}}
	curValues := sqlb.Call{Name: "arraySlice", Args: []sqlb.Expr{valuesExpr, sqlb.RawLit{V: "2"}, lenMinusOne}}
	counterDeltaExpr := sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(prev, cur) -> if(cur < prev, cur, cur - prev)"}, prevValues, curValues}}}}
	changesExpr := sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(prev, cur) -> if(cur != prev, 1, 0)"}, prevValues, curValues}}}}}}
	lenFiniteIsZero := sqlb.Binary{Op: "=", L: sqlb.Call{Name: "length", Args: []sqlb.Expr{finiteValues}}, R: sqlb.RawLit{V: "0"}}

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
		medianExpr := sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'quantileExact(0.5)'"}, valuesExpr}}
		deviationsExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "x -> abs(x - " + renderSQLExprNoParams(medianExpr) + ")"}, valuesExpr}}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "=", L: seriesLength, R: sqlb.RawLit{V: "0"}}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'quantileExact(0.5)'"}, deviationsExpr}}}})
	case "quantile_over_time":
		if paramNumber == nil {
			return "nan"
		}
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		sortedExpr := "arrayConcat(arrayFilter(v -> isNaN(v), " + valuesSQL + "), arraySort(arrayFilter(v -> NOT isNaN(v), " + valuesSQL + ")))"
		lengthExpr := "length(" + sortedExpr + ")"
		qLit := storage.NativeFloatLiteral(*paramNumber)
		rankExpr := "(" + qLit + ") * (toFloat64(" + lengthExpr + ") - 1)"
		lowerIndexExpr := "greatest(1, toInt64(floor(" + rankExpr + ")) + 1)"
		upperIndexExpr := "least(toInt64(" + lengthExpr + "), (" + lowerIndexExpr + ") + 1)"
		weightExpr := "(" + rankExpr + ") - floor(" + rankExpr + ")"
		lowerValueExpr := "toFloat64(arrayElement(" + sortedExpr + ", " + lowerIndexExpr + "))"
		upperValueExpr := "toFloat64(arrayElement(" + sortedExpr + ", " + upperIndexExpr + "))"
		return "multiIf(" + lengthExpr + " = 0, nan, isNaN(" + qLit + "), nan, (" + qLit + ") < 0, -inf, (" + qLit + ") > 1, inf, (" + lowerValueExpr + ") * (1 - (" + weightExpr + ")) + (" + upperValueExpr + ") * (" + weightExpr + "))"
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
		durationExpr := sqlb.RawLit{V: renderSQLExprNoParams(arrayElementAtLength(timestampsExpr)) + " - arrayElement(" + renderSQLExprNoParams(timestampsExpr) + ", 1)"}
		condition := sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR (" + renderSQLExprNoParams(durationExpr) + ") <= 0"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(counterDeltaExpr) + ") / (" + renderSQLExprNoParams(durationExpr) + ")"}
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
