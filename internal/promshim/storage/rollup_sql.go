package storage

import (
	"context"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

func denseRateRollupTableRef(cfg QueryConfig) string {
	return "`" + escapeIdentifier(cfg.Database) + "`.`" + escapeIdentifier(DenseRateRollupTableName) + "`"
}

type DenseRateRollupCoverage struct {
	MetricName string
	HasRows    bool
	MinMS      int64
	MaxMS      int64
}

func BuildDenseRateRollupCoverageSQL(cfg QueryConfig) (string, map[string]string) {
	query := strings.TrimSpace(`
SELECT if(
    count() = 0,
    '',
    concat(toString(toUnixTimestamp64Milli(min(timestamp))), ',', toString(toUnixTimestamp64Milli(max(timestamp))))
) AS value
FROM ` + denseRateRollupTableRef(cfg) + `
WHERE metric_name = {metric_name:String}`)
	return query + schema.FormatSuffix, map[string]string{"param_metric_name": "demo_cpu_usage_seconds_total"}
}

func DiscoverDenseRateRollupCoverage(ctx context.Context, client *Client, cfg QueryConfig) (DenseRateRollupCoverage, error) {
	sql, params := BuildDenseRateRollupCoverageSQL(cfg)
	values, err := client.QueryStringRows(ctx, QueryRequest{SQL: sql, Params: params, Purpose: QueryPurposeSchemaDiscovery, Format: ResultFormatJSONEachRow})
	if err != nil {
		return DenseRateRollupCoverage{}, err
	}
	coverage := DenseRateRollupCoverage{MetricName: "demo_cpu_usage_seconds_total"}
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return coverage, nil
	}
	parts := strings.Split(strings.TrimSpace(values[0]), ",")
	if len(parts) != 2 {
		return coverage, nil
	}
	minMS, minErr := strconv.ParseInt(parts[0], 10, 64)
	maxMS, maxErr := strconv.ParseInt(parts[1], 10, 64)
	if minErr != nil || maxErr != nil {
		return coverage, nil
	}
	coverage.HasRows = true
	coverage.MinMS = minMS
	coverage.MaxMS = maxMS
	return coverage, nil
}

func (c DenseRateRollupCoverage) Covers(startMS, endMS int64) bool {
	return c.HasRows && c.MinMS <= startMS && c.MaxMS >= endMS
}

func BuildDenseRateRollupRangeQuerySQL(cfg QueryConfig, startMS, endMS int64) (string, map[string]string) {
	query := strings.TrimSpace(`
SELECT
    [tuple('job', job)] AS tags,
    arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM ` + denseRateRollupTableRef(cfg) + `
WHERE metric_name = {metric_name:String}
  AND timestamp >= fromUnixTimestamp64Milli({start_ms:Int64})
  AND timestamp <= fromUnixTimestamp64Milli({end_ms:Int64})
GROUP BY job
ORDER BY tags`)
	return query + schema.FormatSuffix, map[string]string{
		"param_metric_name": "demo_cpu_usage_seconds_total",
		"param_start_ms":    strconv.FormatInt(startMS, 10),
		"param_end_ms":      strconv.FormatInt(endMS, 10),
	}
}
