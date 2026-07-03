// Package emit is the shared home for ClickHouse SQL expression primitives
// that the native SQL lowering path (storage/ and native/renderer/) renders
// repeatedly. Each helper returns a plain string (or sqlb.Expr) so callers
// remain free to embed them in sqlb.RawLit, concatenate with other SQL, or
// parameterize with query placeholders.
//
// This package is deliberately thin: it owns only the phrasing of
// ClickHouse-specific SQL fragments that otherwise drift across files. It
// does not model query structure — that is the job of the logical-plan
// layer (internal/promshim/plan) and its consumers.
//
// Stability: callers rely on the exact SQL phrasing for renderer test
// assertions; performance work amends helpers in lockstep with their
// consumers.
package emit

import (
	"strconv"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
)

// --- Tag-array primitives -------------------------------------------------

// StripMetricName returns the ClickHouse expression that removes the
// synthetic __name__ tuple from a tag array. `tagsSQL` is the inner
// expression (column reference, alias, or nested call).
func StripMetricName(tagsSQL string) string {
	return "arrayFilter(tag -> tag.1 != '__name__', " + tagsSQL + ")"
}

// StripMetricNameExpr is the sqlb.Expr variant of StripMetricName, for use
// when the tag source is already modelled as an Expr.
func StripMetricNameExpr(tags sqlb.Expr) sqlb.Expr {
	return sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
		sqlb.RawLit{V: "tag -> tag.1 != '__name__'"},
		tags,
	}}
}

// EmptyTagsArray returns the canonical empty tags array literal used wherever
// a series has no labels (synthetic series, scalar fragments, drop-all-labels
// aggregations).
func EmptyTagsArray() sqlb.Expr {
	return sqlb.RawLit{V: "CAST([], '" + TagsArrayType + "')"}
}

// TagsArrayType is the ClickHouse type of label arrays. Exported so callers
// rendering inline CASTs can reference the same literal.
const TagsArrayType = "Array(Tuple(String, String))"

// --- Value-coercion primitives --------------------------------------------

// NullableFloatCoerce wraps a raw value column so it lowers to Float64 with
// NaN substituted for NULL. Ubiquitous in the selector and renderer SQL
// paths because the TimeSeries value column is nullable.
func NullableFloatCoerce(columnSQL string) string {
	return "ifNull(toFloat64(" + columnSQL + "), nan)"
}

// --- Stale-NaN primitives -------------------------------------------------

// PrometheusStaleNaNBits is the uint64 reinterpretation of Prometheus's
// staleness-marker NaN (0x7FF0000000000002). Computed NaNs use a different
// quiet-NaN bit pattern and must not be conflated with staleness markers.
// ClickHouse preserves the exact float64 bit pattern through Gorilla
// encoding, so reinterpretAsUInt64 against this constant is the cheapest
// distinguisher.
const PrometheusStaleNaNBits uint64 = 0x7FF0000000000002

// StaleNaNFilter returns the WHERE-clause fragment that excludes
// staleness-marker samples from a column. Pass a qualified column name
// (e.g. "d.value").
func StaleNaNFilter(columnSQL string) string {
	return "reinterpretAsUInt64(" + columnSQL + ") != 9218868437227405314"
}

// --- Eval-timestamp grid primitives ---------------------------------------

// GridEvalTSParams returns the arrayJoin grid expression keyed off the
// standard {start_ms:Int64}/{end_ms:Int64}/{step_ms:Int64} query parameters.
// range() emits start_ms + k*step_ms strictly below the stop bound, so with
// stop `end_ms + step_ms` the grid includes end_ms when end_ms - start_ms is
// a multiple of step_ms — and OVERSHOOTS by one grid point past end_ms when
// it is not. Callers must bind an end_ms that lies on the start_ms-phased
// step grid (subquery envelopes clamp via alignSubqueryStepEnd). Shape used
// by the selector-side range queries.
func GridEvalTSParams() string {
	return "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), " +
		"range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64})))"
}

// GridEvalTSLiteral returns the arrayJoin grid expression with start/end/step
// baked in as integer literals. range() emits startMS + k*stepMS strictly
// below the stop bound, so with stop `endMS + 1` the grid never overshoots
// endMS; endMS itself is emitted only when endMS - startMS is a multiple of
// stepMS. Shape used by renderer-side grids once the evaluation range is
// resolved.
func GridEvalTSLiteral(startMS, endMS, stepMS int64) string {
	return "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), " +
		"range(" + strconv.FormatInt(startMS, 10) +
		", " + strconv.FormatInt(endMS, 10) + " + 1" +
		", " + strconv.FormatInt(stepMS, 10) + ")))"
}

// --- Range-window WHERE primitive -----------------------------------------

// RangeWindowTimeFilter returns the conjunction of the four time predicates
// that bound a range-window INNER JOIN between a grid subquery and the raw
// samples table. If `valueColumn` is non-empty, a StaleNaNFilter is appended
// for that column.
func RangeWindowTimeFilter(valueColumn string) string {
	clause := "d.timestamp >= fromUnixTimestamp64Milli({required_start_ms:Int64})" +
		" AND d.timestamp <= fromUnixTimestamp64Milli({required_end_ms:Int64})" +
		" AND d.timestamp <= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64})" +
		" AND d.timestamp >= grid.eval_ts - toIntervalMillisecond({offset_ms:Int64} + {lookback_ms:Int64})"
	if valueColumn != "" {
		clause += " AND " + StaleNaNFilter(valueColumn)
	}
	return clause
}

// --- Window-series projection primitives ----------------------------------

// WindowPointTimestamps projects per-point timestamps out of a sorted
// window_series array of (timestamp, value) tuples.
func WindowPointTimestamps(seriesSQL string) string {
	return "arrayMap(point -> tupleElement(point, 1), " + seriesSQL + ")"
}

// WindowPointValues projects per-point float64 values (with NULL → NaN)
// out of a sorted window_series array.
func WindowPointValues(seriesSQL string) string {
	return "arrayMap(point -> " + NullableFloatCoerce("tupleElement(point, 2)") + ", " + seriesSQL + ")"
}

// SortedTimeSeriesGroupArray collates (timestamp, value) pairs into a
// sorted time_series array. Terminal column of every range-mode native
// query.
func SortedTimeSeriesGroupArray() sqlb.Expr {
	return sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((timestamp, value)))"}
}
