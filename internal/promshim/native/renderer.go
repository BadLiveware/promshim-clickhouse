package native

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type RenderMode string

const (
	RenderModeInstant RenderMode = "instant"
	RenderModeRange   RenderMode = "range"
)

type RenderParams struct {
	Mode                RenderMode
	EvaluationTimeMS    int64
	StartMS             int64
	EndMS               int64
	StepMS              int64
	RequiredStartMS     int64
	RequiredEndMS       int64
	ResolveSourcePromQL func(parser.Expr) (string, error)
}

type RenderedQuery struct {
	SQL         string
	QueryParams map[string]string
}

func RenderFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil {
		return RenderedQuery{}, fmt.Errorf("native fragment render requires a fragment")
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr:
		return renderSourceFragment(cfg, fragment, params)
	case FragmentKindSyntheticSeries:
		return renderSyntheticFragment(fragment, params)
	case FragmentKindScalarConvert:
		return renderScalarConvertFragment(cfg, fragment, params)
	case FragmentKindInfoJoin:
		return renderInfoJoinFragment(cfg, fragment, params)
	case FragmentKindAbsent:
		return renderAbsentFragment(cfg, fragment, params)
	case FragmentKindSubquery:
		return renderSubqueryFragment(cfg, fragment, params)
	case FragmentKindRangeFunction:
		return renderRangeFunctionFragment(cfg, fragment, params)
	case FragmentKindBinaryVectorJoin:
		return renderBinaryJoinFragment(cfg, fragment, params)
	case FragmentKindAggregation:
		return renderAggregationFragment(cfg, fragment, params)
	default:
		return RenderedQuery{}, fmt.Errorf("native SQL rendering for fragment kind %q is not implemented yet", fragment.Kind)
	}
}

func renderSubqueryFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment.Subquery == nil || fragment.Subquery.Child == nil {
		return RenderedQuery{}, fmt.Errorf("subquery fragment is missing subquery metadata")
	}
	if params.Mode != RenderModeInstant {
		return RenderedQuery{}, fmt.Errorf("native subquery rendering in %s mode is not implemented yet", params.Mode)
	}
	endMS := params.EvaluationTimeMS
	if fragment.Subquery.Timestamp != nil {
		endMS = *fragment.Subquery.Timestamp
	}
	if fragment.Subquery.Offset != 0 {
		endMS -= fragment.Subquery.Offset.Milliseconds()
	}
	step := fragment.Subquery.Step
	if step <= 0 {
		step = time.Minute
	}
	startMS := endMS - fragment.Subquery.Range.Milliseconds()
	childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(fragment.Subquery.Child, startMS, endMS)
	childRendered, err := RenderFragment(cfg, fragment.Subquery.Child, RenderParams{
		Mode:                RenderModeRange,
		StartMS:             startMS,
		EndMS:               endMS,
		StepMS:              step.Milliseconds(),
		RequiredStartMS:     childRequiredStartMS,
		RequiredEndMS:       childRequiredEndMS,
		ResolveSourcePromQL: params.ResolveSourcePromQL,
	})
	if err != nil {
		return RenderedQuery{}, err
	}
	return childRendered, nil
}

func renderRangeFunctionFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return RenderedQuery{}, fmt.Errorf("range function fragment is missing range metadata")
	}
	switch params.Mode {
	case RenderModeInstant:
		childRendered, err := RenderFragment(cfg, fragment.RangeFunction.Child, params)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, err := buildInstantRangeFunctionSQL(childRendered.SQL, fragment.RangeFunction.Func, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.EvaluationTimeMS, rangeFunctionChildRangeMS(fragment.RangeFunction.Child))
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: childRendered.QueryParams}, nil
	case RenderModeRange:
		selectorFragment := fragment.RangeFunction.Child
		if selectorFragment != nil && selectorFragment.Kind == FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == SelectorKindRangeVector {
			childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
			childRendered, err := RenderFragment(cfg, selectorFragment, RenderParams{
				Mode:                RenderModeRange,
				StartMS:             params.StartMS,
				EndMS:               params.EndMS,
				StepMS:              params.StepMS,
				RequiredStartMS:     childRequiredStartMS,
				RequiredEndMS:       childRequiredEndMS,
				ResolveSourcePromQL: params.ResolveSourcePromQL,
			})
			if err != nil {
				return RenderedQuery{}, err
			}
			sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fragment.RangeFunction.Func, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Selector.Lookback.Milliseconds(), selectorFragment.Selector.Offset.Milliseconds())
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: sql, QueryParams: childRendered.QueryParams}, nil
		}
		if selectorFragment != nil && selectorFragment.Kind == FragmentKindSubquery && selectorFragment.Subquery != nil && selectorFragment.Subquery.Child != nil {
			subqueryStep := selectorFragment.Subquery.Step
			if subqueryStep <= 0 {
				subqueryStep = time.Minute
			}
			expandedEndMS := params.EndMS - selectorFragment.Subquery.Offset.Milliseconds()
			expandedStartMS := params.StartMS - selectorFragment.Subquery.Offset.Milliseconds() - selectorFragment.Subquery.Range.Milliseconds()
			childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment.Subquery.Child, expandedStartMS, expandedEndMS)
			childRendered, err := RenderFragment(cfg, selectorFragment.Subquery.Child, RenderParams{
				Mode:                RenderModeRange,
				StartMS:             expandedStartMS,
				EndMS:               expandedEndMS,
				StepMS:              subqueryStep.Milliseconds(),
				RequiredStartMS:     childRequiredStartMS,
				RequiredEndMS:       childRequiredEndMS,
				ResolveSourcePromQL: params.ResolveSourcePromQL,
			})
			if err != nil {
				return RenderedQuery{}, err
			}
			sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fragment.RangeFunction.Func, fragment.RangeFunction.ParamNumber, fragment.RangeFunction.ParamNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Subquery.Range.Milliseconds(), selectorFragment.Subquery.Offset.Milliseconds())
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: sql, QueryParams: childRendered.QueryParams}, nil
		}
		return RenderedQuery{}, fmt.Errorf("native range-mode rendering for %s currently requires a direct range-vector selector child or supported subquery child", fragment.RangeFunction.Func)
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

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
	if fragment.Aggregation == nil {
		return RenderedQuery{}, fmt.Errorf("aggregation fragment is missing aggregation metadata")
	}
	source, err := renderAggregationSource(fragment.Aggregation.Source, params)
	if err != nil {
		return RenderedQuery{}, err
	}
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

func renderSourceFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	source, err := renderAggregationSource(fragment, params)
	if err != nil {
		return RenderedQuery{}, err
	}
	switch params.Mode {
	case RenderModeInstant:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return RenderedQuery{}, err
			}
			if sourceWrapperIsIdentity(fragment) {
				return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
			}
			wrappedSQL, err := wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: wrappedSQL, QueryParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return RenderedQuery{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		wrappedSQL, err := wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: wrappedSQL, QueryParams: queryParams}, nil
	case RenderModeRange:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return RenderedQuery{}, err
			}
			if sourceWrapperIsIdentity(fragment) {
				return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
			}
			wrappedSQL, err := wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: wrappedSQL, QueryParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return RenderedQuery{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		wrappedSQL, err := wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: wrappedSQL, QueryParams: queryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderSyntheticFragment(fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.Synthetic == nil {
		return RenderedQuery{}, fmt.Errorf("synthetic series fragment is missing synthetic metadata")
	}
	queryParams := map[string]string{}
	switch params.Mode {
	case RenderModeInstant:
		valueSQL, err := syntheticSeriesValueSQL(fragment.Synthetic.Func, "{evaluation_ms:Int64}")
		if err != nil {
			return RenderedQuery{}, err
		}
		queryParams["param_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
		sql := "SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, " + valueSQL + " AS value\nFORMAT JSONEachRow\n"
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	case RenderModeRange:
		if params.StepMS <= 0 {
			return RenderedQuery{}, fmt.Errorf("synthetic range render requires a positive step")
		}
		valueSQL, err := syntheticSeriesValueSQL(fragment.Synthetic.Func, "ts_ms")
		if err != nil {
			return RenderedQuery{}, err
		}
		queryParams["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
		queryParams["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
		queryParams["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
		sql := "SELECT CAST([], 'Array(Tuple(String, String))') AS tags, arrayMap(ts_ms -> (fromUnixTimestamp64Milli(ts_ms), " + valueSQL + "), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64})) AS time_series\nFORMAT JSONEachRow\n"
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func syntheticSeriesValueSQL(name, tsMSExpr string) (string, error) {
	utcTs := "toTimeZone(fromUnixTimestamp64Milli(" + tsMSExpr + "), 'UTC')"
	switch name {
	case "pi":
		return "toFloat64(3.141592653589793)", nil
	case "time":
		return "toFloat64(" + tsMSExpr + ") / 1000.0", nil
	case "minute":
		return "toFloat64(toMinute(" + utcTs + "))", nil
	case "hour":
		return "toFloat64(toHour(" + utcTs + "))", nil
	case "day_of_week":
		return "toFloat64(modulo(toDayOfWeek(" + utcTs + "), 7))", nil
	case "day_of_month":
		return "toFloat64(toDayOfMonth(" + utcTs + "))", nil
	case "day_of_year":
		return "toFloat64(toDayOfYear(" + utcTs + "))", nil
	case "days_in_month":
		return "toFloat64(toDaysInMonth(" + utcTs + "))", nil
	case "month":
		return "toFloat64(toMonth(" + utcTs + "))", nil
	case "year":
		return "toFloat64(toYear(" + utcTs + "))", nil
	default:
		return "", fmt.Errorf("synthetic series function %q is not implemented yet", name)
	}
}

func renderScalarConvertFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil || fragment.ScalarConvert == nil || fragment.ScalarConvert.Child == nil {
		return RenderedQuery{}, fmt.Errorf("scalar convert fragment is missing child metadata")
	}
	childSQL, childParams, err := renderFragmentSubquery(cfg, fragment.ScalarConvert.Child, params, "scalar_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	queryParams := map[string]string{}
	for key, value := range childParams {
		queryParams[key] = value
	}
	switch params.Mode {
	case RenderModeInstant:
		queryParams["param_evaluation_ms"] = strconv.FormatInt(params.EvaluationTimeMS, 10)
		sql := "SELECT CAST([], 'Array(Tuple(String, String))') AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, if(count() = 1, any(value), nan) AS value FROM (" + childSQL + ") AS scalar_child\nFORMAT JSONEachRow\n"
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	case RenderModeRange:
		queryParams["param_start_ms"] = strconv.FormatInt(params.StartMS, 10)
		queryParams["param_end_ms"] = strconv.FormatInt(params.EndMS, 10)
		queryParams["param_step_ms"] = strconv.FormatInt(params.StepMS, 10)
		sql := "SELECT CAST([], 'Array(Tuple(String, String))') AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" +
			"SELECT grid.timestamp AS timestamp, if(ifNull(scalar_values.sample_count, 0) = 1, scalar_values.any_value, nan) AS value FROM (" +
			"SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS timestamp" +
			") AS grid LEFT JOIN (" +
			"SELECT point.1 AS timestamp, count() AS sample_count, any(point.2) AS any_value FROM (" + childSQL + ") AS scalar_child ARRAY JOIN scalar_child.time_series AS point GROUP BY point.1" +
			") AS scalar_values ON scalar_values.timestamp = grid.timestamp ORDER BY timestamp" +
			")\nFORMAT JSONEachRow\n"
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

func mergeRenderedQueryParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL string) string {
	return "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" +
		"SELECT " + tagsSQL + " AS tags, grid.timestamp AS timestamp, toFloat64(1) AS value FROM (" + buildTimestampGridSQL() + ") AS grid LEFT JOIN (" + presenceSQL + ") AS present ON present.timestamp = grid.timestamp WHERE ifNull(present.sample_count, 0) = 0 ORDER BY timestamp" +
		") AS missing_steps GROUP BY tags HAVING length(time_series) > 0\nFORMAT JSONEachRow\n"
}

func buildTimestampGridSQL() string {
	return "SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS timestamp"
}

func outputMetricTagsSQL(metric map[string]string) string {
	if len(metric) == 0 {
		return "CAST([], 'Array(Tuple(String, String))')"
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, "tuple("+sqlStringLiteral(key)+", "+sqlStringLiteral(metric[key])+")")
	}
	return "CAST([" + strings.Join(items, ", ") + "], 'Array(Tuple(String, String))')"
}

func sqlStringLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}

func forceFragmentFullTags(fragment *NativeFragment) {
	if fragment == nil {
		return
	}
	if fragment.Selector != nil {
		fragment.Selector.RequireFullTags = true
	}
	if fragment.Aggregation != nil {
		forceFragmentFullTags(fragment.Aggregation.Source)
	}
	if fragment.RangeFunction != nil {
		forceFragmentFullTags(fragment.RangeFunction.Child)
	}
	if fragment.Subquery != nil {
		forceFragmentFullTags(fragment.Subquery.Child)
	}
	if fragment.ScalarConvert != nil {
		forceFragmentFullTags(fragment.ScalarConvert.Child)
	}
	if fragment.InfoJoin != nil {
		forceFragmentFullTags(fragment.InfoJoin.Child)
	}
	if fragment.BinaryJoin != nil {
		forceFragmentFullTags(fragment.BinaryJoin.LHS)
		forceFragmentFullTags(fragment.BinaryJoin.RHS)
	}
	if fragment.Absent != nil {
		forceFragmentFullTags(fragment.Absent.Child)
	}
}

func renderAggregationSource(fragment *NativeFragment, params RenderParams) (storage.AggregationSource, error) {
	if fragment == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation fragment is missing its source fragment")
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr:
		// Supported by the initial renderer skeleton.
	default:
		return storage.AggregationSource{}, fmt.Errorf("aggregation source fragment kind %q is not renderable yet", fragment.Kind)
	}
	if fragment.Selector != nil {
		return storage.AggregationSource{
			Selector: &storage.SelectorSource{
				Kind:              storage.SelectorKind(fragment.Selector.Kind),
				MetricName:        fragment.Selector.MetricName,
				Matchers:          selectorEffectiveMatchers(fragment.Selector),
				NeedTags:          selectorNeedsTags(fragment.Selector),
				RequireFullTags:   fragment.Selector.RequireFullTags,
				RequiredTagLabels: append([]string(nil), fragment.Selector.RequiredTagLabels...),
				LookbackMS:        fragment.Selector.Lookback.Milliseconds(),
				OffsetMS:          fragment.Selector.Offset.Milliseconds(),
			},
			ValueExpr: fragment.ValueExpr,
			TagsExpr:  fragment.TagsExpr,
		}, nil
	}
	if fragment.SourcePromQL == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation source fragment is missing its PromQL leaf")
	}
	if params.ResolveSourcePromQL == nil {
		return storage.AggregationSource{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
	}
	promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
	if err != nil {
		return storage.AggregationSource{}, err
	}
	return storage.AggregationSource{PromQLLeaf: promQL, ValueExpr: fragment.ValueExpr, TagsExpr: fragment.TagsExpr}, nil
}

func wrapInstantSourceQuery(sourceSQL, valueExpr, tagsExpr string) (string, error) {
	sourceTagsExpr, err := storage.CompileSourceTagsTemplate(tagsExpr, sqlb.Ident("tags"))
	if err != nil {
		return "", err
	}
	sourceValueExpr, err := storage.CompileSourceValueTemplate(valueExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
	if err != nil {
		return "", err
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sourceTagsExpr, Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sourceValueExpr, Alias: "value"}},
		From:    rawRenderedSubquerySource(sourceSQL),
	}
	return buildNativeWrapperSQL(query)
}

func wrapRangeSourceQuery(sourceSQL, valueExpr, tagsExpr string) (string, error) {
	sourceTagsExpr, err := storage.CompileSourceTagsTemplate(tagsExpr, sqlb.Ident("tags"))
	if err != nil {
		return "", err
	}
	sourceValueExpr, err := storage.CompileSourceValueTemplate(valueExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
	if err != nil {
		return "", err
	}
	sourceValueSQL, params, err := sqlb.BuildExpr(sourceValueExpr)
	if err != nil {
		return "", err
	}
	if len(params) != 0 {
		return "", fmt.Errorf("range wrapper source value template unexpectedly produced params: %#v", params)
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sourceTagsExpr, Alias: "tags"}, {Expr: sqlb.RawLit{V: "arrayMap(point -> (point.1, " + sourceValueSQL + "), time_series)"}, Alias: "time_series"}},
		From:    rawRenderedSubquerySource(sourceSQL),
	}
	return buildNativeWrapperSQL(query)
}

func renderFragmentSubquery(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams, prefix string) (string, map[string]string, error) {
	rendered, err := RenderFragment(cfg, fragment, params)
	if err != nil {
		return "", nil, err
	}
	return namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, prefix)
}

func namespaceRenderedQuery(sql string, queryParams map[string]string, prefix string) (string, map[string]string, error) {
	if prefix == "" {
		return sql, queryParams, nil
	}
	renamed := map[string]string{}
	for key, value := range queryParams {
		placeholderKey := strings.TrimPrefix(key, "param_")
		if placeholderKey == key {
			return "", nil, fmt.Errorf("native query parameter %q is missing param_ prefix", key)
		}
		newPlaceholderKey := prefix + "_" + placeholderKey
		newKey := "param_" + newPlaceholderKey
		sql = strings.ReplaceAll(sql, "{"+placeholderKey+":", "{"+newPlaceholderKey+":")
		renamed[newKey] = value
	}
	return sql, renamed, nil
}

func buildWindowedArraysSourceSQL(sourceSQL string, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range(" + strconv.FormatInt(startMS, 10) + ", " + strconv.FormatInt(endMS, 10) + " + " + strconv.FormatInt(stepMS, 10) + ", " + strconv.FormatInt(stepMS, 10) + ")))"}, Alias: "eval_ts"}}}
	stepWindows := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("source.tags"), Alias: "tags"},
			{Expr: sqlb.Ident("grid.eval_ts"), Alias: "eval_ts"},
			{Expr: sqlb.RawLit{V: "arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS, 10) + ") AND tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(" + strconv.FormatInt(offsetMS+rangeMS, 10) + "), source.time_series)"}, Alias: "window_series"},
			{Expr: sqlb.RawLit{V: "arrayMap(point -> tupleElement(point, 1), window_series)"}, Alias: "window_timestamps"},
			{Expr: sqlb.RawLit{V: "arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), window_series)"}, Alias: "window_values"},
		},
		From: sqlb.Join{Left: sqlb.SubSelect{S: grid, Alias: "grid"}, Right: rawRenderedSubquerySourceWithAlias(sourceSQL, "source"), Kind: "CROSS"},
	}
	return buildNativeWrapperSQL(stepWindows)
}

func buildRangeFunctionOverWindowedArraysSQL(sourceSQL, fn string, paramNumber *float64, paramNumbers []*float64, startMS, endMS, stepMS, rangeMS, offsetMS int64) (string, error) {
	windowedSourceSQL, err := buildWindowedArraysSourceSQL(sourceSQL, startMS, endMS, stepMS, rangeMS, offsetMS)
	if err != nil {
		return "", err
	}
	valueExpr := rangeFunctionValueExpr(fn, "window_series", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", rangeMS)
	perStep := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: rangeFunctionTagsExpr(fn)}, Alias: "final_tags"}, {Expr: sqlb.Ident("eval_ts"), Alias: "timestamp"}, {Expr: sqlb.RawLit{V: valueExpr}, Alias: "value"}},
		From:    rawRenderedSubquerySourceWithAlias(trimRenderedQuerySQL(windowedSourceSQL), "step_windows"),
		Where:   sqlb.RawLit{V: "length(window_series) > " + strconv.Itoa(minimumSeriesLengthForRangeFunction(fn))},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("final_tags"), Alias: "tags"}, {Expr: sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((timestamp, value)))"}, Alias: "time_series"}},
		From:    sqlb.SubSelect{S: perStep},
		GroupBy: []sqlb.Expr{sqlb.Ident("final_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("final_tags")}},
	}
	return buildNativeWrapperSQL(outer)
}

func buildInstantRangeFunctionSQL(sourceSQL, fn string, paramNumber *float64, paramNumbers []*float64, evaluationTimeMS int64, rangeMS int64) (string, error) {
	timestampExpr := "tupleElement(arrayElement(time_series, length(time_series)), 1)"
	if fn == "predict_linear" {
		timestampExpr = "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: rangeFunctionTagsExpr(fn)}, Alias: "tags"}, {Expr: sqlb.RawLit{V: timestampExpr}, Alias: "timestamp"}, {Expr: sqlb.RawLit{V: rangeFunctionValueExpr(fn, "time_series", paramNumber, paramNumbers, "arrayMap(point -> tupleElement(point, 1), time_series)", strconv.FormatInt(evaluationTimeMS, 10), rangeMS)}, Alias: "value"}},
		From:    rawRenderedSubquerySource(sourceSQL),
		Where:   sqlb.RawLit{V: "length(time_series) > " + strconv.Itoa(minimumSeriesLengthForRangeFunction(fn))},
	}
	return buildNativeWrapperSQL(query)
}

func rangeFunctionTagsExpr(fn string) string {
	if rangeFunctionPreservesMetricName(fn) {
		return "tags"
	}
	return "arrayFilter(tag -> tag.1 != '__name__', tags)"
}

// extrapolationFactorSQL builds a ClickHouse expression for Prometheus's
// rate/delta/increase extrapolation factor. Returns 1.0 when rangeMS <= 0
// (caller unable to supply window duration) or the sample count is insufficient.
// Mirrors extrapolatedRate in prometheus/promql/functions.go.
func extrapolationFactorSQL(timestampsExpr sqlb.Expr, seriesLength sqlb.Expr, evalTimeMSExpr string, rangeMS int64) string {
	if rangeMS <= 0 {
		return "1.0"
	}
	tsSQL := renderSQLExprNoParams(timestampsExpr)
	lenSQL := renderSQLExprNoParams(seriesLength)
	firstMS := "toFloat64(toUnixTimestamp64Milli(arrayElement(" + tsSQL + ", 1)))"
	lastMS := "toFloat64(toUnixTimestamp64Milli(arrayElement(" + tsSQL + ", " + lenSQL + ")))"
	sampledMS := "((" + lastMS + ") - (" + firstMS + "))"
	avgMS := "((" + sampledMS + ") / greatest(toFloat64(" + lenSQL + ") - 1, 1))"
	threshold := "((" + avgMS + ") * 1.1)"
	rangeStart := "((" + evalTimeMSExpr + ") - " + strconv.FormatInt(rangeMS, 10) + ".0)"
	rangeEnd := "(" + evalTimeMSExpr + ")"
	gapStart := "((" + firstMS + ") - " + rangeStart + ")"
	gapEnd := "(" + rangeEnd + " - (" + lastMS + "))"
	addStart := "if(" + gapStart + " < " + threshold + ", " + gapStart + ", (" + avgMS + ") / 2)"
	addEnd := "if(" + gapEnd + " < " + threshold + ", " + gapEnd + ", (" + avgMS + ") / 2)"
	extrapolateTo := "((" + sampledMS + ") + (" + addStart + ") + (" + addEnd + "))"
	return "if((" + sampledMS + ") <= 0, 1.0, (" + extrapolateTo + ") / (" + sampledMS + "))"
}

func rangeFunctionChildRangeMS(child *NativeFragment) int64 {
	if child == nil {
		return 0
	}
	if child.Selector != nil && child.Selector.Kind == SelectorKindRangeVector {
		return child.Selector.Lookback.Milliseconds()
	}
	if child.Subquery != nil {
		return child.Subquery.Range.Milliseconds()
	}
	return 0
}

func rangeFunctionValueExpr(fn, seriesExpr string, paramNumber *float64, paramNumbers []*float64, timestampsSourceExpr string, interceptTimeMSExpr string, rangeMS int64) string {
	series := sqlb.RawLit{V: seriesExpr}
	valuesExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "point -> ifNull(toFloat64(tupleElement(point, 2)), nan)"}, series}}
	timestampsExpr := sqlb.RawLit{V: timestampsSourceExpr}
	hasNaN := sqlb.Call{Name: "arrayExists", Args: []sqlb.Expr{sqlb.RawLit{V: "v -> isNaN(v)"}, valuesExpr}}
	finiteValues := sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "v -> NOT isNaN(v)"}, valuesExpr}}
	seriesLength := sqlb.Call{Name: "length", Args: []sqlb.Expr{series}}
	lenMinusOne := sqlb.RawLit{V: renderSQLExprNoParams(seriesLength) + " - 1"}

	arrayElementAtLength := func(expr sqlb.Expr) sqlb.Expr {
		return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{expr, seriesLength}}
	}
	arrayElementAtLengthMinusOne := func(expr sqlb.Expr) sqlb.Expr {
		return sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{expr, lenMinusOne}}
	}
	prevValues := sqlb.Call{Name: "arraySlice", Args: []sqlb.Expr{valuesExpr, sqlb.RawLit{V: "1"}, lenMinusOne}}
	curValues := sqlb.Call{Name: "arraySlice", Args: []sqlb.Expr{valuesExpr, sqlb.RawLit{V: "2"}, lenMinusOne}}
	counterDeltaExpr := sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(prev, cur) -> if(cur < prev, cur, cur - prev)"}, prevValues, curValues}}}}
	changesExpr := sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(prev, cur) -> if(cur != prev, 1, 0)"}, prevValues, curValues}}}}}}
	lenFiniteIsZero := sqlb.Binary{Op: "=", L: sqlb.Call{Name: "length", Args: []sqlb.Expr{finiteValues}}, R: sqlb.RawLit{V: "0"}}

	switch fn {
	case "last_over_time":
		return renderSQLExprNoParams(sqlb.TupleElem{X: sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{series, seriesLength}}, K: 2})
	case "sum_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{hasNaN, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{finiteValues}}}})
	case "avg_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "OR", L: hasNaN, R: lenFiniteIsZero}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayAvg", Args: []sqlb.Expr{finiteValues}}}})
	case "min_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "OR", L: hasNaN, R: lenFiniteIsZero}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayMin", Args: []sqlb.Expr{finiteValues}}}})
	case "max_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "OR", L: hasNaN, R: lenFiniteIsZero}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayMax", Args: []sqlb.Expr{finiteValues}}}})
	case "count_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{seriesLength}})
	case "stddev_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR " + renderSQLExprNoParams(seriesLength) + " = 0"}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'stddevPop'"}, valuesExpr}}}})
	case "stdvar_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR " + renderSQLExprNoParams(seriesLength) + " = 0"}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'varPop'"}, valuesExpr}}}})
	case "present_over_time":
		return renderSQLExprNoParams(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.RawLit{V: "1"}}})
	case "mad_over_time":
		medianExpr := sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'quantileExact(0.5)'"}, valuesExpr}}
		deviationsExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "x -> abs(x - " + renderSQLExprNoParams(medianExpr) + ")"}, valuesExpr}}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.Binary{Op: "=", L: seriesLength, R: sqlb.RawLit{V: "0"}}, sqlb.RawLit{V: "nan"}, sqlb.Call{Name: "arrayReduce", Args: []sqlb.Expr{sqlb.RawLit{V: "'quantileExact(0.5)'"}, deviationsExpr}}}})
	case "increase":
		factor := extrapolationFactorSQL(timestampsExpr, seriesLength, interceptTimeMSExpr, rangeMS)
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(counterDeltaExpr) + ") * (" + factor + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{hasNaN, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "changes":
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{hasNaN, sqlb.RawLit{V: "nan"}, changesExpr}})
	case "resets":
		finitePairsExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{sqlb.RawLit{V: "(prev, cur) -> if(cur < prev, 1, 0)"}, sqlb.Call{Name: "arrayPopBack", Args: []sqlb.Expr{finiteValues}}, sqlb.Call{Name: "arrayPopFront", Args: []sqlb.Expr{finiteValues}}}}
		return renderSQLExprNoParams(sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "arraySum", Args: []sqlb.Expr{finitePairsExpr}}}})
	case "rate":
		durationExpr := sqlb.RawLit{V: renderSQLExprNoParams(arrayElementAtLength(timestampsExpr)) + " - arrayElement(" + renderSQLExprNoParams(timestampsExpr) + ", 1)"}
		condition := sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR (" + renderSQLExprNoParams(durationExpr) + ") <= 0"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(counterDeltaExpr) + ") / (" + renderSQLExprNoParams(durationExpr) + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "irate":
		lastValue := arrayElementAtLength(valuesExpr)
		prevValue := arrayElementAtLengthMinusOne(valuesExpr)
		lastTS := arrayElementAtLength(timestampsExpr)
		prevTS := arrayElementAtLengthMinusOne(timestampsExpr)
		durationExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(lastTS) + ") - (" + renderSQLExprNoParams(prevTS) + ")"}
		deltaExpr := sqlb.RawLit{V: "if((" + renderSQLExprNoParams(lastValue) + ") < (" + renderSQLExprNoParams(prevValue) + "), " + renderSQLExprNoParams(lastValue) + ", (" + renderSQLExprNoParams(lastValue) + ") - (" + renderSQLExprNoParams(prevValue) + "))"}
		condition := sqlb.RawLit{V: renderSQLExprNoParams(hasNaN) + " OR (" + renderSQLExprNoParams(durationExpr) + ") <= 0"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(deltaExpr) + ") / (" + renderSQLExprNoParams(durationExpr) + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "delta":
		firstValue := sqlb.Call{Name: "arrayElement", Args: []sqlb.Expr{valuesExpr, sqlb.RawLit{V: "1"}}}
		lastValue := arrayElementAtLength(valuesExpr)
		condition := sqlb.RawLit{V: "isNaN(" + renderSQLExprNoParams(firstValue) + ") OR isNaN(" + renderSQLExprNoParams(lastValue) + ")"}
		factor := extrapolationFactorSQL(timestampsExpr, seriesLength, interceptTimeMSExpr, rangeMS)
		resultExpr := sqlb.RawLit{V: "((" + renderSQLExprNoParams(lastValue) + ") - (" + renderSQLExprNoParams(firstValue) + ")) * (" + factor + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "idelta":
		lastValue := arrayElementAtLength(valuesExpr)
		prevValue := arrayElementAtLengthMinusOne(valuesExpr)
		condition := sqlb.RawLit{V: "isNaN(" + renderSQLExprNoParams(prevValue) + ") OR isNaN(" + renderSQLExprNoParams(lastValue) + ")"}
		resultExpr := sqlb.RawLit{V: "(" + renderSQLExprNoParams(lastValue) + ") - (" + renderSQLExprNoParams(prevValue) + ")"}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, resultExpr}})
	case "deriv":
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		timestampsSQL := renderSQLExprNoParams(timestampsExpr)
		xsSQL := "arrayMap(ts -> (toFloat64(toUnixTimestamp64Milli(ts)) - (" + interceptTimeMSExpr + ")) / 1000.0, " + timestampsSQL + ")"
		lrExpr := "arrayReduce('simpleLinearRegression', " + xsSQL + ", " + valuesSQL + ")"
		condition := sqlb.RawLit{V: "length(" + valuesSQL + ") < 2 OR " + renderSQLExprNoParams(hasNaN)}
		return renderSQLExprNoParams(sqlb.Call{Name: "if", Args: []sqlb.Expr{condition, sqlb.RawLit{V: "nan"}, sqlb.RawLit{V: "tupleElement(" + lrExpr + ", 1)"}}})
	case "predict_linear":
		if paramNumber == nil {
			return "nan"
		}
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		timestampsSQL := renderSQLExprNoParams(timestampsExpr)
		firstValueExpr := "arrayElement(" + valuesSQL + ", 1)"
		constExpr := "arrayAll(v -> v = " + firstValueExpr + ", " + valuesSQL + ")"
		lrExpr := "arrayReduce('simpleLinearRegression', arrayMap(ts -> (toFloat64(toUnixTimestamp64Milli(ts)) - (" + interceptTimeMSExpr + ")) / 1000.0, " + timestampsSQL + "), " + valuesSQL + ")"
		predictionExpr := "tupleElement(" + lrExpr + ", 1) * " + storage.NativeFloatLiteral(*paramNumber) + " + tupleElement(" + lrExpr + ", 2)"
		return "multiIf(length(" + valuesSQL + ") < 2, nan, (" + constExpr + ") AND NOT isInfinite(" + firstValueExpr + "), " + firstValueExpr + ", (" + constExpr + ") AND isInfinite(" + firstValueExpr + "), nan, " + predictionExpr + ")"
	case "double_exponential_smoothing", "holt_winters":
		if len(paramNumbers) != 2 || paramNumbers[0] == nil || paramNumbers[1] == nil {
			return "nan"
		}
		valuesSQL := renderSQLExprNoParams(valuesExpr)
		sf := storage.NativeFloatLiteral(*paramNumbers[0])
		tf := storage.NativeFloatLiteral(*paramNumbers[1])
		thirdOnwardExpr := "arraySlice(" + valuesSQL + ", 3)"
		secondValueExpr := "arrayElement(" + valuesSQL + ", 2)"
		firstValueExpr := "arrayElement(" + valuesSQL + ", 1)"
		newS1Expr := sf + " * v + (1 - " + sf + ") * (tupleElement(acc, 1) + tupleElement(acc, 2))"
		newBExpr := tf + " * ((" + newS1Expr + ") - tupleElement(acc, 1)) + (1 - " + tf + ") * tupleElement(acc, 2)"
		foldExpr := "arrayFold((acc, v) -> ((" + newS1Expr + "), (" + newBExpr + ")), " + thirdOnwardExpr + ", (" + secondValueExpr + ", (" + secondValueExpr + ") - (" + firstValueExpr + ")))"
		return "multiIf(length(" + valuesSQL + ") < 2, nan, length(" + valuesSQL + ") = 2, " + secondValueExpr + ", tupleElement(" + foldExpr + ", 1))"
	default:
		return "nan"
	}
}

func minimumSeriesLengthForRangeFunction(fn string) int {
	switch fn {
	case "increase", "rate", "irate", "delta", "idelta", "deriv", "predict_linear", "double_exponential_smoothing", "holt_winters":
		return 1
	default:
		return 0
	}
}

func rangeRequiredBoundsForChild(fragment *NativeFragment, startMS, endMS int64) (int64, int64) {
	selector := baseSelectorSource(fragment)
	if selector == nil {
		return startMS, endMS
	}
	lookbackMS := selector.Lookback.Milliseconds()
	offsetMS := selector.Offset.Milliseconds()
	return startMS - offsetMS - lookbackMS, endMS - offsetMS
}

func trimRenderedQuerySQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if idx := strings.LastIndex(sql, "SETTINGS allow_experimental_time_series_table = 1"); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	if idx := strings.LastIndex(sql, "FORMAT JSONEachRow"); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	return sql
}

func sourceWrapperIsIdentity(fragment *NativeFragment) bool {
	return fragment != nil && fragment.ValueExpr == "{value}" && fragment.TagsExpr == "{tags}" && !fragment.DropsMetric
}

func buildNativeWrapperSQL(query *sqlb.Select) (string, error) {
	sql, params, err := query.Build()
	if err != nil {
		return "", err
	}
	if len(params) != 0 {
		return "", fmt.Errorf("native wrapper SQL unexpectedly produced params: %#v", params)
	}
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", nil
}

func renderSQLExprNoParams(expr sqlb.Expr) string {
	sql, params, err := sqlb.BuildExpr(expr)
	if err != nil {
		panic(err)
	}
	if len(params) != 0 {
		panic(fmt.Errorf("sqlb expression unexpectedly produced params: %#v", params))
	}
	return sql
}

func rawRenderedSubquerySource(sql string) sqlb.RawSource {
	return rawRenderedSubquerySourceWithAlias(sql, "")
}

func rawRenderedSubquerySourceWithAlias(sql, alias string) sqlb.RawSource {
	return sqlb.RawSource{SQL: "(\n" + localIndentSQL(trimRenderedQuerySQL(sql), 4) + "\n)", Alias: alias}
}

func selectorEffectiveMatchers(selector *SelectorSource) []*labels.Matcher {
	if selector == nil {
		return nil
	}
	if len(selector.PushedMatchers) > 0 {
		return cloneMatchers(selector.PushedMatchers)
	}
	matchers := cloneMatchers(selector.Matchers)
	matchers = append(matchers, cloneMatchers(selector.InferredMatchers)...)
	return matchers
}

func selectorNeedsTags(selector *SelectorSource) bool {
	if selector == nil {
		return true
	}
	return selector.RequireFullTags || len(selector.RequiredTagLabels) > 0
}

func localIndentSQL(sql string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
