package renderer

import (
	"fmt"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func tryRenderFusedRangeAggregationFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, bool, error) {
	if !canFuseRangeAggregationFragment(fragment, params) {
		return renderedFragment{}, false, nil
	}
	sql, queryParams, err := renderFusedRangeAggregationSQL(cfg, fragment, params)
	if err != nil {
		return renderedFragment{}, false, err
	}
	if fragment.Aggregation.EmitZeroOnEmpty {
		return wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, params), true, nil
	}
	return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, true, nil
}

func renderFusedRangeAggregationSQL(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (string, map[string]string, error) {
	rowsSQL, rowParams, err := renderFusedRangeAggregationRowsSQL(cfg, fragment, params)
	if err != nil {
		return "", nil, err
	}
	return storage.BuildRangeRowsToMatrixSubquerySQL(rowsSQL, rowParams)
}

func renderFusedRangeAggregationRowsSQL(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (string, map[string]string, error) {
	if !canFuseRangeAggregationFragment(fragment, params) {
		return "", nil, fmt.Errorf("fused range aggregation rows require a supported aggregation fragment")
	}
	rowsSQL, rowParams, err := renderRangeFunctionRowsSQL(cfg, fragment.Aggregation.Source, params)
	if err != nil {
		return "", nil, err
	}
	return storage.BuildRangeAggregationRowsSubquerySQL(rowsSQL, rowParams, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber, fragment.Aggregation.ParamString)
}

func canFuseRangeAggregationFragment(fragment *native.NativeFragment, params RenderParams) bool {
	if params.Mode != native.RenderModeRange || fragment == nil || fragment.Aggregation == nil || fragment.Aggregation.Source == nil {
		return false
	}
	if fragment.Aggregation.Op != parser.SUM {
		return false
	}
	source := fragment.Aggregation.Source
	if source.Kind != native.FragmentKindRangeFunction || source.RangeFunction == nil || source.RangeFunction.Child == nil {
		return false
	}
	child := source.RangeFunction.Child
	if child.Kind == native.FragmentKindLeafSource && child.Selector != nil && child.Selector.Kind == native.SelectorKindRangeVector {
		return true
	}
	if child.Kind == native.FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil {
		return true
	}
	return false
}

func renderRangeFunctionRowsSQL(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (string, map[string]string, error) {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return "", nil, fmt.Errorf("range function row rendering requires range metadata")
	}
	selectorFragment := fragment.RangeFunction.Child
	if selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && fragment.RangeFunction.Func == "rate" && preferDirectSelectorWindowJoin(selectorFragment.Selector.Lookback.Milliseconds(), params.StepMS) {
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
		source, err := renderAggregationSource(selectorFragment, params)
		if err != nil {
			return "", nil, err
		}
		tagsExpr := rangeFunctionTagsExprFromInput(fragment.RangeFunction.Func, selectorOutputHasMetricName(selectorFragment.Selector))
		return storage.BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fragment.RangeFunction.Func, tagsExpr, minimumSeriesLengthForRangeFunction(fragment.RangeFunction.Func))
	}
	if selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && preferDirectSelectorWindowJoin(selectorFragment.Selector.Lookback.Milliseconds(), params.StepMS) {
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
		source, err := renderAggregationSource(selectorFragment, params)
		if err != nil {
			return "", nil, err
		}
		windowValueExpr := rangeFunctionValueExpr(fragment.RangeFunction.Func, "window_series", "window_values", fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", selectorFragment.Selector.Lookback.Milliseconds())
		tagsExpr := rangeFunctionTagsExprFromInput(fragment.RangeFunction.Func, selectorOutputHasMetricName(selectorFragment.Selector))
		return storage.BuildRangeWindowSelectorRowsQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fragment.RangeFunction.Func, windowValueExpr, tagsExpr, minimumSeriesLengthForRangeFunction(fragment.RangeFunction.Func))
	}
	if selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector {
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
		childRendered, err := RenderFragment(cfg, selectorFragment, RenderParams{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: childRequiredStartMS, RequiredEndMS: childRequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL})
		if err != nil {
			return "", nil, err
		}
		tagsExpr := rangeFunctionTagsExprFromInput(fragment.RangeFunction.Func, selectorOutputHasMetricName(selectorFragment.Selector))
		sql, err := buildRangeFunctionOverWindowedArraysRowsSQL(trimRenderedQuerySQL(childRendered.SQL), fragment.RangeFunction.Func, tagsExpr, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Selector.Lookback.Milliseconds(), selectorFragment.Selector.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return sql, childRendered.QueryParams, nil
	}
	if selectorFragment.Kind == native.FragmentKindSubquery && selectorFragment.Subquery != nil && selectorFragment.Subquery.Child != nil {
		childRendered, err := RenderFragment(cfg, selectorFragment, RenderParams{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: params.RequiredStartMS, RequiredEndMS: params.RequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL})
		if err != nil {
			return "", nil, err
		}
		sql, err := buildRangeFunctionOverWindowedArraysRowsSQL(trimRenderedQuerySQL(childRendered.SQL), fragment.RangeFunction.Func, rangeFunctionTagsExpr(fragment.RangeFunction.Func), fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Subquery.Range.Milliseconds(), selectorFragment.Subquery.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return sql, childRendered.QueryParams, nil
	}
	return "", nil, fmt.Errorf("fused range aggregation currently requires a direct range-vector selector child or supported subquery child")
}

func buildRangeFunctionOverWindowedArraysRowsSQL(sourceSQL, fn, tagsExpr string, paramNumber *float64, paramNumbers []*float64, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	windowedSourceSQL, err := buildWindowedArraysSourceSQL(sourceSQL, fn, startMS, endMS, stepMS, rangeMS, offsetMS)
	if err != nil {
		return "", err
	}
	valueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", rangeMS)
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: tagsExpr}, Alias: "tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"}},
		From:    rawRenderedSubquerySourceWithAlias(trimRenderedQuerySQL(windowedSourceSQL), "step_windows"),
		Where:   sqlb.RawLit{V: "length(window_series) > " + fmt.Sprintf("%d", minimumSeriesLengthForRangeFunction(fn))},
	}
	return buildNativeWrapperSQL(rows)
}
