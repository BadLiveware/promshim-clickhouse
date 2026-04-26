-- Optional ClickHouse rollup templates for dense promshim dashboard ranges.
--
-- These tables are operator-managed accelerators, not promshim requirements.
-- Promshim must continue to run against the raw TimeSeries table when rollups
-- are absent or stale. Adapt database/table names, metric names, labels,
-- retention, refresh cadence, and grid interval to the deployment.
--
-- The example targets a common expensive dashboard family:
--   sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))
-- evaluated every 1 minute.
--
-- Refresh strategy is deliberately explicit INSERT/replace rather than an
-- always-on materialized view because PromQL rate semantics depend on a lookback
-- window and dashboard evaluation grid. In production, run this from a scheduled
-- job over the newly closed time slice, or replace it with a carefully tested
-- incremental materialized-view design.

CREATE TABLE IF NOT EXISTS observability.rollup_cpu_rate_5m_1m_by_job
(
    metric_name LowCardinality(String),
    job LowCardinality(String),
    timestamp DateTime64(3),
    value Float64
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
ORDER BY (metric_name, job, timestamp);

-- Replace the literals below with the refresh window and TimeSeries table.
-- The required raw read starts one lookback before the output start.
--
-- Parameters to substitute:
--   {database}             e.g. observability
--   {table}                e.g. prometheus
--   {metric_name}          e.g. demo_cpu_usage_seconds_total
--   {output_start_ms}      first output timestamp in milliseconds
--   {output_end_ms}        last output timestamp in milliseconds
--   {required_start_ms}    output_start_ms - 300000 for a 5m rate
--   {step_seconds}         60 for a 1m output grid
--   {lookback_seconds}     300 for rate(...[5m])
--
-- Delete/replace overlapping rollup rows before inserting when re-running a
-- refresh window.

INSERT INTO observability.rollup_cpu_rate_5m_1m_by_job
SELECT
    {metric_name:String} AS metric_name,
    job,
    point.1 AS timestamp,
    point.2 AS value
FROM
(
    SELECT
        job,
        arrayMap(point -> (point.1, point.2),
            arrayFilter(point -> point.3 > 0,
                arrayZip(
                    arrayMap(i -> fromUnixTimestamp64Milli({output_start_ms:Int64} + (i - 1) * ({step_seconds:Int64} * 1000)), arrayEnumerate(group_values)),
                    group_values,
                    present_counts))) AS points
    FROM
    (
        SELECT
            job,
            arrayReduce('sumForEach', groupArray(arrayMap(v -> if(isNull(v), 0., toFloat64(assumeNotNull(v))), values))) AS group_values,
            arrayReduce('sumForEach', groupArray(arrayMap(v -> if(isNull(v), 0, 1), values))) AS present_counts
        FROM
        (
            SELECT
                any(series.job) AS job,
                timeSeriesRateToGrid(
                    fromUnixTimestamp64Milli({output_start_ms:Int64}),
                    fromUnixTimestamp64Milli({output_end_ms:Int64}),
                    toDecimal64({step_seconds:Int64}, 3),
                    toDecimal64({lookback_seconds:Int64}, 3)
                )(d.timestamp, d.value) AS values
            FROM timeSeriesData({database:Identifier}.{table:Identifier}) AS d
            INNER JOIN
            (
                SELECT src.id AS id, src.tags['job'] AS job
                FROM timeSeriesTags({database:Identifier}.{table:Identifier}) AS src
                WHERE src.metric_name = {metric_name:String}
                  AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64})
                  AND src.min_time <= fromUnixTimestamp64Milli({output_end_ms:Int64})
            ) AS series ON d.id = series.id
            WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64})
              AND d.timestamp <= fromUnixTimestamp64Milli({output_end_ms:Int64})
              AND reinterpretAsUInt64(d.value) != 9218868437227405314
            GROUP BY series.id
        )
        GROUP BY job
    )
)
ARRAY JOIN points AS point
SETTINGS allow_experimental_time_series_table = 1;

-- Query shape for dashboards after refresh:
--
-- SELECT
--     [tuple('job', job)] AS tags,
--     arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
-- FROM observability.rollup_cpu_rate_5m_1m_by_job
-- WHERE metric_name = 'demo_cpu_usage_seconds_total'
--   AND timestamp >= fromUnixTimestamp64Milli({output_start_ms:Int64})
--   AND timestamp <= fromUnixTimestamp64Milli({output_end_ms:Int64})
-- GROUP BY job
-- ORDER BY tags;
