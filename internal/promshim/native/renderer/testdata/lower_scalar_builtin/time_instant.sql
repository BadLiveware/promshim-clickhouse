SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64({evaluation_ms:Int64}) / 1000.0 AS value
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
