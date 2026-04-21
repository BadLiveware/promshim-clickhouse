package storage

// prometheusStaleNaNBits is the uint64 reinterpretation of Prometheus's
// staleness-marker NaN (0x7FF0000000000002). Prometheus writes a sample with
// this specific NaN bit pattern to indicate that a series has gone stale (the
// target stopped exposing it). Computed NaNs from math operations (e.g.
// sqrt(-1), 0/0) use a different quiet-NaN bit pattern and must not be
// confused with staleness markers.
//
// ClickHouse preserves the exact float64 bit pattern through Gorilla encoding,
// so we can distinguish staleness from legitimate computed NaN by comparing
// reinterpretAsUInt64(value) against this constant.
const prometheusStaleNaNBits uint64 = 0x7FF0000000000002

// staleNaNFilterSQL is the ClickHouse WHERE-clause fragment that excludes
// staleness-marker samples from a column. Use with a qualified column name.
func staleNaNFilterSQL(column string) string {
	return "reinterpretAsUInt64(" + column + ") != 9218868437227405314"
}
