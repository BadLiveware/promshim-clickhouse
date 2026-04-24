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
)

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

// assembleInfoJoinSQL builds the info-join outer query from an already
// rendered, namespaced child source ("info_lhs") and info-series metadata.
// Used by the direct path (lowerInfoJoin).
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

// renderValueTransformFromSource wraps a pre-rendered child source in the
// value-transform outer SELECT (instant or range mode). Used by the direct
// path (lowerRound, lowerUnary, lowerClamp, scalar-BinaryPlan conversions).
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

// renderAbsentFromNamespacedChild builds the absent() body for both instant
// and range modes given a child already namespaced under the "absent_child"
// alias. Used by the direct-render AbsentPlan lowerer.
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

func buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL string) string {
	return "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" +
		"SELECT " + tagsSQL + " AS tags, grid.timestamp AS timestamp, toFloat64(1) AS value FROM (" + buildTimestampGridSQL() + ") AS grid LEFT JOIN (" + presenceSQL + ") AS present ON present.timestamp = grid.timestamp WHERE ifNull(present.sample_count, 0) = 0 ORDER BY timestamp" +
		") AS missing_steps GROUP BY tags HAVING length(time_series) > 0" + schema.FormatSuffix
}

func buildTimestampGridSQL() string {
	return "SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({start_ms:Int64}, {end_ms:Int64} + {step_ms:Int64}, {step_ms:Int64}))) AS timestamp"
}
