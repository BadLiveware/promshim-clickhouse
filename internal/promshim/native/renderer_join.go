package native

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
)

func renderBinaryJoinFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment.BinaryJoin == nil || fragment.BinaryJoin.LHS == nil || fragment.BinaryJoin.RHS == nil {
		return RenderedQuery{}, fmt.Errorf("binary join fragment is missing join metadata")
	}
	lhsSQL, lhsParams, err := renderFragmentSubquery(cfg, fragment.BinaryJoin.LHS, params, "lhs")
	if err != nil {
		return RenderedQuery{}, err
	}
	rhsSQL, rhsParams, err := renderFragmentSubquery(cfg, fragment.BinaryJoin.RHS, params, "rhs")
	if err != nil {
		return RenderedQuery{}, err
	}
	joinCfg := storage.BinaryJoinConfig{
		Op:             fragment.BinaryJoin.Op,
		ReturnBool:     fragment.BinaryJoin.ReturnBool,
		VectorMatching: cloneVectorMatching(fragment.BinaryJoin.VectorMatching),
		JoinShape:      fragment.BinaryJoin.JoinShape,
	}
	switch params.Mode {
	case RenderModeInstant:
		sql, queryParams, err := storage.BuildInstantBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	case RenderModeRange:
		sql, queryParams, err := storage.BuildRangeBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAggregationFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment.Aggregation == nil || fragment.Aggregation.Source == nil {
		return RenderedQuery{}, fmt.Errorf("aggregation fragment is missing aggregation metadata")
	}
	if source, err := renderAggregationSource(fragment.Aggregation.Source, params); err == nil {
		switch params.Mode {
		case RenderModeInstant:
			sql, queryParams, err := storage.BuildInstantAggregationQuerySQLWithBounds(cfg, source, params.EvaluationTimeMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber)
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
		case RenderModeRange:
			sql, queryParams, err := storage.BuildRangeAggregationQuerySQLWithBounds(cfg, source, params.StartMS, params.EndMS, params.StepMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber)
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
		default:
			return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
	}
	childSQL, childParams, err := renderFragmentSubquery(cfg, fragment.Aggregation.Source, params, "aggregation_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	source := storage.AggregationSource{ValueExpr: "{value}", TagsExpr: "{tags}"}
	switch params.Mode {
	case RenderModeInstant:
		sql, queryParams, err := storage.BuildInstantAggregationOverSubquerySQL(source, childSQL, childParams, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	case RenderModeRange:
		sql, queryParams, err := storage.BuildRangeAggregationOverSubquerySQL(source, childSQL, childParams, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without, fragment.Aggregation.ParamNumber)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderInfoJoinFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.InfoJoin == nil || fragment.InfoJoin.Child == nil {
		return RenderedQuery{}, fmt.Errorf("info join fragment is missing child metadata")
	}
	childFragment := cloneFragment(fragment.InfoJoin.Child)
	forceFragmentFullTags(childFragment)
	childSQL, childParams, err := renderFragmentSubquery(cfg, childFragment, params, "info_lhs")
	if err != nil {
		return RenderedQuery{}, err
	}
	selector := storage.SelectorSource{Kind: storage.SelectorKindInstantVector, MetricName: fragment.InfoJoin.InfoMetricName, Matchers: cloneMatchers(fragment.InfoJoin.SelectorMatchers), NeedTags: true, RequireFullTags: true, LookbackMS: defaultInstantSelectorLookback.Milliseconds()}
	var infoSQL string
	var infoParams map[string]string
	switch params.Mode {
	case RenderModeInstant:
		infoSQL, infoParams, err = storage.BuildInstantSelectorQuerySQL(cfg, selector, params.EvaluationTimeMS-defaultInstantSelectorLookback.Milliseconds(), params.EvaluationTimeMS)
		if err != nil {
			return RenderedQuery{}, err
		}
		joinSQL, joinParams, err := storage.BuildInstantInfoJoinSQL(childSQL, childParams, infoSQL, infoParams, storage.InfoJoinConfig{IdentifyingLabels: []string{"instance", "job"}, CopyLabelNames: append([]string(nil), fragment.InfoJoin.CopyLabelNames...), DropUnmatched: fragment.InfoJoin.DropUnmatched})
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: joinSQL, QueryParams: joinParams}, nil
	case RenderModeRange:
		requiredStartMS := params.StartMS - defaultInstantSelectorLookback.Milliseconds()
		infoSQL, infoParams, err = storage.BuildRangeSelectorQuerySQL(cfg, selector, requiredStartMS, params.EndMS, params.StartMS, params.EndMS, params.StepMS)
		if err != nil {
			return RenderedQuery{}, err
		}
		joinSQL, joinParams, err := storage.BuildRangeInfoJoinSQL(childSQL, childParams, infoSQL, infoParams, storage.InfoJoinConfig{IdentifyingLabels: []string{"instance", "job"}, CopyLabelNames: append([]string(nil), fragment.InfoJoin.CopyLabelNames...), DropUnmatched: fragment.InfoJoin.DropUnmatched})
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: joinSQL, QueryParams: joinParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderValueTransformFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.ValueTransform == nil || fragment.ValueTransform.Child == nil {
		return RenderedQuery{}, fmt.Errorf("value transform fragment is missing child metadata")
	}
	spec := fragment.ValueTransform
	if strings.TrimSpace(spec.ValueExpr) == "" {
		return RenderedQuery{}, fmt.Errorf("value transform fragment requires a value expression")
	}
	childRendered, err := RenderFragment(cfg, spec.Child, params)
	if err != nil {
		return RenderedQuery{}, err
	}
	tagsTemplate := "{tags}"
	if spec.DropsMetric {
		tagsTemplate = "arrayFilter(tag -> tag.1 != '__name__', {tags})"
	}
	switch params.Mode {
	case RenderModeInstant:
		tagsExpr, err := storage.CompileSourceTagsTemplate(tagsTemplate, sqlb.Ident("tags"))
		if err != nil {
			return RenderedQuery{}, err
		}
		valueExpr, err := storage.CompileSourceValueTemplate(spec.ValueExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
		if err != nil {
			return RenderedQuery{}, err
		}
		query := &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: tagsExpr, Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: valueExpr, Alias: "value"}},
			From:    rawRenderedSubquerySource(trimRenderedQuerySQL(childRendered.SQL)),
		}
		if strings.TrimSpace(spec.FilterExpr) != "" {
			filterExpr, err := storage.CompileSourceValueTemplate(spec.FilterExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
			if err != nil {
				return RenderedQuery{}, err
			}
			query.Where = filterExpr
		}
		sql, err := buildNativeWrapperSQL(query)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: childRendered.QueryParams}, nil
	case RenderModeRange:
		tagsExpr, err := storage.CompileSourceTagsTemplate(tagsTemplate, sqlb.Ident("tags"))
		if err != nil {
			return RenderedQuery{}, err
		}
		valueExpr, err := storage.CompileSourceValueTemplate(spec.ValueExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
		if err != nil {
			return RenderedQuery{}, err
		}
		valueSQL, valueParams, err := sqlb.BuildExpr(valueExpr)
		if err != nil {
			return RenderedQuery{}, err
		}
		if len(valueParams) != 0 {
			return RenderedQuery{}, fmt.Errorf("value transform range value template unexpectedly produced params: %#v", valueParams)
		}
		seriesExpr := "time_series"
		if strings.TrimSpace(spec.FilterExpr) != "" {
			filterExpr, err := storage.CompileSourceValueTemplate(spec.FilterExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
			if err != nil {
				return RenderedQuery{}, err
			}
			filterSQL, filterParams, err := sqlb.BuildExpr(filterExpr)
			if err != nil {
				return RenderedQuery{}, err
			}
			if len(filterParams) != 0 {
				return RenderedQuery{}, fmt.Errorf("value transform range filter template unexpectedly produced params: %#v", filterParams)
			}
			seriesExpr = "arrayFilter(point -> " + filterSQL + ", time_series)"
		}
		query := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: tagsExpr, Alias: "tags"},
				{Expr: sqlb.RawLit{V: "arrayMap(point -> (point.1, " + valueSQL + "), " + seriesExpr + ")"}, Alias: "time_series"},
			},
			From: rawRenderedSubquerySource(trimRenderedQuerySQL(childRendered.SQL)),
		}
		sql, err := buildNativeWrapperSQL(query)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: childRendered.QueryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAbsentFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.Absent == nil || fragment.Absent.Child == nil {
		return RenderedQuery{}, fmt.Errorf("absent fragment is missing child metadata")
	}
	queryParams := map[string]string{}
	tagsSQL := outputMetricTagsSQL(fragment.Absent.OutputMetric)
	switch fragment.Absent.Func {
	case "absent":
		childSQL, childParams, err := renderFragmentSubquery(cfg, fragment.Absent.Child, params, "absent_child")
		if err != nil {
			return RenderedQuery{}, err
		}
		mergeRenderedQueryParams(queryParams, childParams)
		switch params.Mode {
		case RenderModeInstant:
			queryParams["param_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
			sql := "SELECT " + tagsSQL + " AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(1) AS value FROM (SELECT count() AS sample_count FROM (" + childSQL + ") AS absent_child) AS probe WHERE probe.sample_count = 0\nFORMAT JSONEachRow\n"
			return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
		case RenderModeRange:
			queryParams["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
			queryParams["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
			queryParams["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
			presenceSQL := "SELECT point.1 AS timestamp, count() AS sample_count FROM (" + childSQL + ") AS absent_child ARRAY JOIN absent_child.time_series AS point GROUP BY point.1"
			return RenderedQuery{SQL: buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL), QueryParams: queryParams}, nil
		default:
			return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
	case "absent_over_time":
		switch params.Mode {
		case RenderModeInstant:
			childSQL, childParams, err := renderFragmentSubquery(cfg, fragment.Absent.Child, params, "absent_child")
			if err != nil {
				return RenderedQuery{}, err
			}
			mergeRenderedQueryParams(queryParams, childParams)
			queryParams["param_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
			sql := "SELECT " + tagsSQL + " AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(1) AS value FROM (SELECT count() AS sample_count FROM (" + childSQL + ") AS absent_child ARRAY JOIN absent_child.time_series AS point) AS probe WHERE probe.sample_count = 0\nFORMAT JSONEachRow\n"
			return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
		case RenderModeRange:
			queryParams["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
			queryParams["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
			queryParams["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
			windowedSQL, childParams, err := renderAbsentOverTimeWindowedSource(cfg, fragment.Absent.Child, params)
			if err != nil {
				return RenderedQuery{}, err
			}
			mergeRenderedQueryParams(queryParams, childParams)
			presenceSQL := "SELECT eval_ts AS timestamp, sum(length(window_series)) AS sample_count FROM (" + trimRenderedQuerySQL(windowedSQL) + ") AS absent_windows GROUP BY eval_ts"
			return RenderedQuery{SQL: buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL), QueryParams: queryParams}, nil
		default:
			return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
		}
	default:
		return RenderedQuery{}, fmt.Errorf("absent fragment function %q is not implemented yet", fragment.Absent.Func)
	}
}

func renderAbsentOverTimeWindowedSource(cfg storage.QueryConfig, child *NativeFragment, params RenderParams) (string, map[string]string, error) {
	if child == nil {
		return "", nil, fmt.Errorf("absent_over_time child fragment is missing")
	}
	if child.Kind == FragmentKindLeafSource && child.Selector != nil && child.Selector.Kind == SelectorKindRangeVector {
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(child, params.StartMS, params.EndMS)
		rendered, err := RenderFragment(cfg, child, RenderParams{Mode: RenderModeRange, StartMS: params.StartMS, EndMS: params.EndMS, StepMS: params.StepMS, RequiredStartMS: childRequiredStartMS, RequiredEndMS: childRequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL})
		if err != nil {
			return "", nil, err
		}
		namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, "absent_window_child")
		if err != nil {
			return "", nil, err
		}
		windowedSQL, err := buildWindowedArraysSourceSQL(namespacedSQL, params.StartMS, params.EndMS, params.StepMS, child.Selector.Lookback.Milliseconds(), child.Selector.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return windowedSQL, namespacedParams, nil
	}
	if child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil {
		subqueryStep := child.Subquery.Step
		if subqueryStep <= 0 {
			subqueryStep = time.Minute
		}
		expandedEndMS := params.EndMS - child.Subquery.Offset.Milliseconds()
		expandedStartMS := params.StartMS - child.Subquery.Offset.Milliseconds() - child.Subquery.Range.Milliseconds()
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(child.Subquery.Child, expandedStartMS, expandedEndMS)
		rendered, err := RenderFragment(cfg, child.Subquery.Child, RenderParams{Mode: RenderModeRange, StartMS: expandedStartMS, EndMS: expandedEndMS, StepMS: subqueryStep.Milliseconds(), RequiredStartMS: childRequiredStartMS, RequiredEndMS: childRequiredEndMS, ResolveSourcePromQL: params.ResolveSourcePromQL})
		if err != nil {
			return "", nil, err
		}
		namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, "absent_window_child")
		if err != nil {
			return "", nil, err
		}
		windowedSQL, err := buildWindowedArraysSourceSQL(namespacedSQL, params.StartMS, params.EndMS, params.StepMS, child.Subquery.Range.Milliseconds(), child.Subquery.Offset.Milliseconds())
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
		") AS missing_steps GROUP BY tags HAVING length(time_series) > 0\nFORMAT JSONEachRow\n"
}

func buildTimestampGridSQL() string {
	return "SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS timestamp"
}
