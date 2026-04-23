SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, 42 AS value
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
