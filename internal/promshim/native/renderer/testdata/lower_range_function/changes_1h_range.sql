SELECT final_tags AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (SELECT arrayFilter(tag -> tag.1 != '__name__', tags) AS final_tags, eval_ts AS timestamp, if(arrayExists(v -> isNaN(v), window_values), nan, changes_count) AS value FROM (
    SELECT source.tags AS tags, grid.eval_ts AS eval_ts, arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts - toIntervalMillisecond(0) AND tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(3600000), source.time_series) AS window_series, arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series) AS window_values, arrayPopBack(window_values) AS window_values_prev, arrayPopFront(window_values) AS window_values_cur, toFloat64(arraySum(arrayMap((p, c) -> if(c != p, 1, 0), window_values_prev, window_values_cur))) AS changes_count FROM (SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range(1700000000000, 1700000300000 + 1, 60000))) AS eval_ts) AS grid CROSS JOIN (
        SELECT tags AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (
            SELECT series.tags AS tags, d.timestamp AS timestamp, d.value AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
                SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_matrix_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
            ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND reinterpretAsUInt64(d.value) != 9218868437227405314 AND d.id IN (
                SELECT DISTINCT src.id FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_matrix_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
            )
        ) GROUP BY tags
    ) AS source
) AS step_windows WHERE length(window_series) > 0) GROUP BY final_tags ORDER BY final_tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
