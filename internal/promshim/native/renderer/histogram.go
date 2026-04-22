package renderer

import (
	"fmt"
	"math"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage/schema"
)

func renderHistogramProjectionFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.HistogramProjection == nil || fragment.HistogramProjection.Child == nil {
		return renderedFragment{}, fmt.Errorf("histogram projection fragment is missing child metadata")
	}
	histograms, err := renderClassicHistogramGroupsQuery(cfg, fragment.HistogramProjection.Child, params, "histogram_projection_child")
	if err != nil {
		return renderedFragment{}, err
	}
	valueExpr, err := classicHistogramProjectionValueExpr(sqlb.Ident("buckets"), fragment.HistogramProjection.Func)
	if err != nil {
		return renderedFragment{}, err
	}
	return histogramOutputFragment(histograms, valueExpr, params.Mode, "histogram_projection_steps"), nil
}

func classicHistogramProjectionValueExpr(buckets sqlb.Expr, fn string) (sqlb.Expr, error) {
	counts := bucketCountsExpr(buckets)
	upperBounds := bucketUpperBoundsExpr(buckets)
	prevCounts := prependZeroPopBack(counts)
	prevUpperBounds := prependZeroPopBack(upperBounds)
	finiteCounts := sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
		sqlb.Lambda{Params: []sqlb.Ident{"v"}, Body: sqlb.RawLit{V: "NOT isNaN(v)"}},
		counts,
	}}
	lastUpper := sqlb.TupleElem{X: arrayLastElement(buckets), K: 1}

	bucketsTooShort := sqlb.RawLit{V: "length(buckets) < 2 OR NOT isInfinite(" + renderSQLExprNoParams(lastUpper) + ")"}

	countExpr := sqlb.Call{Name: "if", Args: []sqlb.Expr{
		bucketsTooShort,
		sqlb.RawLit{V: "nan"},
		sqlb.Call{Name: "if", Args: []sqlb.Expr{
			sqlb.RawLit{V: "length(" + renderSQLExprNoParams(finiteCounts) + ") = 0"},
			sqlb.RawLit{V: "nan"},
			sqlb.Call{Name: "arrayMax", Args: []sqlb.Expr{finiteCounts}},
		}},
	}}

	lowerBounds := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.Lambda{
			Params: []sqlb.Ident{"idx", "upper", "prev_upper"},
			Body:   sqlb.RawLit{V: "if(idx = 1 AND upper <= 0, upper, prev_upper)"},
		},
		sqlb.Call{Name: "arrayEnumerate", Args: []sqlb.Expr{upperBounds}},
		upperBounds,
		prevUpperBounds,
	}}
	adjustedUpperBounds := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.Lambda{
			Params: []sqlb.Ident{"upper", "prev_upper"},
			Body:   sqlb.RawLit{V: "if(isInfinite(upper), prev_upper, upper)"},
		},
		upperBounds,
		prevUpperBounds,
	}}
	deltas := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.Lambda{
			Params: []sqlb.Ident{"count", "prev_count"},
			Body:   sqlb.RawLit{V: "if(count - prev_count < 0, toFloat64(0), count - prev_count)"},
		},
		counts,
		prevCounts,
	}}
	midpoints := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.Lambda{
			Params: []sqlb.Ident{"lower", "upper"},
			Body:   sqlb.RawLit{V: "if(isNaN(lower) OR isNaN(upper) OR isInfinite(lower) OR isInfinite(upper), toFloat64(0), lower + (upper - lower) / 2)"},
		},
		lowerBounds,
		adjustedUpperBounds,
	}}

	sumExpr := sqlb.Call{Name: "if", Args: []sqlb.Expr{
		sqlb.RawLit{V: "length(buckets) < 2 OR NOT isInfinite(" + renderSQLExprNoParams(lastUpper) + ") OR " +
			"arrayExists(v -> isNaN(v), " + renderSQLExprNoParams(counts) + ")"},
		sqlb.RawLit{V: "nan"},
		sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{
			sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
				sqlb.Lambda{
					Params: []sqlb.Ident{"delta", "midpoint"},
					Body:   sqlb.RawLit{V: "delta * midpoint"},
				},
				deltas,
				midpoints,
			}},
		}},
	}}

	countRendered := renderSQLExprNoParams(countExpr)
	sumRendered := renderSQLExprNoParams(sumExpr)
	avgExpr := sqlb.RawLit{V: "if(isNaN(" + countRendered + ") OR (" + countRendered + ") <= 0 OR isNaN(" + sumRendered + "), nan, (" + sumRendered + ") / (" + countRendered + "))"}

	meanRendered := "(" + sumRendered + ") / (" + countRendered + ")"
	varianceExpr := sqlb.RawLit{V: "if(isNaN(" + countRendered + ") OR (" + countRendered + ") <= 0 OR isNaN(" + sumRendered + "), nan, arraySum(arrayMap((delta, midpoint) -> toFloat64(ifNull(delta * pow(midpoint - (" + meanRendered + "), 2), nan)), " + renderSQLExprNoParams(deltas) + ", " + renderSQLExprNoParams(midpoints) + ")) / (" + countRendered + "))"}
	stddevExpr := sqlb.RawLit{V: "sqrt(" + renderSQLExprNoParams(varianceExpr) + ")"}

	switch fn {
	case "histogram_count":
		return countExpr, nil
	case "histogram_sum":
		return sumExpr, nil
	case "histogram_avg":
		return avgExpr, nil
	case "histogram_stdvar":
		return varianceExpr, nil
	case "histogram_stddev":
		return stddevExpr, nil
	default:
		return nil, fmt.Errorf("histogram projection function %q is not implemented yet", fn)
	}
}

func classicHistogramFractionValueExpr(buckets sqlb.Expr, lower, upper float64) sqlb.Expr {
	if math.IsNaN(lower) || math.IsNaN(upper) {
		return sqlb.RawLit{V: "nan"}
	}
	if lower >= upper {
		return sqlb.RawLit{V: "0"}
	}
	counts := bucketCountsExpr(buckets)
	upperBounds := bucketUpperBoundsExpr(buckets)
	prevCounts := prependZeroPopBack(counts)
	normalizedCounts := bucketNormalizedCountsExpr(counts, prevCounts)
	observations := arrayLastValue(normalizedCounts)
	prevNormalizedCounts := prependZeroPopBack(normalizedCounts)
	initialLower := sqlb.RawLit{V: "if(arrayElement(" + renderSQLExprNoParams(upperBounds) + ", 1) <= 0, -inf, toFloat64(0))"}
	lowerBounds := sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{
		sqlb.RawLit{V: "[" + renderSQLExprNoParams(initialLower) + "]"},
		sqlb.Call{Name: "arrayPopBack", Args: []sqlb.Expr{upperBounds}},
	}}

	lowerBoundsSQL := renderSQLExprNoParams(lowerBounds)
	upperBoundsSQL := renderSQLExprNoParams(upperBounds)
	normalizedSQL := renderSQLExprNoParams(normalizedCounts)
	prevNormalizedSQL := renderSQLExprNoParams(prevNormalizedCounts)
	observationsSQL := renderSQLExprNoParams(observations)

	boundaryRank := func(boundarySQL string) string {
		insideIdx := "arrayFirstIndex((lower_bound, upper_bound) -> lower_bound < (" + boundarySQL + ") AND upper_bound > (" + boundarySQL + "), " + lowerBoundsSQL + ", " + upperBoundsSQL + ")"
		prevIdx := "arrayFirstIndex(lower_bound -> lower_bound >= (" + boundarySQL + "), " + lowerBoundsSQL + ")"
		interp := "if(isInfinite(arrayElement(" + lowerBoundsSQL + ", " + insideIdx + ")) AND arrayElement(" + lowerBoundsSQL + ", " + insideIdx + ") < 0, arrayElement(" + normalizedSQL + ", " + insideIdx + "), arrayElement(" + prevNormalizedSQL + ", " + insideIdx + ") + (arrayElement(" + normalizedSQL + ", " + insideIdx + ") - arrayElement(" + prevNormalizedSQL + ", " + insideIdx + ")) * ((" + boundarySQL + ") - arrayElement(" + lowerBoundsSQL + ", " + insideIdx + ")) / (arrayElement(" + upperBoundsSQL + ", " + insideIdx + ") - arrayElement(" + lowerBoundsSQL + ", " + insideIdx + ")))"
		return "if((" + insideIdx + ") > 0, " + interp + ", if((" + prevIdx + ") > 0, arrayElement(" + prevNormalizedSQL + ", " + prevIdx + "), (" + observationsSQL + ")))"
	}
	lowerRank := boundaryRank(storage.NativeFloatLiteral(lower))
	upperRank := boundaryRank(storage.NativeFloatLiteral(upper))

	lastUpperSQL := renderSQLExprNoParams(sqlb.TupleElem{X: arrayLastElement(buckets), K: 1})
	return sqlb.MultiIf{
		Cases: []sqlb.MultiIfArm{
			{When: sqlb.RawLit{V: "length(buckets) < 2"}, Then: sqlb.RawLit{V: "nan"}},
			{When: sqlb.RawLit{V: "NOT isInfinite(" + lastUpperSQL + ")"}, Then: sqlb.RawLit{V: "nan"}},
			{When: sqlb.RawLit{V: "isNaN(" + observationsSQL + ") OR (" + observationsSQL + ") = 0"}, Then: sqlb.RawLit{V: "nan"}},
		},
		Else: sqlb.RawLit{V: "((" + upperRank + ") - (" + lowerRank + ")) / (" + observationsSQL + ")"},
	}
}

func classicHistogramQuantileValueExprForExpr(buckets sqlb.Expr, quantileExpr string) sqlb.Expr {
	quantileExpr = strings.TrimSpace(quantileExpr)
	if quantileExpr == "" {
		quantileExpr = "nan"
	}
	counts := bucketCountsExpr(buckets)
	upperBounds := bucketUpperBoundsExpr(buckets)
	prevCounts := prependZeroPopBack(counts)
	normalizedCounts := bucketNormalizedCountsExpr(counts, prevCounts)
	observations := arrayLastValue(normalizedCounts)

	observationsSQL := renderSQLExprNoParams(observations)
	normalizedSQL := renderSQLExprNoParams(normalizedCounts)
	upperBoundsSQL := renderSQLExprNoParams(upperBounds)

	rankSQL := "((" + quantileExpr + ") * (" + observationsSQL + "))"
	searchCountsSQL := "arraySlice(" + normalizedSQL + ", 1, length(" + normalizedSQL + ") - 1)"
	bucketIndexSQL := "arrayFirstIndex(v -> v >= " + rankSQL + ", " + searchCountsSQL + ")"
	bucketEndSQL := "arrayElement(" + upperBoundsSQL + ", " + bucketIndexSQL + ")"
	bucketStartSQL := "if((" + bucketIndexSQL + ") > 1, arrayElement(" + upperBoundsSQL + ", (" + bucketIndexSQL + ") - 1), toFloat64(0))"
	bucketCountSQL := "if((" + bucketIndexSQL + ") > 1, arrayElement(" + normalizedSQL + ", " + bucketIndexSQL + ") - arrayElement(" + normalizedSQL + ", (" + bucketIndexSQL + ") - 1), arrayElement(" + normalizedSQL + ", " + bucketIndexSQL + "))"
	rankInBucketSQL := "if((" + bucketIndexSQL + ") > 1, " + rankSQL + " - arrayElement(" + normalizedSQL + ", (" + bucketIndexSQL + ") - 1), " + rankSQL + ")"

	lastUpperSQL := renderSQLExprNoParams(sqlb.TupleElem{X: arrayLastElement(buckets), K: 1})
	return sqlb.MultiIf{
		Cases: []sqlb.MultiIfArm{
			{When: sqlb.RawLit{V: "isNaN(" + quantileExpr + ")"}, Then: sqlb.RawLit{V: "nan"}},
			{When: sqlb.RawLit{V: "(" + quantileExpr + ") < 0"}, Then: sqlb.RawLit{V: "-inf"}},
			{When: sqlb.RawLit{V: "(" + quantileExpr + ") > 1"}, Then: sqlb.RawLit{V: "inf"}},
			{When: sqlb.RawLit{V: "length(buckets) < 2"}, Then: sqlb.RawLit{V: "nan"}},
			{When: sqlb.RawLit{V: "NOT isInfinite(" + lastUpperSQL + ")"}, Then: sqlb.RawLit{V: "nan"}},
			{When: sqlb.RawLit{V: "isNaN(" + observationsSQL + ") OR (" + observationsSQL + ") <= 0"}, Then: sqlb.RawLit{V: "nan"}},
			{When: sqlb.RawLit{V: "(" + bucketIndexSQL + ") = 0"}, Then: sqlb.RawLit{V: "arrayElement(" + upperBoundsSQL + ", length(" + upperBoundsSQL + ") - 1)"}},
			{When: sqlb.RawLit{V: "(" + bucketIndexSQL + ") = 1 AND arrayElement(" + upperBoundsSQL + ", 1) <= 0"}, Then: sqlb.RawLit{V: "arrayElement(" + upperBoundsSQL + ", 1)"}},
			{When: sqlb.RawLit{V: "isNaN(" + bucketCountSQL + ") OR (" + bucketCountSQL + ") <= 0"}, Then: sqlb.RawLit{V: "nan"}},
		},
		Else: sqlb.RawLit{V: "(" + bucketStartSQL + ") + ((" + bucketEndSQL + ") - (" + bucketStartSQL + ")) * ((" + rankInBucketSQL + ") / (" + bucketCountSQL + "))"},
	}
}

func classicHistogramQuantileValueExpr(buckets sqlb.Expr, quantile float64) sqlb.Expr {
	return classicHistogramQuantileValueExprForExpr(buckets, storage.NativeFloatLiteral(quantile))
}

func renderHistogramFunctionFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.HistogramFunction == nil || fragment.HistogramFunction.Child == nil {
		return renderedFragment{}, fmt.Errorf("histogram function fragment is missing child metadata")
	}
	histograms, err := renderClassicHistogramGroupsQuery(cfg, fragment.HistogramFunction.Child, params, "histogram_function_child")
	if err != nil {
		return renderedFragment{}, err
	}
	var valueExpr sqlb.Expr
	switch fragment.HistogramFunction.Func {
	case "histogram_quantile":
		if fragment.HistogramFunction.Quantile == nil {
			return renderedFragment{}, fmt.Errorf("histogram_quantile fragment requires a quantile parameter")
		}
		valueExpr = classicHistogramQuantileValueExpr(sqlb.Ident("buckets"), *fragment.HistogramFunction.Quantile)
	case "histogram_quantiles":
		return renderHistogramQuantilesFragment(cfg, fragment, params, histograms)
	case "histogram_fraction":
		if fragment.HistogramFunction.Lower == nil || fragment.HistogramFunction.Upper == nil {
			return renderedFragment{}, fmt.Errorf("histogram_fraction fragment requires lower and upper parameters")
		}
		valueExpr = classicHistogramFractionValueExpr(sqlb.Ident("buckets"), *fragment.HistogramFunction.Lower, *fragment.HistogramFunction.Upper)
	default:
		return renderedFragment{}, fmt.Errorf("histogram function %q is not implemented yet", fragment.HistogramFunction.Func)
	}
	return histogramOutputFragment(histograms, valueExpr, params.Mode, "histogram_function_steps"), nil
}

func renderHistogramQuantilesFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams, histograms renderedFragment) (renderedFragment, error) {
	if fragment == nil || fragment.HistogramFunction == nil {
		return renderedFragment{}, fmt.Errorf("histogram_quantiles fragment is missing metadata")
	}
	finalized, err := finalizeRenderedFragment(histograms)
	if err != nil {
		return renderedFragment{}, err
	}
	queries := make([]string, 0, len(fragment.HistogramFunction.Quantiles))
	queryParams := map[string]string{}
	mergeRenderedQueryParams(queryParams, finalized.QueryParams)
	for i, quantile := range fragment.HistogramFunction.Quantiles {
		alias := fmt.Sprintf("histogram_quantile_%d", i)
		switch params.Mode {
		case native.RenderModeInstant:
			instantBindingSQL, instantParams, quantileValueExpr, err := renderInstantScalarBinding(cfg, quantile, params, alias)
			if err != nil {
				return renderedFragment{}, err
			}
			mergeRenderedQueryParams(queryParams, instantParams)
			quantileLabelExpr := openMetricsFloatExpr(quantileValueExpr)
			addedTagsExpr := "arrayPushBack(classic_histograms.tags, tuple(" + sqlStringLiteral(fragment.HistogramFunction.Label) + ", " + quantileLabelExpr + "))"
			valueExpr := renderSQLExprNoParams(classicHistogramQuantileValueExprForExpr(sqlb.Ident("buckets"), quantileValueExpr))
			joinSQL := ""
			if instantBindingSQL != "" {
				joinSQL = " CROSS JOIN (" + instantBindingSQL + ") AS " + alias
			}
			queries = append(queries, "SELECT "+addedTagsExpr+" AS tags, classic_histograms.timestamp AS timestamp, "+valueExpr+" AS value FROM ("+trimRenderedQuerySQL(finalized.SQL)+") AS classic_histograms"+joinSQL)
		case native.RenderModeRange:
			rangeBindingSQL, rangeParams, quantileValueExpr, err := renderRangeScalarBinding(cfg, quantile, params, alias)
			if err != nil {
				return renderedFragment{}, err
			}
			mergeRenderedQueryParams(queryParams, rangeParams)
			quantileLabelExpr := openMetricsFloatExpr(quantileValueExpr)
			addedTagsExpr := "arrayPushBack(classic_histograms.tags, tuple(" + sqlStringLiteral(fragment.HistogramFunction.Label) + ", " + quantileLabelExpr + "))"
			valueExpr := renderSQLExprNoParams(classicHistogramQuantileValueExprForExpr(sqlb.Ident("buckets"), quantileValueExpr))
			joinSQL := ""
			if rangeBindingSQL != "" {
				joinSQL = " LEFT JOIN (" + rangeBindingSQL + ") AS " + alias + " ON " + alias + ".timestamp = classic_histograms.timestamp"
			}
			queries = append(queries, "SELECT "+addedTagsExpr+" AS tags, classic_histograms.timestamp AS timestamp, "+valueExpr+" AS value FROM ("+trimRenderedQuerySQL(finalized.SQL)+") AS classic_histograms"+joinSQL)
		default:
			return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
	}
	unionSQL := strings.Join(queries, " UNION ALL ")
	if params.Mode != native.RenderModeRange {
		return renderedFragment{RawSQL: trimRenderedQuerySQL(unionSQL), ExtraParams: queryParams}, nil
	}
	finalSQL := "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" + unionSQL + ") AS histogram_quantiles_rows GROUP BY tags ORDER BY tags"
	return renderedFragment{RawSQL: trimRenderedQuerySQL(finalSQL), ExtraParams: queryParams}, nil
}

func openMetricsFloatExpr(valueExpr string) string {
	valueExpr = strings.TrimSpace(valueExpr)
	return "multiIf(isNaN(" + valueExpr + "), 'NaN', isInfinite(" + valueExpr + ") AND (" + valueExpr + ") > 0, '+Inf', isInfinite(" + valueExpr + ") AND (" + valueExpr + ") < 0, '-Inf', (" + valueExpr + ") = 1, '1.0', (" + valueExpr + ") = 0, '0.0', (" + valueExpr + ") = -1, '-1.0', if(position(toString(" + valueExpr + "), '.') > 0 OR position(lower(toString(" + valueExpr + ")), 'e') > 0, toString(" + valueExpr + "), concat(toString(" + valueExpr + "), '.0')))"
}

func histogramOutputFragment(histograms renderedFragment, valueExpr sqlb.Expr, mode native.RenderMode, rangeAlias string) renderedFragment {
	histogramsSource := sqlb.SubSelect{S: histograms.Select, Alias: "classic_histograms"}
	innerSelect := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: valueExpr, Alias: "value"},
		},
		From: histogramsSource,
	}
	if mode != native.RenderModeRange {
		return renderedFragment{Select: innerSelect, ExtraParams: histograms.ExtraParams}
	}
	innerSelect.OrderBy = []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}, {Expr: sqlb.Ident("timestamp")}}
	outerSelect := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"},
		},
		From:    sqlb.SubSelect{S: innerSelect, Alias: rangeAlias},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
	}
	return renderedFragment{Select: outerSelect, ExtraParams: histograms.ExtraParams}
}

// bucketCountsExpr yields arrayMap(bucket -> toFloat64(tupleElement(bucket, 2)), buckets).
func bucketCountsExpr(buckets sqlb.Expr) sqlb.Expr {
	return sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.RawLit{V: "bucket -> toFloat64(ifNull(tupleElement(bucket, 2), nan))"},
		buckets,
	}}
}

// bucketUpperBoundsExpr yields arrayMap(bucket -> toFloat64(tupleElement(bucket, 1)), buckets).
func bucketUpperBoundsExpr(buckets sqlb.Expr) sqlb.Expr {
	return sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
		sqlb.RawLit{V: "bucket -> toFloat64(ifNull(tupleElement(bucket, 1), nan))"},
		buckets,
	}}
}

// prependZeroPopBack yields arrayConcat([toFloat64(0)], arrayPopBack(x)).
func prependZeroPopBack(x sqlb.Expr) sqlb.Expr {
	return sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{
		sqlb.RawLit{V: "[toFloat64(0)]"},
		sqlb.Call{Name: "arrayPopBack", Args: []sqlb.Expr{x}},
	}}
}

// bucketNormalizedCountsExpr yields the cumulative per-bucket observation count
// (non-decreasing, NaN-propagating) from raw cumulative counts + their shifted
// copy. Mirrors Prometheus' classic-histogram normalisation.
func bucketNormalizedCountsExpr(counts, prevCounts sqlb.Expr) sqlb.Expr {
	return sqlb.Call{Name: "arrayCumSum", Args: []sqlb.Expr{
		sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
			sqlb.RawLit{V: "(count, prev_count) -> if(isNaN(count), nan, greatest(count - prev_count, toFloat64(0)))"},
			counts,
			prevCounts,
		}},
	}}
}

// arrayLastElement yields arrayElement(x, length(x)).
func arrayLastElement(x sqlb.Expr) sqlb.Expr {
	return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{x, sqlb.Call{Name: "length", Args: []sqlb.Expr{x}}}}
}

// arrayLastValue is arrayLastElement but emphasises "last value of a scalar
// array" (vs. last tuple element). Same SQL.
func arrayLastValue(x sqlb.Expr) sqlb.Expr {
	return arrayLastElement(x)
}

// renderClassicHistogramGroupsQuery normalizes classic histogram bucket vectors into
// one grouped row per histogram identity and timestamp. It removes __name__ and le
// from the output tags, parses le into a numeric upper bound, and coalesces repeated
// bucket boundaries before later projection helpers consume the grouped bucket array.
func renderClassicHistogramGroupsQuery(cfg storage.QueryConfig, child *native.NativeFragment, params RenderParams, prefix string) (renderedFragment, error) {
	if child == nil {
		return renderedFragment{}, fmt.Errorf("classic histogram materialization requires a child fragment")
	}
	childSQL, childParams, err := renderFragmentSubquery(cfg, child, params, prefix)
	if err != nil {
		return renderedFragment{}, err
	}

	leRaw := sqlb.RawLit{V: "tupleElement(arrayFirst(tag -> tag.1 = 'le', tags), 2)"}
	upperBoundExpr := sqlb.RawLit{V: "multiIf(le_raw IN ['+Inf', 'Inf', '+inf', 'inf'], inf, le_raw IN ['-Inf', '-inf'], -inf, toFloat64OrNull(le_raw))"}
	histogramTagsExpr := sqlb.RawLit{V: "arrayFilter(tag -> tag.1 != 'le' AND tag.1 != '__name__', tags)"}
	whereExpr := sqlb.RawLit{V: "le_raw != '' AND upper_bound IS NOT NULL"}

	var flattened *sqlb.Select
	switch params.Mode {
	case native.RenderModeInstant:
		innerPoints := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.Ident("tags"), Alias: "tags"},
				{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
				{Expr: sqlb.Ident("value"), Alias: "value"},
				{Expr: leRaw, Alias: "le_raw"},
			},
			From: rawRenderedSubquerySourceWithAlias(childSQL, "histogram_child"),
		}
		flattened = &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: histogramTagsExpr, Alias: "histogram_tags"},
				{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
				{Expr: upperBoundExpr, Alias: "upper_bound"},
				{Expr: sqlb.RawLit{V: "ifNull(toFloat64(value), nan)"}, Alias: "cumulative_count"},
			},
			From:  sqlb.SubSelect{S: innerPoints, Alias: "histogram_points"},
			Where: whereExpr,
		}
	case native.RenderModeRange:
		innerSeries := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.Ident("tags"), Alias: "tags"},
				{Expr: sqlb.Ident("time_series"), Alias: "time_series"},
				{Expr: leRaw, Alias: "le_raw"},
			},
			From: rawRenderedSubquerySourceWithAlias(childSQL, "histogram_child"),
		}
		flattened = &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: histogramTagsExpr, Alias: "histogram_tags"},
				{Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"},
				{Expr: upperBoundExpr, Alias: "upper_bound"},
				{Expr: sqlb.RawLit{V: "ifNull(toFloat64(point.2), nan)"}, Alias: "cumulative_count"},
			},
			From: sqlb.ArrayJoin{
				Base:  sqlb.SubSelect{S: innerSeries, Alias: "histogram_series"},
				Expr:  sqlb.RawLit{V: "histogram_series.time_series"},
				Alias: "point",
			},
			Where: whereExpr,
		}
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}

	coalesced := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("histogram_tags"), Alias: "histogram_tags"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: sqlb.Ident("upper_bound"), Alias: "upper_bound"},
			{Expr: sqlb.Call{Name: "sum", Args: []sqlb.Expr{sqlb.Ident("cumulative_count")}}, Alias: "cumulative_count"},
		},
		From: sqlb.SubSelect{S: flattened, Alias: "histogram_rows"},
		GroupBy: []sqlb.Expr{
			sqlb.Ident("histogram_tags"),
			sqlb.Ident("timestamp"),
			sqlb.Ident("upper_bound"),
		},
	}

	grouped := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("histogram_tags"), Alias: "tags"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((upper_bound, cumulative_count)))"}, Alias: "buckets"},
		},
		From:    sqlb.SubSelect{S: coalesced, Alias: "coalesced_histogram_rows"},
		GroupBy: []sqlb.Expr{sqlb.Ident("histogram_tags"), sqlb.Ident("timestamp")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}, {Expr: sqlb.Ident("timestamp")}},
	}
	return renderedFragment{Select: grouped, ExtraParams: childParams}, nil
}
