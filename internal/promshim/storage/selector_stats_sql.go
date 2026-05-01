package storage

import (
	"fmt"
	"net/http"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

const QueryPurposeSelectorStats QueryPurpose = "selector_stats"

func BuildCappedSelectorStatsQuery(cfg QueryConfig, selector SelectorSource, requiredStartMS, requiredEndMS, cap int64) (string, map[string]string, error) {
	if cap < 0 {
		cap = 0
	}
	selector.NeedTags = false
	selector.RequireFullTags = false
	selector.RequiredTagLabels = nil
	sourceSQL, params, err := buildMatchedSeriesSQL(cfg, selector, "selector_preflight", requiredStartMS, requiredEndMS, true)
	if err != nil {
		return "", nil, err
	}
	limitedSQL := fmt.Sprintf("SELECT id FROM (%s) LIMIT %d", sourceSQL, cap+1)
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "count()"}, Alias: "matched_series"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(limitedSQL)},
	}
	sql, _, err := query.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.FormatSuffix, params, nil
}

func BuildSelectorStatsQuery(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "count()"}, Alias: "matched_series"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)},
	}
	sql, _, err := query.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.FormatSuffix, params, nil
}
