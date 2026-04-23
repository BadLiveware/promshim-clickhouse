SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(toDayOfMonth(toTimeZone(fromUnixTimestamp64Milli({evaluation_ms:Int64}), 'UTC'))) AS value
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
