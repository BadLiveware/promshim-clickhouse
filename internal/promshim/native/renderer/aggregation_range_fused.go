package renderer

import (
	"fmt"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
)

// buildRangeFunctionOverWindowedArraysRowsSQL builds the inner row SQL for a
// fused range-function over windowed arrays. It is consumed by the logical
// aggregation_range_fused_logical.go body for both the selector-child
// non-fast-path branch and the subquery-child branch.
func buildRangeFunctionOverWindowedArraysRowsSQL(sourceSQL, fn, tagsExpr string, paramNumber *float64, paramNumbers []*float64, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	windowedSourceSQL, err := buildWindowedArraysSourceSQL(sourceSQL, fn, startMS, endMS, stepMS, rangeMS, offsetMS)
	if err != nil {
		return "", err
	}
	valueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", rangeExtrapolationAnchorExpr(offsetMS), rangeMS)
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: tagsExpr}, Alias: "tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"}},
		From:    rawRenderedSubquerySourceWithAlias(trimRenderedQuerySQL(windowedSourceSQL), "step_windows"),
		Where:   sqlb.RawLit{V: "length(window_series) > " + fmt.Sprintf("%d", minimumSeriesLengthForRangeFunction(fn))},
	}
	return buildNativeWrapperSQL(rows)
}
