package storage

import (
	"net/http"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

const QueryPurposeSelectorStats QueryPurpose = "selector_stats"

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
