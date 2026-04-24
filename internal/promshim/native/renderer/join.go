package renderer

import (
	"ch-observability/internal/promshim/native"
	"fmt"
	"strconv"
	"strings"

	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
	"ch-observability/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func renderBinaryJoinFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment.BinaryJoin == nil || fragment.BinaryJoin.LHS == nil || fragment.BinaryJoin.RHS == nil {
		return renderedFragment{}, fmt.Errorf("binary join fragment is missing join metadata")
	}
	joinCfg := storage.BinaryJoinConfig{
		Op:             fragment.BinaryJoin.Op,
		ReturnBool:     fragment.BinaryJoin.ReturnBool,
		VectorMatching: native.CloneVectorMatching(fragment.BinaryJoin.VectorMatching),
		JoinShape:      fragment.BinaryJoin.JoinShape,
	}
	switch params.Mode {
	case native.RenderModeInstant:
		lhsSQL, lhsParams, err := renderFragmentSubquery(cfg, fragment.BinaryJoin.LHS, params, "lhs")
		if err != nil {
			return renderedFragment{}, err
		}
		rhsSQL, rhsParams, err := renderFragmentSubquery(cfg, fragment.BinaryJoin.RHS, params, "rhs")
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams, err := storage.BuildInstantBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		lhsRequiredStartMS, lhsRequiredEndMS, _ := native.RequiredInputBounds(fragment.BinaryJoin.LHS, nil, native.OptimizationContext{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS})
		rhsRequiredStartMS, rhsRequiredEndMS, _ := native.RequiredInputBounds(fragment.BinaryJoin.RHS, nil, native.OptimizationContext{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS})
		lhsSQL, lhsParams, err := renderFragmentSubquery(cfg, fragment.BinaryJoin.LHS, RenderParams{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: lhsRequiredStartMS, RequiredEndMS: lhsRequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL}, "lhs")
		if err != nil {
			return renderedFragment{}, err
		}
		rhsSQL, rhsParams, err := renderFragmentSubquery(cfg, fragment.BinaryJoin.RHS, RenderParams{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: rhsRequiredStartMS, RequiredEndMS: rhsRequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL}, "rhs")
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams, err := storage.BuildRangeBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAggregationFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment.Aggregation == nil || fragment.Aggregation.Source == nil {
		return renderedFragment{}, fmt.Errorf("aggregation fragment is missing aggregation metadata")
	}
	sourceFragment := fragment.Aggregation.Source
	if native.IsSelectionNativeAggregation(fragment.Aggregation.Op) || fragment.Aggregation.Op == parser.COUNT_VALUES {
		sourceFragment = native.CloneFragment(sourceFragment)
		// count_values, like the selection aggregations, synthesizes output labels
		// from the input series labelset. It therefore cannot lower through the
		// empty-tags selector fast path.
		forceFragmentFullTags(sourceFragment)
	}
	if fused, ok, err := tryRenderFusedRangeAggregationFragment(cfg, fragment, params); err != nil {
		return renderedFragment{}, err
	} else if ok {
		return fused, nil
	}
	if source, err := renderAggregationSource(sourceFragment, params); err == nil {
		switch params.Mode {
		case native.RenderModeInstant:
			sql, queryParams, err := storage.BuildInstantAggregationQuerySQLWithBounds(cfg, source, params.EvaluationTimeMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber, fragment.Aggregation.ParamString)
			if err != nil {
				return renderedFragment{}, err
			}
			if fragment.Aggregation.EmitZeroOnEmpty {
				return wrapZeroOnEmptyAggregationInstantSQL(trimRenderedQuerySQL(sql), queryParams, params), nil
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		case native.RenderModeRange:
			sql, queryParams, err := storage.BuildRangeAggregationQuerySQLWithBounds(cfg, source, params.StartMS, params.EndMS, params.StepMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber, fragment.Aggregation.ParamString)
			if err != nil {
				return renderedFragment{}, err
			}
			if fragment.Aggregation.EmitZeroOnEmpty {
				return wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, params), nil
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		default:
			return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
	}
	childSQL, childParams, err := renderFragmentSubquery(cfg, sourceFragment, params, "aggregation_child")
	if err != nil {
		return renderedFragment{}, err
	}
	source := storage.AggregationSource{ValueExpr: "{value}", TagsExpr: "{tags}"}
	switch params.Mode {
	case native.RenderModeInstant:
		sql, queryParams, err := storage.BuildInstantAggregationOverSubquerySQL(source, childSQL, childParams, params.EvaluationTimeMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber, fragment.Aggregation.ParamString)
		if err != nil {
			return renderedFragment{}, err
		}
		if fragment.Aggregation.EmitZeroOnEmpty {
			return wrapZeroOnEmptyAggregationInstantSQL(trimRenderedQuerySQL(sql), queryParams, params), nil
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		sql, queryParams, err := storage.BuildRangeAggregationOverSubquerySQL(source, childSQL, childParams, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber, fragment.Aggregation.ParamString)
		if err != nil {
			return renderedFragment{}, err
		}
		if fragment.Aggregation.EmitZeroOnEmpty {
			return wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, params), nil
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func wrapZeroOnEmptyAggregationInstantSQL(aggregationSQL string, queryParams map[string]string, params RenderParams) renderedFragment {
	namespaced := map[string]string{}
	mergeRenderedQueryParams(namespaced, queryParams)
	namespaced["param_zero_fill_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
	sql := "SELECT tags, timestamp, value FROM (" + aggregationSQL + ") AS aggregated UNION ALL " +
		"SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({zero_fill_evaluation_ms:Int64}) AS timestamp, toFloat64(0) AS value WHERE NOT EXISTS (SELECT 1 FROM (" + aggregationSQL + ") AS aggregated_probe)"
	return renderedFragment{RawSQL: sql, ExtraParams: namespaced}
}

func wrapZeroOnEmptyAggregationRangeSQL(aggregationSQL string, queryParams map[string]string, params RenderParams) renderedFragment {
	namespaced := map[string]string{}
	mergeRenderedQueryParams(namespaced, queryParams)
	namespaced["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
	namespaced["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
	namespaced["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
	presenceSQL := "SELECT point.1 AS timestamp, count() AS sample_count FROM (" + aggregationSQL + ") AS aggregated_presence ARRAY JOIN aggregated_presence.time_series AS point GROUP BY point.1"
	sql := "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" +
		"SELECT aggregated.tags AS tags, point.1 AS timestamp, point.2 AS value FROM (" + aggregationSQL + ") AS aggregated ARRAY JOIN aggregated.time_series AS point UNION ALL " +
		"SELECT CAST([], 'Array(Tuple(String, String))') AS tags, grid.timestamp AS timestamp, toFloat64(0) AS value FROM (" + buildTimestampGridSQL() + ") AS grid LEFT JOIN (" + presenceSQL + ") AS present ON present.timestamp = grid.timestamp WHERE ifNull(present.sample_count, 0) = 0" +
		") AS zero_filled_steps GROUP BY tags ORDER BY tags"
	return renderedFragment{RawSQL: sql, ExtraParams: namespaced}
}

func renderInfoJoinFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.InfoJoin == nil || fragment.InfoJoin.Child == nil {
		return renderedFragment{}, fmt.Errorf("info join fragment is missing child metadata")
	}
	childFragment := native.CloneFragment(fragment.InfoJoin.Child)
	forceFragmentFullTags(childFragment)
	childSQL, childParams, err := renderFragmentSubquery(cfg, childFragment, params, "info_lhs")
	if err != nil {
		return renderedFragment{}, err
	}
	return assembleInfoJoinSQL(cfg, childSQL, childParams, fragment.InfoJoin.InfoMetricName, fragment.InfoJoin.SelectorMatchers, fragment.InfoJoin.CopyLabelNames, fragment.InfoJoin.DropUnmatched, params)
}

// assembleInfoJoinSQL builds the info-join outer query from an already
// rendered, namespaced child source ("info_lhs") and info-series metadata.
// Shared by the Fragment path (renderInfoJoinFragment) and the direct path
// (lowerInfoJoin) so SQL stays byte-identical.
func assembleInfoJoinSQL(cfg storage.QueryConfig, childSQL string, childParams map[string]string, infoMetricName string, selectorMatchers []*labels.Matcher, copyLabelNames []string, dropUnmatched bool, params RenderParams) (renderedFragment, error) {
	selector := storage.SelectorSource{Kind: storage.SelectorKindInstantVector, MetricName: infoMetricName, Matchers: native.CloneMatchers(selectorMatchers), NeedTags: true, RequireFullTags: true, LookbackMS: native.DefaultInstantSelectorLookback.Milliseconds()}
	infoNameMatchers := infoJoinNameMatchers(selectorMatchers, infoMetricName)
	joinCfg := storage.InfoJoinConfig{IdentifyingLabels: []string{"instance", "job"}, CopyLabelNames: append([]string(nil), copyLabelNames...), DropUnmatched: dropUnmatched, InfoNameMatchers: infoNameMatchers}
	switch params.Mode {
	case native.RenderModeInstant:
		infoSQL, infoParams, err := storage.BuildInstantSelectorQuerySQL(cfg, selector, params.EvaluationTimeMS-native.DefaultInstantSelectorLookback.Milliseconds(), params.EvaluationTimeMS)
		if err != nil {
			return renderedFragment{}, err
		}
		joinSQL, joinParams, err := storage.BuildInstantInfoJoinSQL(childSQL, childParams, trimRenderedQuerySQL(infoSQL), infoParams, joinCfg)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(joinSQL), ExtraParams: joinParams}, nil
	case native.RenderModeRange:
		requiredStartMS := params.StartMS - native.DefaultInstantSelectorLookback.Milliseconds()
		infoSQL, infoParams, err := storage.BuildRangeSelectorQuerySQL(cfg, selector, requiredStartMS, params.EndMS, params.StartMS, params.EndMS, params.StepMS)
		if err != nil {
			return renderedFragment{}, err
		}
		joinSQL, joinParams, err := storage.BuildRangeInfoJoinSQL(childSQL, childParams, trimRenderedQuerySQL(infoSQL), infoParams, joinCfg)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(joinSQL), ExtraParams: joinParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

// infoJoinNameMatchers derives the metric-name matcher list the info-join
// SQL builder expects, given either explicit MetricName-matchers inside
// selectorMatchers or a single equality on infoMetricName as the fallback.
func infoJoinNameMatchers(selectorMatchers []*labels.Matcher, infoMetricName string) []*labels.Matcher {
	nameMatchers := make([]*labels.Matcher, 0)
	for _, matcher := range selectorMatchers {
		if matcher == nil || matcher.Name != labels.MetricName {
			continue
		}
		nameMatchers = append(nameMatchers, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	if len(nameMatchers) == 0 && infoMetricName != "" {
		nameMatchers = append(nameMatchers, labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, infoMetricName))
	}
	return nameMatchers
}

func renderValueTransformFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.ValueTransform == nil || fragment.ValueTransform.Child == nil {
		return renderedFragment{}, fmt.Errorf("value transform fragment is missing child metadata")
	}
	spec := fragment.ValueTransform
	childRendered, err := RenderFragment(cfg, spec.Child, params)
	if err != nil {
		return renderedFragment{}, err
	}
	return renderValueTransformFromSource(trimRenderedQuerySQL(childRendered.SQL), childRendered.QueryParams, spec.ValueExpr, spec.FilterExpr, spec.DropsMetric, params)
}

// renderValueTransformFromSource wraps a pre-rendered child source in the
// value-transform outer SELECT (instant or range mode). Shared by the
// Fragment path (renderValueTransformFragment) and the direct path
// (lowerRound, and future lowerUnary / lowerClamp / scalar-BinaryPlan
// conversions) so SQL stays byte-identical.
//
// childSQL is the trimmed child SQL body embedded verbatim as the FROM source;
// childParams are the child's QueryParams passed through to ExtraParams without
// namespacing (matching current Fragment-path behavior).
func renderValueTransformFromSource(childSQL string, childParams map[string]string, valueExpr, filterExpr string, dropsMetric bool, params RenderParams) (renderedFragment, error) {
	if strings.TrimSpace(valueExpr) == "" {
		return renderedFragment{}, fmt.Errorf("value transform requires a value expression")
	}
	tagsTemplate := "{tags}"
	if dropsMetric {
		tagsTemplate = "arrayFilter(tag -> tag.1 != '__name__', {tags})"
	}
	switch params.Mode {
	case native.RenderModeInstant:
		tagsExpr, err := storage.CompileSourceTagsTemplate(tagsTemplate, sqlb.Ident("tags"))
		if err != nil {
			return renderedFragment{}, err
		}
		valueCompiled, err := storage.CompileSourceValueTemplate(valueExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
		if err != nil {
			return renderedFragment{}, err
		}
		query := &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: tagsExpr, Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: valueCompiled, Alias: "value"}},
			From:    rawRenderedSubquerySource(childSQL),
		}
		if strings.TrimSpace(filterExpr) != "" {
			filterCompiled, err := storage.CompileSourceValueTemplate(filterExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
			if err != nil {
				return renderedFragment{}, err
			}
			query.Where = filterCompiled
		}
		sql, err := buildNativeWrapperSQL(query)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
	case native.RenderModeRange:
		tagsExpr, err := storage.CompileSourceTagsTemplate(tagsTemplate, sqlb.Ident("tags"))
		if err != nil {
			return renderedFragment{}, err
		}
		valueCompiled, err := storage.CompileSourceValueTemplate(valueExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
		if err != nil {
			return renderedFragment{}, err
		}
		valueSQL, valueParams, err := sqlb.BuildExpr(valueCompiled)
		if err != nil {
			return renderedFragment{}, err
		}
		if len(valueParams) != 0 {
			return renderedFragment{}, fmt.Errorf("value transform range value template unexpectedly produced params: %#v", valueParams)
		}
		seriesExpr := "time_series"
		if strings.TrimSpace(filterExpr) != "" {
			filterCompiled, err := storage.CompileSourceValueTemplate(filterExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
			if err != nil {
				return renderedFragment{}, err
			}
			filterSQL, filterParams, err := sqlb.BuildExpr(filterCompiled)
			if err != nil {
				return renderedFragment{}, err
			}
			if len(filterParams) != 0 {
				return renderedFragment{}, fmt.Errorf("value transform range filter template unexpectedly produced params: %#v", filterParams)
			}
			seriesExpr = "arrayFilter(point -> " + filterSQL + ", time_series)"
		}
		query := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: tagsExpr, Alias: "tags"},
				{Expr: sqlb.RawLit{V: "arrayMap(point -> (point.1, " + valueSQL + "), " + seriesExpr + ")"}, Alias: "time_series"},
			},
			From: rawRenderedSubquerySource(childSQL),
		}
		if strings.TrimSpace(filterExpr) != "" {
			query.Where = sqlb.RawLit{V: "length(" + seriesExpr + ") > 0"}
		}
		sql, err := buildNativeWrapperSQL(query)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAbsentFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.Absent == nil || fragment.Absent.Child == nil {
		return renderedFragment{}, fmt.Errorf("absent fragment is missing child metadata")
	}
	queryParams := map[string]string{}
	tagsSQL := outputMetricTagsSQL(fragment.Absent.OutputMetric)
	switch fragment.Absent.Func {
	case "absent":
		childSQL, childParams, err := renderFragmentSubquery(cfg, fragment.Absent.Child, params, "absent_child")
		if err != nil {
			return renderedFragment{}, err
		}
		return renderAbsentFromNamespacedChild(tagsSQL, childSQL, childParams, params)
	case "absent_over_time":
		switch params.Mode {
		case native.RenderModeInstant:
			childSQL, childParams, err := renderFragmentSubquery(cfg, fragment.Absent.Child, params, "absent_child")
			if err != nil {
				return renderedFragment{}, err
			}
			mergeRenderedQueryParams(queryParams, childParams)
			queryParams["param_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
			sql := "SELECT " + tagsSQL + " AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(1) AS value FROM (SELECT count() AS sample_count FROM (" + childSQL + ") AS absent_child ARRAY JOIN absent_child.time_series AS point) AS probe WHERE probe.sample_count = 0"
			return renderedFragment{RawSQL: sql, ExtraParams: queryParams}, nil
		case native.RenderModeRange:
			queryParams["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
			queryParams["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
			queryParams["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
			windowedSQL, childParams, err := renderAbsentOverTimeWindowedSource(cfg, fragment.Absent.Child, params)
			if err != nil {
				return renderedFragment{}, err
			}
			mergeRenderedQueryParams(queryParams, childParams)
			presenceSQL := "SELECT eval_ts AS timestamp, sum(length(window_series)) AS sample_count FROM (" + trimRenderedQuerySQL(windowedSQL) + ") AS absent_windows GROUP BY eval_ts"
			return renderedFragment{RawSQL: trimRenderedQuerySQL(buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL)), ExtraParams: queryParams}, nil
		default:
			return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
	default:
		return renderedFragment{}, fmt.Errorf("absent fragment function %q is not implemented yet", fragment.Absent.Func)
	}
}

// renderAbsentFromNamespacedChild builds the absent() body for both instant
// and range modes given a child already namespaced under the "absent_child"
// alias. Shared between the Fragment path and the direct-render AbsentPlan
// lowerer so both produce byte-identical SQL.
func renderAbsentFromNamespacedChild(tagsSQL, childSQL string, childParams map[string]string, params RenderParams) (renderedFragment, error) {
	queryParams := map[string]string{}
	mergeRenderedQueryParams(queryParams, childParams)
	switch params.Mode {
	case native.RenderModeInstant:
		queryParams["param_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
		sql := "SELECT " + tagsSQL + " AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(1) AS value FROM (SELECT count() AS sample_count FROM (" + childSQL + ") AS absent_child) AS probe WHERE probe.sample_count = 0"
		return renderedFragment{RawSQL: sql, ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		queryParams["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
		queryParams["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
		queryParams["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
		presenceSQL := "SELECT point.1 AS timestamp, count() AS sample_count FROM (" + childSQL + ") AS absent_child ARRAY JOIN absent_child.time_series AS point GROUP BY point.1"
		return renderedFragment{RawSQL: trimRenderedQuerySQL(buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL)), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAbsentOverTimeWindowedSource(cfg storage.QueryConfig, child *native.NativeFragment, params RenderParams) (string, map[string]string, error) {
	if child == nil {
		return "", nil, fmt.Errorf("absent_over_time child fragment is missing")
	}
	if child.Kind == native.FragmentKindLeafSource && child.Selector != nil && child.Selector.Kind == native.SelectorKindRangeVector {
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(child, params.StartMS, params.EndMS)
		rendered, err := RenderFragment(cfg, child, RenderParams{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: childRequiredStartMS, RequiredEndMS: childRequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL})
		if err != nil {
			return "", nil, err
		}
		namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, "absent_window_child")
		if err != nil {
			return "", nil, err
		}
		windowedSQL, err := buildWindowedArraysSourceSQL(namespacedSQL, "absent_over_time", params.StartMS, params.EndMS, params.StepMS, child.Selector.Lookback.Milliseconds(), child.Selector.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return windowedSQL, namespacedParams, nil
	}
	if child.Kind == native.FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil {
		rendered, err := RenderFragment(cfg, child, RenderParams{Mode: native.RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: params.RequiredStartMS, RequiredEndMS: params.RequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL})
		if err != nil {
			return "", nil, err
		}
		namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, "absent_window_child")
		if err != nil {
			return "", nil, err
		}
		windowedSQL, err := buildWindowedArraysSourceSQL(namespacedSQL, "absent_over_time", params.StartMS, params.EndMS, params.StepMS, child.Subquery.Range.Milliseconds(), child.Subquery.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return windowedSQL, namespacedParams, nil
	}
	return "", nil, fmt.Errorf("native range-mode rendering for absent_over_time currently requires a direct range-vector selector child or supported subquery child")
}

func buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL string) string {
	return "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" +
		"SELECT " + tagsSQL + " AS tags, grid.timestamp AS timestamp, toFloat64(1) AS value FROM (" + buildTimestampGridSQL() + ") AS grid LEFT JOIN (" + presenceSQL + ") AS present ON present.timestamp = grid.timestamp WHERE ifNull(present.sample_count, 0) = 0 ORDER BY timestamp" +
		") AS missing_steps GROUP BY tags HAVING length(time_series) > 0" + schema.FormatSuffix
}

func buildTimestampGridSQL() string {
	return "SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS timestamp"
}
