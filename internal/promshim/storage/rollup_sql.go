package storage

import (
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

func denseRateRollupTableRef(cfg QueryConfig) string {
	return "`" + escapeIdentifier(cfg.Database) + "`.`" + escapeIdentifier(DenseRateRollupTableName) + "`"
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
