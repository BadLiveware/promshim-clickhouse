package storage

// staleNaNFilterSQL is the ClickHouse WHERE-clause fragment that excludes
// staleness-marker samples from a column. Use with a qualified column name.
func staleNaNFilterSQL(column string) string {
	return "reinterpretAsUInt64(" + column + ") != 9218868437227405314"
}
