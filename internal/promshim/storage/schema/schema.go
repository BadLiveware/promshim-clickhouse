// Package schema centralises identifiers, type literals, suffixes, and small
// helper expressions that touch the experimental ClickHouse TimeSeries schema.
//
// The intent is narrow: anything ClickHouse owns (column names, table-function
// names, the SETTINGS/FORMAT trailer, the tag tuple type) lives here so that
// when the upstream schema changes we feel a single concentrated diff.
// Renderer-internal aliases (result_tags, join_group, original_group, ...) are
// deliberately NOT in this package.
package schema

import "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"

// QuerySuffix is the trailer appended to every native ClickHouse query that
// reads from the experimental TimeSeries engine.
const QuerySuffix = "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n"

// FormatSuffix is the trailer for queries that don't need the experimental
// setting (e.g. metadata lookups against ordinary tables).
const FormatSuffix = "\nFORMAT JSONEachRow\n"

// SettingsLine is the standalone SETTINGS clause (no leading newline). Used
// where callers need to detect or strip the suffix piecewise.
const SettingsLine = "SETTINGS allow_experimental_time_series_table = 1"

// FormatLine is the standalone FORMAT clause used where callers need to
// detect or strip the suffix piecewise.
const FormatLine = "FORMAT JSONEachRow"

// TagsArrayType is the ClickHouse type for label arrays.
const TagsArrayType = "Array(Tuple(String, String))"

// Schema columns exposed by the experimental TimeSeries table functions.
// These are upstream-owned column names; renderer-internal aliases are not
// included here on purpose.
const (
	ColID        = "id"
	ColTags      = "tags"
	ColTimestamp = "timestamp"
	ColValue     = "value"
)

// TimeSeriesDataRef returns the ClickHouse expression that reads time-series
// rows from the experimental TimeSeries engine. tableRef must be a
// backtick-quoted `db`.`table` reference produced by the caller.
func TimeSeriesDataRef(tableRef string) string {
	return "timeSeriesData(" + tableRef + ")"
}

// TimeSeriesTagsRef returns the ClickHouse expression that reads tag rows
// from the experimental TimeSeries engine.
func TimeSeriesTagsRef(tableRef string) string {
	return "timeSeriesTags(" + tableRef + ")"
}

// EmptyTagsArrayExpr is the canonical empty-tags placeholder used wherever a
// series has no labels (synthetic series, scalar fragments, drop-all-labels
// aggregations).
func EmptyTagsArrayExpr() sqlb.Expr {
	return sqlb.RawLit{V: "CAST([], '" + TagsArrayType + "')"}
}

// SortedTimeSeriesGroupArrayExpr collates (timestamp, value) pairs into a
// sorted time_series array. This is the terminal column of every range-mode
// native query.
func SortedTimeSeriesGroupArrayExpr() sqlb.Expr {
	return sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((timestamp, value)))"}
}
