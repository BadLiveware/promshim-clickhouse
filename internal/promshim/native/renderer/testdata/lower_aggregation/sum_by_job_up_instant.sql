SELECT arrayFilter(tag -> has(['job'], tag.1), tags) AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, if(countIf(isNaN(value)) > 0, CAST(NULL, 'Nullable(Float64)'), sum(value)) AS value FROM (
    SELECT series.tags AS tags, fromUnixTimestamp64Milli(1700000000000) AS timestamp, argMax(d.value, d.timestamp) AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
        SELECT DISTINCT src.id, if(mapContains(src.tags, 'job'), [tuple('job', concat('', src.tags['job']))], CAST([], 'Array(Tuple(String, String))')) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
    ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64}) GROUP BY d.id, series.tags HAVING NOT isNaN(value) ORDER BY tags
) GROUP BY tags ORDER BY tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
