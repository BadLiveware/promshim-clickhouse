package renderer

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/promql/parser"
)

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

// renderPreparedClassicHistogramQuery stages the classic histogram bucket arrays into
// named intermediate columns so later quantile lowering can reuse them instead of
// inlining the same arrayMap/arrayCumSum chains many times.
func renderPreparedClassicHistogramQuery(histograms renderedFragment, alias string) (RenderedQuery, error) {
	finalized, err := finalizeRenderedFragment(histograms)
	if err != nil {
		return RenderedQuery{}, err
	}
	baseSQL := trimRenderedQuerySQL(finalized.SQL)
	countsExpr := renderSQLExprNoParams(bucketCountsExpr(sqlb.Ident("buckets")))
	upperBoundsExpr := renderSQLExprNoParams(bucketUpperBoundsExpr(sqlb.Ident("buckets")))
	normalizedCountsExpr := renderSQLExprNoParams(bucketNormalizedCountsExpr(sqlb.Ident("counts"), prependZeroPopBack(sqlb.Ident("counts"))))

	arraysSQL := "SELECT tags AS tags, timestamp AS timestamp, buckets AS buckets, " + countsExpr + " AS counts, " + upperBoundsExpr + " AS upper_bounds FROM (" + baseSQL + ") AS " + alias + "_grouped"
	preparedSQL := "SELECT tags AS tags, timestamp AS timestamp, upper_bounds AS upper_bounds, " + normalizedCountsExpr + " AS normalized_counts, arrayElement(normalized_counts, length(normalized_counts)) AS observations, length(normalized_counts) AS bucket_len, arrayElement(upper_bounds, length(upper_bounds)) AS last_upper, arrayElement(upper_bounds, length(upper_bounds) - 1) AS last_finite_upper, arrayElement(upper_bounds, 1) AS first_upper FROM (" + arraysSQL + ") AS " + alias + "_arrays"
	return RenderedQuery{SQL: preparedSQL + schema.QuerySuffix, QueryParams: finalized.QueryParams}, nil
}

// renderClassicHistogramQuantileInputsSQL computes the quantile-specific rank and
// bucket interpolation inputs from the staged histogram columns above. Keeping this
// as a separate subquery dramatically shrinks the final emitted SQL while preserving
// the existing classic histogram semantics.
func renderClassicHistogramQuantileInputsSQL(preparedSQL, quantileExpr, joinSQL, prefix string) string {
	quantileExpr = strings.TrimSpace(quantileExpr)
	if quantileExpr == "" {
		quantileExpr = "nan"
	}
	rankSQL := "((" + quantileExpr + ") * observations)"
	searchCountsSQL := "arraySlice(normalized_counts, 1, length(normalized_counts) - 1)"
	rankedSQL := "SELECT prepared_histograms.tags AS tags, prepared_histograms.timestamp AS timestamp, (" + quantileExpr + ") AS q, prepared_histograms.bucket_len AS bucket_len, prepared_histograms.last_upper AS last_upper, prepared_histograms.last_finite_upper AS last_finite_upper, prepared_histograms.first_upper AS first_upper, prepared_histograms.upper_bounds AS upper_bounds, prepared_histograms.normalized_counts AS normalized_counts, prepared_histograms.observations AS observations, " + rankSQL + " AS rank, arrayFirstIndex(v -> v >= " + rankSQL + ", " + searchCountsSQL + ") AS bucket_index FROM (" + preparedSQL + ") AS prepared_histograms" + joinSQL
	return "SELECT tags AS tags, timestamp AS timestamp, q AS q, bucket_len AS bucket_len, last_upper AS last_upper, last_finite_upper AS last_finite_upper, first_upper AS first_upper, observations AS observations, bucket_index AS bucket_index, if(bucket_index > 1, arrayElement(upper_bounds, bucket_index - 1), toFloat64(0)) AS bucket_start, if(bucket_index > 0, arrayElement(upper_bounds, bucket_index), toFloat64(0)) AS bucket_end, if(bucket_index > 1, arrayElement(normalized_counts, bucket_index) - arrayElement(normalized_counts, bucket_index - 1), if(bucket_index = 1, arrayElement(normalized_counts, bucket_index), toFloat64(0))) AS bucket_count, if(bucket_index > 1, rank - arrayElement(normalized_counts, bucket_index - 1), rank) AS rank_in_bucket FROM (" + rankedSQL + ") AS " + prefix + "_ranked"
}

func classicHistogramQuantileFinalExprFromInputs() string {
	return "multiIf(isNaN(q), nan, q < 0, -inf, q > 1, inf, bucket_len < 2, nan, NOT isInfinite(last_upper), nan, isNaN(observations) OR observations <= 0, nan, bucket_index = 0, last_finite_upper, bucket_index = 1 AND first_upper <= 0, first_upper, isNaN(bucket_count) OR bucket_count <= 0, nan, bucket_start + (bucket_end - bucket_start) * (rank_in_bucket / bucket_count))"
}

func renderHistogramQuantileRowsSQL(preparedSQL, quantileExpr, joinSQL, prefix string) string {
	inputsSQL := renderClassicHistogramQuantileInputsSQL(preparedSQL, quantileExpr, joinSQL, prefix)
	return "SELECT tags AS tags, timestamp AS timestamp, " + classicHistogramQuantileFinalExprFromInputs() + " AS value FROM (" + inputsSQL + ") AS " + prefix + "_inputs"
}

func wrapHistogramRowsForMode(rowsSQL string, mode native.RenderMode, alias string) string {
	rowsSQL = trimRenderedQuerySQL(rowsSQL)
	if mode != native.RenderModeRange {
		return rowsSQL
	}
	return "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" + rowsSQL + ") AS " + alias + " GROUP BY tags ORDER BY tags"
}

func wrapHistogramRowsForConstantEmptyTagsRange(rowsSQL, alias string) string {
	rowsSQL = trimRenderedQuerySQL(rowsSQL)
	return "SELECT CAST([], 'Array(Tuple(String, String))') AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" + rowsSQL + ") AS " + alias
}

// renderHistogramQuantilesLogical iterates the per-quantile scalar
// children from the HistogramQuantilesPlan logical node (n.ParamChildren),
// lowering each through renderInstantScalarBindingFromLogical /
// renderRangeScalarBindingFromLogical. The label literal comes from n.Label.
func renderHistogramQuantilesLogical(ctx LoweringCtx, n *logicalpkg.HistogramQuantilesPlan, params RenderParams, prepared RenderedQuery) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("histogram_quantiles logical node is missing metadata")
	}
	preparedSQL := trimRenderedQuerySQL(prepared.SQL)
	queries := make([]string, 0, len(n.ParamChildren))
	queryParams := map[string]string{}
	mergeRenderedQueryParams(queryParams, prepared.QueryParams)
	for i, quantile := range n.ParamChildren {
		alias := fmt.Sprintf("histogram_quantile_%d", i)
		var joinSQL string
		var quantileValueExpr string
		switch params.Mode {
		case native.RenderModeInstant:
			instantBindingSQL, instantParams, valueExpr, err := renderInstantScalarBindingFromLogical(ctx, quantile, alias)
			if err != nil {
				return renderedFragment{}, err
			}
			mergeRenderedQueryParams(queryParams, instantParams)
			quantileValueExpr = valueExpr
			if instantBindingSQL != "" {
				joinSQL = " CROSS JOIN (" + instantBindingSQL + ") AS " + alias
			}
		case native.RenderModeRange:
			rangeBindingSQL, rangeParams, valueExpr, err := renderRangeScalarBindingFromLogical(ctx, quantile, alias)
			if err != nil {
				return renderedFragment{}, err
			}
			mergeRenderedQueryParams(queryParams, rangeParams)
			quantileValueExpr = valueExpr
			if rangeBindingSQL != "" {
				joinSQL = " LEFT JOIN (" + rangeBindingSQL + ") AS " + alias + " ON " + alias + ".timestamp = prepared_histograms.timestamp"
			}
		default:
			return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
		quantileLabelExpr := openMetricsFloatExpr(quantileValueExpr)
		rowsSQL := renderHistogramQuantileRowsSQL(preparedSQL, quantileValueExpr, joinSQL, alias)
		queries = append(queries, "SELECT arrayPushBack(tags, tuple("+sqlStringLiteral(n.Label)+", "+quantileLabelExpr+")) AS tags, timestamp AS timestamp, value AS value FROM ("+rowsSQL+") AS "+alias+"_rows")
	}
	unionSQL := strings.Join(queries, " UNION ALL ")
	return renderedFragment{RawSQL: trimRenderedQuerySQL(wrapHistogramRowsForMode(unionSQL, params.Mode, "histogram_quantiles_rows")), ExtraParams: queryParams}, nil
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
		return renderedFragment{Select: innerSelect, ExtraParams: histograms.ExtraParams, ExtraPhysicalDecisions: histograms.ExtraPhysicalDecisions}
	}
	innerSelect.OrderBy = []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}, {Expr: sqlb.Ident("timestamp")}}
	outerSelect := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"},
		},
		From:    sqlb.SubSelect{S: innerSelect, Alias: rangeAlias},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
	}
	return renderedFragment{Select: outerSelect, ExtraParams: histograms.ExtraParams, ExtraPhysicalDecisions: histograms.ExtraPhysicalDecisions}
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

func buildHistogramIdentityTagAggregationRowsSQL(sourceSQL string, params map[string]string, timestampExpr string) (string, map[string]string, error) {
	rows := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: sqlb.RawLit{V: timestampExpr}, Alias: "timestamp"},
			{Expr: sqlb.RawLit{V: "if(countIf(isNaN(value)) > 0, CAST(NULL, 'Nullable(Float64)'), sum(value))"}, Alias: "value"},
		},
		From:    rawRenderedSubquerySource(trimRenderedQuerySQL(sourceSQL)),
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
	}
	if timestampExpr == "timestamp" {
		rows.GroupBy = []sqlb.Expr{sqlb.Ident("tags"), sqlb.Ident("timestamp")}
	}
	sql, _, err := rows.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + schema.QuerySuffix, clonedParams, nil
}

// tryRenderHistogramChildRowsSQLLogical is the logical-plan entry to the
// histogram child-rows fast path. The capability check is keyed on the
// logical plan shape alone. Supported shapes:
//
//   - Range mode: the child is a SUM grouping aggregation whose source is a
//     range function over a leaf range-vector selector or a supported
//     subquery child — detected via canFuseRangeAggregationLogicalDirect.
//   - Instant mode: the child is a SUM aggregation (logical
//     AggregationPlan with Op=SUM). The inner source can be any
//     lowerable instant-vector child.
//
// For non-matching shapes the helper returns ok=false so the caller
// falls through to the full renderLogicalSubquery path. The
// "only-le-tags" shape check is performed on the logical tree via
// histogramChildUsesOnlyLETagsLogical.
func tryRenderHistogramChildRowsSQLLogical(cfg storage.QueryConfig, childNode logicalpkg.Node, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, params RenderParams, prefix string, optimizationReport *native.OptimizationReport) (string, map[string]string, bool, error) {
	if childNode == nil {
		return "", nil, false, nil
	}
	agg, ok := childNode.(*logicalpkg.AggregationPlan)
	if !ok || agg == nil {
		return "", nil, false, nil
	}
	ctx := LoweringCtx{Config: cfg, Analysis: logicalAnalysis, NativeAnalysis: analysis, OptimizationReport: optimizationReport, Params: params}
	var (
		rowsSQL   string
		rowParams map[string]string
		err       error
	)
	switch params.Mode {
	case native.RenderModeRange:
		if !canFuseRangeAggregationLogicalDirect(agg, params) {
			return "", nil, false, nil
		}
		if histogramChildUsesOnlyLETagsLogical(agg) {
			childRowsSQL, childRowParams, _, childErr := renderRangeFunctionRowsLogicalSQL(ctx, agg.Child)
			if childErr != nil {
				return "", nil, false, childErr
			}
			rowsSQL, rowParams, err = buildHistogramIdentityTagAggregationRowsSQL(childRowsSQL, childRowParams, "timestamp")
		} else {
			childCtx := ctx
			childCtx.Params = aggregationChildRenderParams(agg, params)
			rowsSQL, rowParams, _, err = renderFusedRangeAggregationLogicalRowsSQL(childCtx, agg)
		}
	case native.RenderModeInstant:
		if agg.Op != parser.SUM {
			return "", nil, false, nil
		}
		if agg.Child == nil {
			return "", nil, false, nil
		}
		childRendered, lowerErr := Lower(ctx, agg.Child)
		if lowerErr != nil {
			return "", nil, false, lowerErr
		}
		if histogramChildUsesOnlyLETagsLogical(agg) {
			rowsSQL, rowParams, err = buildHistogramIdentityTagAggregationRowsSQL(trimRenderedQuerySQL(childRendered.SQL), childRendered.QueryParams, "fromUnixTimestamp64Milli("+strconv.FormatInt(params.EvaluationTimeMS, 10)+")")
		} else {
			rowsSQL, rowParams, err = storage.BuildInstantAggregationRowsSubquerySQL(storage.AggregationSource{ValueExpr: "{value}", TagsExpr: "{tags}"}, trimRenderedQuerySQL(childRendered.SQL), childRendered.QueryParams, params.EvaluationTimeMS, agg.Op, agg.Grouping, agg.Without, agg.ParamNumber, agg.ParamString)
		}
	default:
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rowsSQL), rowParams, prefix)
	if err != nil {
		return "", nil, false, err
	}
	return namespacedSQL, namespacedParams, true, nil
}
