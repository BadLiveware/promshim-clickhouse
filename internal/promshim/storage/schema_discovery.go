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

const DenseRateRollupTableName = "rollup_cpu_rate_5m_1m_by_job"

var denseRateRollupRequiredColumns = []string{"job", "metric_name", "timestamp", "value"}

type DenseRateRollupDiscovery struct {
	Table          string
	Available      bool
	ColumnsPresent []string
	MissingColumns []string
}

func BuildPromotedTagColumnsDiscoverySQL(cfg QueryConfig) string {
	return "SELECT name AS value FROM (DESCRIBE TABLE " + schema.TimeSeriesTagsRef(timeSeriesTableRef(cfg)) + ") WHERE name NOT IN ('id', 'metric_name', 'tags', 'min_time', 'max_time') ORDER BY name" + schema.QuerySuffix
}

func BuildTimeSeriesIDTypeDiscoverySQL(cfg QueryConfig) string {
	return "SELECT type AS value FROM (DESCRIBE TABLE " + schema.TimeSeriesDataRef(timeSeriesTableRef(cfg)) + ") WHERE name = 'id' LIMIT 1" + schema.QuerySuffix
}

func BuildDenseRateRollupDiscoverySQL() string {
	return "SELECT name AS value FROM system.columns WHERE database = {database:String} AND table = {rollup_table:String} AND name IN ('job', 'metric_name', 'timestamp', 'value') ORDER BY name" + schema.QuerySuffix
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

func DiscoverDenseRateRollup(ctx context.Context, client *Client, cfg QueryConfig) (DenseRateRollupDiscovery, error) {
	values, err := client.QueryStringRows(ctx, QueryRequest{
		SQL:     BuildDenseRateRollupDiscoverySQL(),
		Params:  map[string]string{"param_database": cfg.Database, "param_rollup_table": DenseRateRollupTableName},
		Purpose: QueryPurposeSchemaDiscovery,
		Format:  ResultFormatJSONEachRow,
	})
	if err != nil {
		return DenseRateRollupDiscovery{}, err
	}
	return denseRateRollupDiscoveryFromColumns(values), nil
}

func denseRateRollupDiscoveryFromColumns(columns []string) DenseRateRollupDiscovery {
	present := map[string]struct{}{}
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		present[column] = struct{}{}
	}
	out := DenseRateRollupDiscovery{Table: DenseRateRollupTableName}
	for _, column := range denseRateRollupRequiredColumns {
		if _, ok := present[column]; ok {
			out.ColumnsPresent = append(out.ColumnsPresent, column)
		} else {
			out.MissingColumns = append(out.MissingColumns, column)
		}
	}
	out.Available = len(out.MissingColumns) == 0
	return out
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
