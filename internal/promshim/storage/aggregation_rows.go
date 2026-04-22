package storage

import (
	"fmt"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/promql/parser"
)

// BuildRangeAggregationOverRowsSubquerySQL aggregates a row-oriented range source
// (tags, timestamp, value) directly, avoiding the intermediate per-series
// time_series materialization plus ARRAY JOIN re-flatten step.
func BuildRangeAggregationOverRowsSubquerySQL(sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
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
			{Expr: buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without), Alias: "grouping_tags"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: aggExpr, Alias: "value"},
		},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(trimNestedRowSourceSQL(sourceSQL))},
		GroupBy: []sqlb.Expr{sqlb.Ident("grouping_tags"), sqlb.Ident("timestamp")},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("grouping_tags"), Alias: "tags"}, {Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"}},
		From:    sqlb.SubSelect{S: grouped},
		GroupBy: []sqlb.Expr{sqlb.Ident("grouping_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("grouping_tags")}},
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
