package storage

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/promql/parser"
)

// BuildRangeAggregationRowsSubquerySQL aggregates a row-oriented range source
// (tags, timestamp, value) directly and keeps the result in row form. Callers
// that still need a range matrix can wrap the returned rows into time_series.
func BuildRangeAggregationRowsSubquerySQL(sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	if IsSelectionAggregation(op) {
		return "", nil, fmt.Errorf("row-oriented range aggregation for selection operator %q is not implemented yet", op.String())
	}
	if op == parser.COUNT_VALUES {
		return "", nil, fmt.Errorf("row-oriented range aggregation for operator %q is not implemented yet", op.String())
	}
	aggExpr, err := buildAggregationValueExpr(op, sqlb.Ident("value"), paramNumber)
	if err != nil {
		return "", nil, err
	}
	grouped := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without), Alias: "tags"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: aggExpr, Alias: "value"},
		},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(trimNestedRowSourceSQL(sourceSQL))},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags"), sqlb.Ident("timestamp")},
	}
	sql, _, err := grouped.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + schema.QuerySuffix, clonedParams, nil
}

// BuildRangeRowsToMatrixSubquerySQL wraps an already row-oriented range source
// (tags, timestamp, value) into the Prometheus-compatible time_series matrix
// shape without re-aggregating its values.
func BuildRangeRowsToMatrixSubquerySQL(sourceSQL string, params map[string]string) (string, map[string]string, error) {
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(trimNestedRowSourceSQL(sourceSQL))},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + schema.QuerySuffix, clonedParams, nil
}

// BuildRangeAggregationOverRowsSubquerySQL aggregates a row-oriented range source
// (tags, timestamp, value) directly, avoiding the intermediate per-series
// time_series materialization plus ARRAY JOIN re-flatten step.
func BuildRangeAggregationOverRowsSubquerySQL(sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	rowsSQL, clonedParams, err := BuildRangeAggregationRowsSubquerySQL(sourceSQL, params, op, grouping, without, paramNumber, paramString)
	if err != nil {
		return "", nil, err
	}
	return BuildRangeRowsToMatrixSubquerySQL(rowsSQL, clonedParams)
}

// BuildInstantAggregationRowsSubquerySQL aggregates an instant row-oriented
// source (tags, timestamp, value) and keeps the result in row form. This is
// useful for downstream consumers that do not need the final ordered vector
// wrapper produced by BuildInstantAggregationOverSubquerySQL.
func BuildInstantAggregationRowsSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, evaluationTimeMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	if IsSelectionAggregation(op) {
		return "", nil, fmt.Errorf("row-oriented instant aggregation for selection operator %q is not implemented yet", op.String())
	}
	if op == parser.COUNT_VALUES {
		return "", nil, fmt.Errorf("row-oriented instant aggregation for operator %q is not implemented yet", op.String())
	}
	aggExpr, err := buildAggregationValueExpr(op, sqlb.Ident("value"), paramNumber)
	if err != nil {
		return "", nil, err
	}
	tagsExpr := buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without)
	fromSource := sqlb.Source(sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)})
	if !aggregationSourceIsIdentity(source) {
		sourceSubquery, err := renderAggregationInstantSourceSubquery(source, sourceSQL)
		if err != nil {
			return "", nil, err
		}
		fromSource = sqlb.SubSelect{S: sourceSubquery}
	}
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: tagsExpr, Alias: "tags"}, {Expr: sqlb.RawLit{V: "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"}, Alias: "timestamp"}, {Expr: aggExpr, Alias: "value"}},
		From:    fromSource,
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
	}
	sql, _, err := rows.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + schema.QuerySuffix, clonedParams, nil
}

func trimNestedRowSourceSQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if idx := strings.LastIndex(sql, schema.SettingsLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	if idx := strings.LastIndex(sql, schema.FormatLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	return sql
}
