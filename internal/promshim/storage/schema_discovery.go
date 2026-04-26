package storage

import (
	"context"
	"sort"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

var timeSeriesTagsSystemColumns = map[string]struct{}{
	"id":          {},
	"metric_name": {},
	"tags":        {},
	"min_time":    {},
	"max_time":    {},
}

func BuildPromotedTagColumnsDiscoverySQL(cfg QueryConfig) string {
	return "SELECT name AS value FROM (DESCRIBE TABLE " + schema.TimeSeriesTagsRef(timeSeriesTableRef(cfg)) + ") WHERE name NOT IN ('id', 'metric_name', 'tags', 'min_time', 'max_time') ORDER BY name" + schema.QuerySuffix
}

func BuildTimeSeriesIDTypeDiscoverySQL(cfg QueryConfig) string {
	return "SELECT type AS value FROM (DESCRIBE TABLE " + schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)) + ") WHERE name = 'id' LIMIT 1" + schema.QuerySuffix
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
		if _, reserved := timeSeriesTagsSystemColumns[name]; reserved {
			continue
		}
		out[name] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
