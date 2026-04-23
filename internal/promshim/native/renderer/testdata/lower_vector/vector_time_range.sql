SELECT CAST([], 'Array(Tuple(String, String))') AS tags, arrayMap(ts_ms -> (fromUnixTimestamp64Milli(ts_ms), toFloat64(ts_ms) / 1000.0), range({start_ms:Int64}, ({end_ms:Int64} + {step_ms:Int64}), {step_ms:Int64})) AS time_series
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
