SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(1) AS value FROM (SELECT count() AS sample_count FROM (SELECT any(tags) AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (
    SELECT d.id AS id, series.tags AS tags, d.timestamp AS timestamp, d.value AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
        SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {absent_child_range_matrix_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({absent_child_required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({absent_child_required_end_ms:Int64})
    ) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({absent_child_required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({absent_child_required_end_ms:Int64}) AND reinterpretAsUInt64(d.value) != 9218868437227405314
) GROUP BY id) AS absent_child ARRAY JOIN absent_child.time_series AS point) AS probe WHERE probe.sample_count = 0
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
