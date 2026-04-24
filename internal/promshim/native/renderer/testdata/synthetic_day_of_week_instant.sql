SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(modulo(toDayOfWeek(toTimeZone(fromUnixTimestamp64Milli({evaluation_ms:Int64}), 'UTC')), 7)) AS value
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
