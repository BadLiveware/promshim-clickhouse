WITH cse_selector_3ea73066c075 AS MATERIALIZED (
SELECT series.tags AS tags, max(d.timestamp) AS timestamp, argMax(d.value, d.timestamp) AS value FROM timeSeriesData(`observability`.`prometheus`) AS d INNER JOIN (
    SELECT DISTINCT src.id, arrayConcat([tuple('__name__', src.metric_name)], arrayMap((k, v) -> tuple(k, v), mapKeys(src.tags), mapValues(src.tags))) AS tags FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {cse_selector_3ea73066c075_instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({cse_selector_3ea73066c075_required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({cse_selector_3ea73066c075_required_end_ms:Int64})
) AS series ON d.id = series.id WHERE d.timestamp >= fromUnixTimestamp64Milli({cse_selector_3ea73066c075_required_start_ms:Int64}) AND d.timestamp <= fromUnixTimestamp64Milli({cse_selector_3ea73066c075_required_end_ms:Int64}) AND d.id IN (
    SELECT DISTINCT src.id FROM timeSeriesTags(`observability`.`prometheus`) AS src WHERE src.metric_name = {cse_selector_3ea73066c075_instant_matcher_0_value:String} AND src.max_time >= fromUnixTimestamp64Milli({cse_selector_3ea73066c075_required_start_ms:Int64}) AND src.min_time <= fromUnixTimestamp64Milli({cse_selector_3ea73066c075_required_end_ms:Int64})
) GROUP BY d.id, series.tags HAVING NOT isNaN(value) ORDER BY tags
)
SELECT result_tags AS tags, timestamp AS timestamp, value AS value FROM (SELECT lhs.original_group AS result_tags, lhs.timestamp AS timestamp, lhs.value AS value FROM (SELECT tags AS original_group, arraySort(tag -> tag.1, arrayFilter(tag -> NOT has(['__name__'], tag.1), tags)) AS join_group, timestamp AS timestamp, value AS value FROM (
    SELECT tags AS tags, timestamp AS timestamp, value AS value FROM cse_selector_3ea73066c075
)) AS lhs INNER JOIN (SELECT join_group, toUInt8(1) AS present_marker FROM (SELECT tags AS original_group, arraySort(tag -> tag.1, arrayFilter(tag -> NOT has(['__name__'], tag.1), tags)) AS join_group, timestamp AS timestamp, value AS value FROM (
    SELECT tags AS tags, timestamp AS timestamp, value AS value FROM cse_selector_3ea73066c075
)) AS rhs GROUP BY join_group) AS rhs ON lhs.join_group = rhs.join_group) AS joined_rows ORDER BY result_tags
SETTINGS allow_experimental_time_series_table = 1, enable_global_with_statement = 1, enable_materialized_cte = 1
FORMAT JSONEachRow
