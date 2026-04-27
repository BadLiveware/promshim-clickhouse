SELECT arrayFilter(tag -> tag.1 != '__name__', tags) AS tags, timestamp AS timestamp, if(isNaN(value) OR isInfinite(value), value, if(((value) / 1) >= 0, floor(((value) / 1) + 0.5), ceil(((value) / 1) - 0.5)) * 1) AS value FROM (
    SELECT final_tags AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, if(nan_count > 0 OR sample_count <= 1 OR range_duration_ms <= 0, nan, range_counter_delta_sum / range_duration_ms) AS value FROM (SELECT final_tags AS final_tags, count() AS sample_count, countIf(isNaN(value)) AS nan_count, max(timestamp) - min(timestamp) AS range_duration_ms, deltaSumTimestamp(ifNull(toFloat64(value), nan), toUnixTimestamp64Milli(timestamp)) AS range_counter_delta_sum FROM (SELECT arrayFilter(tag -> tag.1 != '__name__', tags) AS final_tags, timestamp AS timestamp, ifNull(toFloat64(value), nan) AS value FROM (
        SELECT series.tags AS tags, d.timestamp AS timestamp, d.value AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
            SELECT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {range_matrix_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
        ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND reinterpretAsUInt64(d.value) != 9218868437227405314
    )) GROUP BY final_tags) ORDER BY final_tags
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
