package renderer

import (
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"fmt"
	"math"

	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func renderHistogramProjectionFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.HistogramProjection == nil || fragment.HistogramProjection.Child == nil {
		return RenderedQuery{}, fmt.Errorf("histogram projection fragment is missing child metadata")
	}
	histograms, err := renderClassicHistogramGroupsQuery(cfg, fragment.HistogramProjection.Child, params, "histogram_projection_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	countsExpr := "arrayMap(bucket -> toFloat64(tupleElement(bucket, 2)), buckets)"
	finiteCountsExpr := "arrayFilter(v -> NOT isNaN(v), " + countsExpr + ")"
	lastUpperExpr := "tupleElement(arrayElement(buckets, length(buckets)), 1)"
	countExpr := "if(length(buckets) < 2 OR NOT isInfinite(" + lastUpperExpr + "), nan, if(length(" + finiteCountsExpr + ") = 0, nan, arrayMax(" + finiteCountsExpr + ")))"
	prevCountsExpr := "arrayConcat([toFloat64(0)], arrayPopBack(" + countsExpr + "))"
	upperBoundsExpr := "arrayMap(bucket -> toFloat64(tupleElement(bucket, 1)), buckets)"
	prevUpperBoundsExpr := "arrayConcat([toFloat64(0)], arrayPopBack(" + upperBoundsExpr + "))"
	lowerBoundsExpr := "arrayMap((idx, upper, prev_upper) -> if(idx = 1 AND upper <= 0, upper, prev_upper), arrayEnumerate(" + upperBoundsExpr + "), " + upperBoundsExpr + ", " + prevUpperBoundsExpr + ")"
	adjustedUpperBoundsExpr := "arrayMap((upper, prev_upper) -> if(isInfinite(upper), prev_upper, upper), " + upperBoundsExpr + ", " + prevUpperBoundsExpr + ")"
	deltasExpr := "arrayMap((count, prev_count) -> if(count - prev_count < 0, toFloat64(0), count - prev_count), " + countsExpr + ", " + prevCountsExpr + ")"
	midpointsExpr := "arrayMap((lower, upper) -> if(isNaN(lower) OR isNaN(upper) OR isInfinite(lower) OR isInfinite(upper), toFloat64(0), lower + (upper - lower) / 2), " + lowerBoundsExpr + ", " + adjustedUpperBoundsExpr + ")"
	sumExpr := "if(length(buckets) < 2 OR NOT isInfinite(" + lastUpperExpr + ") OR arrayExists(v -> isNaN(v), " + countsExpr + "), nan, arraySum(arrayMap((delta, midpoint) -> delta * midpoint, " + deltasExpr + ", " + midpointsExpr + ")))"
	avgExpr := "if(isNaN(" + countExpr + ") OR (" + countExpr + ") <= 0 OR isNaN(" + sumExpr + "), nan, (" + sumExpr + ") / (" + countExpr + "))"
	valueExpr := countExpr
	switch fragment.HistogramProjection.Func {
	case "histogram_count":
		valueExpr = countExpr
	case "histogram_sum":
		valueExpr = sumExpr
	case "histogram_avg":
		valueExpr = avgExpr
	default:
		return RenderedQuery{}, fmt.Errorf("histogram projection function %q is not implemented yet", fragment.HistogramProjection.Func)
	}
	query := "SELECT tags AS tags, timestamp AS timestamp, " + valueExpr + " AS value FROM (" + trimRenderedQuerySQL(histograms.SQL) + ") AS classic_histograms\nFORMAT JSONEachRow\n"
	if params.Mode == native.RenderModeRange {
		query = "SELECT tags AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (SELECT tags AS tags, timestamp AS timestamp, " + valueExpr + " AS value FROM (" + trimRenderedQuerySQL(histograms.SQL) + ") AS classic_histograms ORDER BY tags, timestamp) AS histogram_projection_steps GROUP BY tags ORDER BY tags\nFORMAT JSONEachRow\n"
	}
	return RenderedQuery{SQL: query, QueryParams: histograms.QueryParams}, nil
}

func classicHistogramFractionValueExpr(bucketsExpr string, lower, upper float64) string {
	if math.IsNaN(lower) || math.IsNaN(upper) {
		return "nan"
	}
	if lower >= upper {
		return "0"
	}
	lowerLiteral := storage.NativeFloatLiteral(lower)
	upperLiteral := storage.NativeFloatLiteral(upper)
	countsExpr := "arrayMap(bucket -> toFloat64(tupleElement(bucket, 2)), " + bucketsExpr + ")"
	upperBoundsExpr := "arrayMap(bucket -> toFloat64(tupleElement(bucket, 1)), " + bucketsExpr + ")"
	prevCountsExpr := "arrayConcat([toFloat64(0)], arrayPopBack(" + countsExpr + "))"
	normalizedCountsExpr := "arrayCumSum(arrayMap((count, prev_count) -> if(isNaN(count), nan, greatest(count - prev_count, toFloat64(0))), " + countsExpr + ", " + prevCountsExpr + "))"
	observationsExpr := "arrayElement(" + normalizedCountsExpr + ", length(" + normalizedCountsExpr + "))"
	prevNormalizedCountsExpr := "arrayConcat([toFloat64(0)], arrayPopBack(" + normalizedCountsExpr + "))"
	initialLowerExpr := "if(arrayElement(" + upperBoundsExpr + ", 1) <= 0, -inf, toFloat64(0))"
	lowerBoundsExpr := "arrayConcat([" + initialLowerExpr + "], arrayPopBack(" + upperBoundsExpr + "))"
	boundaryRankExpr := func(boundary string) string {
		insideIdxExpr := "arrayFirstIndex((lower_bound, upper_bound) -> lower_bound < (" + boundary + ") AND upper_bound > (" + boundary + "), " + lowerBoundsExpr + ", " + upperBoundsExpr + ")"
		prevIdxExpr := "arrayFirstIndex(lower_bound -> lower_bound >= (" + boundary + "), " + lowerBoundsExpr + ")"
		interpExpr := "if(isInfinite(arrayElement(" + lowerBoundsExpr + ", " + insideIdxExpr + ")) AND arrayElement(" + lowerBoundsExpr + ", " + insideIdxExpr + ") < 0, arrayElement(" + normalizedCountsExpr + ", " + insideIdxExpr + "), arrayElement(" + prevNormalizedCountsExpr + ", " + insideIdxExpr + ") + (arrayElement(" + normalizedCountsExpr + ", " + insideIdxExpr + ") - arrayElement(" + prevNormalizedCountsExpr + ", " + insideIdxExpr + ")) * ((" + boundary + ") - arrayElement(" + lowerBoundsExpr + ", " + insideIdxExpr + ")) / (arrayElement(" + upperBoundsExpr + ", " + insideIdxExpr + ") - arrayElement(" + lowerBoundsExpr + ", " + insideIdxExpr + ")))"
		return "if((" + insideIdxExpr + ") > 0, " + interpExpr + ", if((" + prevIdxExpr + ") > 0, arrayElement(" + prevNormalizedCountsExpr + ", " + prevIdxExpr + "), (" + observationsExpr + ")))"
	}
	lowerRankExpr := boundaryRankExpr(lowerLiteral)
	upperRankExpr := boundaryRankExpr(upperLiteral)
	return "multiIf(length(" + bucketsExpr + ") < 2, nan, NOT isInfinite(tupleElement(arrayElement(" + bucketsExpr + ", length(" + bucketsExpr + ")), 1)), nan, isNaN(" + observationsExpr + ") OR (" + observationsExpr + ") = 0, nan, ((" + upperRankExpr + ") - (" + lowerRankExpr + ")) / (" + observationsExpr + "))"
}

func classicHistogramQuantileValueExpr(bucketsExpr string, quantile float64) string {
	if math.IsNaN(quantile) {
		return "nan"
	}
	if quantile < 0 {
		return "-inf"
	}
	if quantile > 1 {
		return "inf"
	}
	q := storage.NativeFloatLiteral(quantile)
	countsExpr := "arrayMap(bucket -> toFloat64(tupleElement(bucket, 2)), " + bucketsExpr + ")"
	upperBoundsExpr := "arrayMap(bucket -> toFloat64(tupleElement(bucket, 1)), " + bucketsExpr + ")"
	prevCountsExpr := "arrayConcat([toFloat64(0)], arrayPopBack(" + countsExpr + "))"
	normalizedCountsExpr := "arrayCumSum(arrayMap((count, prev_count) -> if(isNaN(count), nan, greatest(count - prev_count, toFloat64(0))), " + countsExpr + ", " + prevCountsExpr + "))"
	observationsExpr := "arrayElement(" + normalizedCountsExpr + ", length(" + normalizedCountsExpr + "))"
	rankExpr := "(" + q + " * (" + observationsExpr + "))"
	searchCountsExpr := "arraySlice(" + normalizedCountsExpr + ", 1, length(" + normalizedCountsExpr + ") - 1)"
	bucketIndexExpr := "arrayFirstIndex(v -> v >= (" + rankExpr + "), " + searchCountsExpr + ")"
	bucketEndExpr := "arrayElement(" + upperBoundsExpr + ", " + bucketIndexExpr + ")"
	bucketStartExpr := "if((" + bucketIndexExpr + ") > 1, arrayElement(" + upperBoundsExpr + ", (" + bucketIndexExpr + ") - 1), toFloat64(0))"
	bucketCountExpr := "if((" + bucketIndexExpr + ") > 1, arrayElement(" + normalizedCountsExpr + ", " + bucketIndexExpr + ") - arrayElement(" + normalizedCountsExpr + ", (" + bucketIndexExpr + ") - 1), arrayElement(" + normalizedCountsExpr + ", " + bucketIndexExpr + "))"
	rankInBucketExpr := "if((" + bucketIndexExpr + ") > 1, (" + rankExpr + ") - arrayElement(" + normalizedCountsExpr + ", (" + bucketIndexExpr + ") - 1), (" + rankExpr + "))"
	return "multiIf(length(" + bucketsExpr + ") < 2, nan, NOT isInfinite(tupleElement(arrayElement(" + bucketsExpr + ", length(" + bucketsExpr + ")), 1)), nan, isNaN(" + observationsExpr + ") OR (" + observationsExpr + ") <= 0, nan, (" + bucketIndexExpr + ") = 0, arrayElement(" + upperBoundsExpr + ", length(" + upperBoundsExpr + ") - 1), (" + bucketIndexExpr + ") = 1 AND arrayElement(" + upperBoundsExpr + ", 1) <= 0, arrayElement(" + upperBoundsExpr + ", 1), isNaN(" + bucketCountExpr + ") OR (" + bucketCountExpr + ") <= 0, nan, (" + bucketStartExpr + ") + ((" + bucketEndExpr + ") - (" + bucketStartExpr + ")) * ((" + rankInBucketExpr + ") / (" + bucketCountExpr + ")))"
}

func renderHistogramFunctionFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.HistogramFunction == nil || fragment.HistogramFunction.Child == nil {
		return RenderedQuery{}, fmt.Errorf("histogram function fragment is missing child metadata")
	}
	histograms, err := renderClassicHistogramGroupsQuery(cfg, fragment.HistogramFunction.Child, params, "histogram_function_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	valueExpr := "nan"
	switch fragment.HistogramFunction.Func {
	case "histogram_quantile":
		if fragment.HistogramFunction.Quantile == nil {
			return RenderedQuery{}, fmt.Errorf("histogram_quantile fragment requires a quantile parameter")
		}
		valueExpr = classicHistogramQuantileValueExpr("buckets", *fragment.HistogramFunction.Quantile)
	case "histogram_fraction":
		if fragment.HistogramFunction.Lower == nil || fragment.HistogramFunction.Upper == nil {
			return RenderedQuery{}, fmt.Errorf("histogram_fraction fragment requires lower and upper parameters")
		}
		valueExpr = classicHistogramFractionValueExpr("buckets", *fragment.HistogramFunction.Lower, *fragment.HistogramFunction.Upper)
	default:
		return RenderedQuery{}, fmt.Errorf("histogram function %q is not implemented yet", fragment.HistogramFunction.Func)
	}
	query := "SELECT tags AS tags, timestamp AS timestamp, " + valueExpr + " AS value FROM (" + trimRenderedQuerySQL(histograms.SQL) + ") AS classic_histograms\nFORMAT JSONEachRow\n"
	if params.Mode == native.RenderModeRange {
		query = "SELECT tags AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (SELECT tags AS tags, timestamp AS timestamp, " + valueExpr + " AS value FROM (" + trimRenderedQuerySQL(histograms.SQL) + ") AS classic_histograms ORDER BY tags, timestamp) AS histogram_function_steps GROUP BY tags ORDER BY tags\nFORMAT JSONEachRow\n"
	}
	return RenderedQuery{SQL: query, QueryParams: histograms.QueryParams}, nil
}

// renderClassicHistogramGroupsQuery normalizes classic histogram bucket vectors into
// one grouped row per histogram identity and timestamp. It removes __name__ and le
// from the output tags, parses le into a numeric upper bound, and coalesces repeated
// bucket boundaries before later projection helpers consume the grouped bucket array.
func renderClassicHistogramGroupsQuery(cfg storage.QueryConfig, child *native.NativeFragment, params RenderParams, prefix string) (RenderedQuery, error) {
	if child == nil {
		return RenderedQuery{}, fmt.Errorf("classic histogram materialization requires a child fragment")
	}
	childSQL, childParams, err := renderFragmentSubquery(cfg, child, params, prefix)
	if err != nil {
		return RenderedQuery{}, err
	}
	var flattenedSQL string
	switch params.Mode {
	case native.RenderModeInstant:
		flattenedSQL = "SELECT arrayFilter(tag -> tag.1 != 'le' AND tag.1 != '__name__', tags) AS histogram_tags, timestamp AS timestamp, multiIf(le_raw IN ['+Inf', 'Inf', '+inf', 'inf'], inf, le_raw IN ['-Inf', '-inf'], -inf, toFloat64OrNull(le_raw)) AS upper_bound, ifNull(toFloat64(value), nan) AS cumulative_count FROM (SELECT tags AS tags, timestamp AS timestamp, value AS value, tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2) AS le_raw FROM (" + childSQL + ") AS histogram_child) AS histogram_points WHERE le_raw != '' AND upper_bound IS NOT NULL"
	case native.RenderModeRange:
		flattenedSQL = "SELECT arrayFilter(tag -> tag.1 != 'le' AND tag.1 != '__name__', tags) AS histogram_tags, point.1 AS timestamp, multiIf(le_raw IN ['+Inf', 'Inf', '+inf', 'inf'], inf, le_raw IN ['-Inf', '-inf'], -inf, toFloat64OrNull(le_raw)) AS upper_bound, ifNull(toFloat64(point.2), nan) AS cumulative_count FROM (SELECT tags AS tags, time_series AS time_series, tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2) AS le_raw FROM (" + childSQL + ") AS histogram_child) AS histogram_series ARRAY JOIN histogram_series.time_series AS point WHERE le_raw != '' AND upper_bound IS NOT NULL"
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
	groupedSQL := "SELECT histogram_tags AS tags, timestamp AS timestamp, arraySort(item -> item.1, groupArray((upper_bound, cumulative_count))) AS buckets FROM (SELECT histogram_tags AS histogram_tags, timestamp AS timestamp, upper_bound AS upper_bound, sum(cumulative_count) AS cumulative_count FROM (" + flattenedSQL + ") AS histogram_rows GROUP BY histogram_tags, timestamp, upper_bound) AS coalesced_histogram_rows GROUP BY histogram_tags, timestamp ORDER BY tags, timestamp"
	return RenderedQuery{SQL: groupedSQL + "\nFORMAT JSONEachRow\n", QueryParams: childParams}, nil
}
