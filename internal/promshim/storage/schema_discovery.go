package storage

import (
	"context"
	"sort"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

var timeSeriesSystemColumns = map[string]struct{}{
	"id":                 {},
	"timestamp":          {},
	"value":              {},
	"metric_name":        {},
	"tags":               {},
	"all_tags":           {},
	"min_time":           {},
	"max_time":           {},
	"metric_family_name": {},
	"type":               {},
	"unit":               {},
	"help":               {},
}

func BuildPromotedTagColumnsDiscoverySQL(cfg QueryConfig) string {
	return strings.Join([]string{
		"SELECT c.name AS value",
		"FROM system.columns AS c",
		"INNER JOIN system.tables AS t ON t.database = c.database AND t.name = c.table",
		"WHERE c.database = " + sqlStringLiteral(cfg.Database),
		"AND c.table = " + sqlStringLiteral(cfg.Table),
		"AND c.name NOT IN " + timeSeriesSystemColumnNameSetSQL(),
		"AND match(t.engine_full, concat('''', regexpQuoteMeta(c.name), '''\\\\s*:\\\\s*''', regexpQuoteMeta(c.name), ''''))",
		"ORDER BY c.name",
	}, " ") + schema.FormatSuffix
}

func BuildTimeSeriesIDTypeDiscoverySQL(cfg QueryConfig) string {
	return "SELECT type AS value FROM system.columns WHERE database = " + sqlStringLiteral(cfg.Database) + " AND table = " + sqlStringLiteral(cfg.Table) + " AND name = 'id' LIMIT 1" + schema.FormatSuffix
}

func DiscoverPromotedTagColumns(ctx context.Context, client *Client, cfg QueryConfig) (map[string]struct{}, error) {
	values, err := client.QueryStringRows(ctx, QueryRequest{SQL: BuildPromotedTagColumnsDiscoverySQL(cfg), Purpose: QueryPurposeSchemaDiscovery, Format: ResultFormatJSONEachRow})
	if err != nil {
		return nil, err
	}
	return promotedTagColumnSetFromNames(values), nil
}

func DiscoverTimeSeriesIDType(ctx context.Context, client *Client, cfg QueryConfig) (string, error) {
	values, err := client.QueryStringRows(ctx, QueryRequest{SQL: BuildTimeSeriesIDTypeDiscoverySQL(cfg), Purpose: QueryPurposeSchemaDiscovery, Format: ResultFormatJSONEachRow})
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", nil
	}
	return strings.TrimSpace(values[0]), nil
}

func promotedTagColumnSetFromNames(names []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, reserved := timeSeriesSystemColumns[name]; reserved {
			continue
		}
		out[name] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func timeSeriesSystemColumnNameSetSQL() string {
	names := make([]string, 0, len(timeSeriesSystemColumns))
	for name := range timeSeriesSystemColumns {
		names = append(names, name)
	}
	sort.Strings(names)
	return "(" + strings.Join(quotedStringList(names), ", ") + ")"
}

func quotedStringList(values []string) []string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, sqlStringLiteral(value))
	}
	return quoted
}

func SortedPromotedTagColumnNames(columns map[string]struct{}) []string {
	if len(columns) == 0 {
		return nil
	}
	names := make([]string, 0, len(columns))
	for name := range columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
