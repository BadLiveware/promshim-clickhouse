SELECT arrayFilter(tag -> tag.1 != '__name__', tags) AS tags, arrayMap(point -> (point.1, -(point.2)), time_series) AS time_series FROM (
    SELECT tags AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (
        SELECT grid.tags AS tags, grid.eval_ts AS timestamp, d.value AS value FROM (SELECT grid_base.id AS id, grid_base.tags AS tags, grid_base.eval_ts AS eval_ts, grid_base.eval_ts - toIntervalMillisecond({offset_ms:Int64}) AS eval_bound FROM (SELECT series.id AS id, series.tags AS tags, arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS eval_ts FROM (
            SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
        ) AS series) AS grid_base) AS grid ASOF INNER JOIN (
            SELECT id, timestamp, value FROM timeSeriesData(`observability`.`prometheus`) WHERE timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND id IN (SELECT id FROM (SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})) AS matched_series_ids)
        ) AS d ON grid.id = d.id AND grid.eval_bound >= d.timestamp WHERE d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64}) AND NOT isNaN(value)
    ) GROUP BY tags ORDER BY tags
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
