package native

import (
	"fmt"
	"strconv"

	"ch-observability/internal/promshim/storage"
)

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
	if fragment.HistogramProjection != nil {
		forceFragmentFullTags(fragment.HistogramProjection.Child)
	}
	if fragment.ValueTransform != nil {
		forceFragmentFullTags(fragment.ValueTransform.Child)
	}
}
