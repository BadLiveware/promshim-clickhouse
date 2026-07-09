SELECT arraySort(tag -> tag.1, arrayFilter(tag -> NOT has(['__name__', 'instance'], tag.1), tags)) AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, if(countIf(isNaN(value)) > 0 OR count() = 0, CAST(NULL, 'Nullable(Float64)'), avg(value)) AS value FROM (
    SELECT series.tags AS tags, max(d.timestamp) AS timestamp, argMax(d.value, d.timestamp) AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
        SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
    ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) AND d.id IN (
        SELECT DISTINCT src.id FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
    ) GROUP BY d.id, series.tags HAVING NOT isNaN(value) ORDER BY tags
) GROUP BY tags ORDER BY tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
