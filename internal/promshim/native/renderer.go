package native

import (
	"fmt"
	"strings"
	"time"

	"ch-observability/internal/promshim/storage"
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
		return RenderedQuery{SQL: buildInstantRangeFunctionSQL(childRendered.SQL, fragment.RangeFunction.Func), QueryParams: childRendered.QueryParams}, nil
	case RenderModeRange:
		selectorFragment := fragment.RangeFunction.Child
		if selectorFragment != nil && selectorFragment.Kind == FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == SelectorKindRangeVector {
			source, err := renderAggregationSource(selectorFragment, params)
			if err != nil {
				return RenderedQuery{}, err
			}
			if source.Selector == nil {
				return RenderedQuery{}, fmt.Errorf("native range-mode rendering for %s requires a selector-backed source", fragment.RangeFunction.Func)
			}
			childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
			sql, queryParams, err := storage.BuildRangeWindowSelectorQuerySQL(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, rangeFunctionValueExpr(fragment.RangeFunction.Func, "window_series"), minimumSeriesLengthForRangeFunction(fragment.RangeFunction.Func))
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
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
			return RenderedQuery{SQL: buildRangeFunctionOverSubqueryRangeSQL(childRendered.SQL, fragment.RangeFunction.Func, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Subquery.Range.Milliseconds(), selectorFragment.Subquery.Offset.Milliseconds()), QueryParams: childRendered.QueryParams}, nil
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
		sql, queryParams, err := storage.BuildInstantAggregationQuerySQLWithBounds(cfg, source, params.EvaluationTimeMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	case RenderModeRange:
		sql, queryParams, err := storage.BuildRangeAggregationQuerySQLWithBounds(cfg, source, params.StartMS, params.EndMS, params.StepMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without)
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
			return RenderedQuery{SQL: wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return RenderedQuery{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		return RenderedQuery{SQL: wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
	case RenderModeRange:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return RenderedQuery{}, err
			}
			if sourceWrapperIsIdentity(fragment) {
				return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
			}
			return RenderedQuery{SQL: wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return RenderedQuery{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		return RenderedQuery{SQL: wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
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

func wrapInstantSourceQuery(sourceSQL, valueExpr, tagsExpr string) string {
	sourceTagsExpr := strings.ReplaceAll(tagsExpr, "{tags}", "tags")
	sourceValueExpr := strings.ReplaceAll(valueExpr, "{value}", "value")
	return fmt.Sprintf(`
SELECT
    %s AS tags,
    timestamp,
    %s AS value
FROM (
%s
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, sourceTagsExpr, sourceValueExpr, localIndentSQL(trimRenderedQuerySQL(sourceSQL), 4))
}

func wrapRangeSourceQuery(sourceSQL, valueExpr, tagsExpr string) string {
	sourceTagsExpr := strings.ReplaceAll(tagsExpr, "{tags}", "tags")
	sourceValueExpr := strings.ReplaceAll(valueExpr, "{value}", "point.2")
	return fmt.Sprintf(`
SELECT
    %s AS tags,
    arrayMap(point -> (point.1, %s), time_series) AS time_series
FROM (
%s
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, sourceTagsExpr, sourceValueExpr, localIndentSQL(trimRenderedQuerySQL(sourceSQL), 4))
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

func buildRangeFunctionOverSubqueryRangeSQL(sourceSQL, fn string, startMS, endMS, stepMS, subqueryRangeMS, subqueryOffsetMS int64) string {
	valueExpr := rangeFunctionValueExpr(fn, "window_series")
	return fmt.Sprintf(`
SELECT
    final_tags AS tags,
    arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
    SELECT
        arrayFilter(tag -> tag.1 != '__name__', tags) AS final_tags,
        eval_ts AS timestamp,
        %s AS value
    FROM (
        SELECT
            source.tags AS tags,
            grid.eval_ts AS eval_ts,
            arrayFilter(point -> tupleElement(point, 1) <= grid.eval_ts - toIntervalMillisecond(%d) AND tupleElement(point, 1) >= grid.eval_ts - toIntervalMillisecond(%d), source.time_series) AS window_series
        FROM (
            SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range(%d, %d + %d, %d))) AS eval_ts
        ) AS grid
        CROSS JOIN (
%s
        ) AS source
    ) AS step_windows
    WHERE length(window_series) > %d
)
GROUP BY final_tags
ORDER BY final_tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, valueExpr, subqueryOffsetMS, subqueryOffsetMS+subqueryRangeMS, startMS, endMS, stepMS, stepMS, localIndentSQL(trimRenderedQuerySQL(sourceSQL), 8), minimumSeriesLengthForRangeFunction(fn))
}

func buildInstantRangeFunctionSQL(sourceSQL, fn string) string {
	valueExpr := rangeFunctionValueExpr(fn, "time_series")
	return fmt.Sprintf(`
SELECT
    arrayFilter(tag -> tag.1 != '__name__', tags) AS tags,
    tupleElement(arrayElement(time_series, length(time_series)), 1) AS timestamp,
    %s AS value
FROM (
%s
)
WHERE length(time_series) > %d
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, valueExpr, localIndentSQL(trimRenderedQuerySQL(sourceSQL), 4), minimumSeriesLengthForRangeFunction(fn))
}

func rangeFunctionValueExpr(fn, seriesExpr string) string {
	valuesExpr := fmt.Sprintf("arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), %s)", seriesExpr)
	timestampsExpr := fmt.Sprintf("arrayMap(point -> tupleElement(point, 1), %s)", seriesExpr)
	hasNaN := fmt.Sprintf("arrayExists(v -> isNaN(v), %s)", valuesExpr)
	finiteValues := fmt.Sprintf("arrayFilter(v -> NOT isNaN(v), %s)", valuesExpr)
	seriesLength := fmt.Sprintf("length(%s)", seriesExpr)
	switch fn {
	case "last_over_time":
		return fmt.Sprintf("tupleElement(arrayElement(%s, length(%s)), 2)", seriesExpr, seriesExpr)
	case "sum_over_time":
		return fmt.Sprintf("if(%s, nan, arraySum(%s))", hasNaN, finiteValues)
	case "avg_over_time":
		return fmt.Sprintf("if(%s OR length(%s) = 0, nan, arrayAvg(%s))", hasNaN, finiteValues, finiteValues)
	case "min_over_time":
		return fmt.Sprintf("if(%s OR length(%s) = 0, nan, arrayMin(%s))", hasNaN, finiteValues, finiteValues)
	case "max_over_time":
		return fmt.Sprintf("if(%s OR length(%s) = 0, nan, arrayMax(%s))", hasNaN, finiteValues, finiteValues)
	case "count_over_time":
		return fmt.Sprintf("toFloat64(length(%s))", seriesExpr)
	case "increase":
		prevValues := fmt.Sprintf("arraySlice(%s, 1, %s - 1)", valuesExpr, seriesLength)
		curValues := fmt.Sprintf("arraySlice(%s, 2, %s - 1)", valuesExpr, seriesLength)
		deltaExpr := fmt.Sprintf("arraySum(arrayMap((prev, cur) -> if(cur < prev, cur, cur - prev), %s, %s))", prevValues, curValues)
		return fmt.Sprintf("if(%s, nan, %s)", hasNaN, deltaExpr)
	case "changes":
		prevValues := fmt.Sprintf("arraySlice(%s, 1, %s - 1)", valuesExpr, seriesLength)
		curValues := fmt.Sprintf("arraySlice(%s, 2, %s - 1)", valuesExpr, seriesLength)
		changesExpr := fmt.Sprintf("toFloat64(arraySum(arrayMap((prev, cur) -> if(cur != prev, 1, 0), %s, %s)))", prevValues, curValues)
		return fmt.Sprintf("if(%s, nan, %s)", hasNaN, changesExpr)
	case "rate":
		prevValues := fmt.Sprintf("arraySlice(%s, 1, %s - 1)", valuesExpr, seriesLength)
		curValues := fmt.Sprintf("arraySlice(%s, 2, %s - 1)", valuesExpr, seriesLength)
		deltaExpr := fmt.Sprintf("arraySum(arrayMap((prev, cur) -> if(cur < prev, cur, cur - prev), %s, %s))", prevValues, curValues)
		durationExpr := fmt.Sprintf("arrayElement(%s, %s) - arrayElement(%s, 1)", timestampsExpr, seriesLength, timestampsExpr)
		return fmt.Sprintf("if(%s OR (%s) <= 0, nan, (%s) / (%s))", hasNaN, durationExpr, deltaExpr, durationExpr)
	case "irate":
		lastValue := fmt.Sprintf("arrayElement(%s, %s)", valuesExpr, seriesLength)
		prevValue := fmt.Sprintf("arrayElement(%s, %s - 1)", valuesExpr, seriesLength)
		lastTS := fmt.Sprintf("arrayElement(%s, %s)", timestampsExpr, seriesLength)
		prevTS := fmt.Sprintf("arrayElement(%s, %s - 1)", timestampsExpr, seriesLength)
		durationExpr := fmt.Sprintf("(%s) - (%s)", lastTS, prevTS)
		deltaExpr := fmt.Sprintf("if((%s) < (%s), %s, (%s) - (%s))", lastValue, prevValue, lastValue, lastValue, prevValue)
		return fmt.Sprintf("if(%s OR (%s) <= 0, nan, (%s) / (%s))", hasNaN, durationExpr, deltaExpr, durationExpr)
	case "delta":
		firstValue := fmt.Sprintf("arrayElement(%s, 1)", valuesExpr)
		lastValue := fmt.Sprintf("arrayElement(%s, %s)", valuesExpr, seriesLength)
		return fmt.Sprintf("if(isNaN(%s) OR isNaN(%s), nan, (%s) - (%s))", firstValue, lastValue, lastValue, firstValue)
	case "idelta":
		lastValue := fmt.Sprintf("arrayElement(%s, %s)", valuesExpr, seriesLength)
		prevValue := fmt.Sprintf("arrayElement(%s, %s - 1)", valuesExpr, seriesLength)
		return fmt.Sprintf("if(isNaN(%s) OR isNaN(%s), nan, (%s) - (%s))", prevValue, lastValue, lastValue, prevValue)
	case "deriv":
		nExpr := fmt.Sprintf("toFloat64(%s)", seriesLength)
		sumXExpr := fmt.Sprintf("arraySum(%s)", timestampsExpr)
		sumYExpr := fmt.Sprintf("arraySum(%s)", valuesExpr)
		sumXYExpr := fmt.Sprintf("arraySum(arrayMap((x, y) -> x * y, %s, %s))", timestampsExpr, valuesExpr)
		sumX2Expr := fmt.Sprintf("arraySum(arrayMap(x -> x * x, %s))", timestampsExpr)
		denomExpr := fmt.Sprintf("(%s) * (%s) - (%s) * (%s)", nExpr, sumX2Expr, sumXExpr, sumXExpr)
		numerExpr := fmt.Sprintf("(%s) * (%s) - (%s) * (%s)", nExpr, sumXYExpr, sumXExpr, sumYExpr)
		return fmt.Sprintf("if(%s OR (%s) = 0, nan, (%s) / (%s))", hasNaN, denomExpr, numerExpr, denomExpr)
	default:
		return "nan"
	}
}

func minimumSeriesLengthForRangeFunction(fn string) int {
	switch fn {
	case "increase", "rate", "irate", "delta", "idelta", "deriv":
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
	return startMS - lookbackMS, endMS
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
