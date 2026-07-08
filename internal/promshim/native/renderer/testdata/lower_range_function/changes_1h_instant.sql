SELECT tags AS tags, timestamp AS timestamp, if(range_has_nan, nan, range_changes_count) AS value FROM (SELECT time_series AS time_series, arrayFilter(tag -> tag.1 != '__name__', tags) AS tags, tupleElement(arrayElement(time_series, length(time_series)), 1) AS timestamp, arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series) AS range_values, arrayExists(v -> isNaN(v), range_values) AS range_has_nan, arrayPopBack(range_values) AS range_values_prev, arrayPopFront(range_values) AS range_values_cur, toFloat64(arraySum(arrayMap((p, c) -> if(c != p, 1, 0), range_values_prev, range_values_cur))) AS range_changes_count FROM (
    SELECT tags AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (
        SELECT series.tags AS tags, d.timestamp AS timestamp, d.value AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
            SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_matrix_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
        ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND reinterpretAsUInt64(d.value) != 9218868437227405314 AND d.id IN (SELECT id FROM (
            SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_matrix_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
        ))
    ) GROUP BY tags
) WHERE length(time_series) > 0)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
