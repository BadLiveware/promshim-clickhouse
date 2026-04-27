package storage

import (
	"fmt"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type InfoJoinConfig struct {
	IdentifyingLabels []string
	CopyLabelNames    []string
	DropUnmatched     bool
	InfoNameMatchers  []*labels.Matcher
}

func BuildInstantInfoJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg InfoJoinConfig) (string, map[string]string, error) {
	return buildInfoJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, false)
}

func BuildRangeInfoJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg InfoJoinConfig) (string, map[string]string, error) {
	return buildInfoJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, true)
}

func buildInfoJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg InfoJoinConfig, rangeMode bool) (string, map[string]string, error) {
	if err := validateInfoJoinConfig(cfg); err != nil {
		return "", nil, err
	}
	matching := &parser.VectorMatching{On: true, MatchingLabels: append([]string(nil), cfg.IdentifyingLabels...)}
	lhsPrepared, err := buildPreparedJoinSideSelect("lhs", buildJoinSource(lhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), matching), rangeMode, false)
	if err != nil {
		return "", nil, err
	}
	rhsPrepared, err := buildPreparedJoinSideSelect("rhs", buildJoinSource(rhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), matching), rangeMode, false)
	if err != nil {
		return "", nil, err
	}
	lhsPreparedSQL, _, err := lhsPrepared.Build()
	if err != nil {
		return "", nil, err
	}
	rhsPreparedSQL, _, err := rhsPrepared.Build()
	if err != nil {
		return "", nil, err
	}

	joinOn := "lhs.join_group = rhs.join_group"
	if rangeMode {
		joinOn += " AND lhs.timestamp = rhs.timestamp"
	}
	rhsValidatedSQL := buildInfoValidatedRHSQuery(rhsPreparedSQL, rangeMode)
	lhsInfoMetricSQL, lhsInfoMetricParams := buildInfoNameMatcherCondition("lhs.original_group", "info_lhs_name", cfg.InfoNameMatchers)
	resultTagsSQL := buildInfoJoinMergedResultTagsSQL(cfg, "lhs_original_group", "rhs_groups", "lhs_is_info_metric")
	groupBy := []string{"lhs.original_group", "lhs.join_group", "lhs.timestamp", "lhs.value", "(" + lhsInfoMetricSQL + ")"}
	joinedRowsSQL := "SELECT lhs.original_group AS lhs_original_group, lhs.timestamp AS timestamp, lhs.value AS value, (" + lhsInfoMetricSQL + ") AS lhs_is_info_metric, groupArrayIf(rhs.original_group, length(rhs.original_group) > 0) AS rhs_groups FROM (" + lhsPreparedSQL + ") AS lhs LEFT JOIN (" + rhsValidatedSQL + ") AS rhs ON " + joinOn + " GROUP BY " + strings.Join(groupBy, ", ")
	if cfg.DropUnmatched {
		joinedRowsSQL += " HAVING lhs_is_info_metric OR length(rhs_groups) > 0"
	}
	finalRowsSQL := "SELECT " + resultTagsSQL + " AS result_tags, timestamp AS timestamp, value AS value FROM (" + joinedRowsSQL + ") AS joined_rows"

	params := mergeParamMaps(lhsParams, rhsParams)
	mergeParams(params, lhsInfoMetricParams)
	if rangeMode {
		groupedSQL := "SELECT result_tags, timestamp, any(value) AS value FROM (" + finalRowsSQL + ") AS joined_result_rows GROUP BY result_tags, timestamp"
		outerSQL := "SELECT result_tags AS tags, " + renderStorageExprNoParams(emit.SortedTimeSeriesGroupArray()) + " AS time_series FROM (" + groupedSQL + ") AS grouped_rows GROUP BY result_tags ORDER BY result_tags"
		return outerSQL + schema.QuerySuffix, params, nil
	}
	outerSQL := "SELECT result_tags AS tags, any(timestamp) AS timestamp, any(value) AS value FROM (" + finalRowsSQL + ") AS joined_result_rows GROUP BY result_tags ORDER BY result_tags"
	return outerSQL + schema.QuerySuffix, params, nil
}

func buildInfoValidatedRHSQuery(rhsPreparedSQL string, rangeMode bool) string {
	timestampExpr := "any(timestamp) AS timestamp"
	groupBy := []string{"join_group", "metric_name"}
	if rangeMode {
		timestampExpr = "timestamp AS timestamp"
		groupBy = append(groupBy, "timestamp")
	}
	return "SELECT any(original_group) AS original_group, join_group AS join_group, " + timestampExpr + ", any(value) AS value, metric_name FROM (" +
		"SELECT original_group, join_group, timestamp, value, arrayElement(arrayMap(tag -> tag.2, arrayFilter(tag -> tag.1 = '__name__', original_group)), 1) AS metric_name FROM (" + rhsPreparedSQL + ") AS rhs_input" +
		") AS rhs_checked GROUP BY " + strings.Join(groupBy, ", ") + " HAVING throwIf(count() > 1, 'found duplicate series for info metric on identifying labels') = 0"
}

func buildInfoJoinMergedResultTagsSQL(cfg InfoJoinConfig, lhsGroupExpr, rhsGroupsExpr, lhsIsInfoMetricExpr string) string {
	lhsNames := "arrayMap(tag -> tag.1, " + lhsGroupExpr + ")"
	ignored := append([]string{"__name__"}, cfg.IdentifyingLabels...)
	condition := "NOT has(" + sqlStringArrayLiteral(ignored) + ", tag.1) AND NOT has(" + lhsNames + ", tag.1)"
	if len(cfg.CopyLabelNames) > 0 {
		condition += " AND has(" + sqlStringArrayLiteral(cfg.CopyLabelNames) + ", tag.1)"
	}
	rhsMerged := "arrayDistinct(arrayFlatten(arrayMap(tags -> arrayFilter(tag -> " + condition + ", tags), " + rhsGroupsExpr + ")))"
	merged := "arraySort(tag -> tag.1, arrayConcat(" + lhsGroupExpr + ", " + rhsMerged + "))"
	return "if((" + lhsIsInfoMetricExpr + ") OR length(" + rhsGroupsExpr + ") = 0, " + lhsGroupExpr + ", " + merged + ")"
}

func buildInfoNameMatcherCondition(tagsExpr, prefix string, matchers []*labels.Matcher) (string, map[string]string) {
	metricColumn := "arrayElement(arrayMap(tag -> tag.2, arrayFilter(tag -> tag.1 = '__name__', " + tagsExpr + ")), 1)"
	clauses := make([]string, 0)
	params := map[string]string{}
	matcherIndex := 0
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name != "__name__" {
			continue
		}
		clause, extraParams := compileMatcherClause(QueryConfig{}, prefix, matcherIndex, metricColumn, tagsExpr, matcher)
		matcherIndex++
		clauses = append(clauses, clause)
		mergeParams(params, extraParams)
	}
	if len(clauses) == 0 {
		return "0", params
	}
	return strings.Join(clauses, " AND "), params
}

func validateInfoJoinConfig(cfg InfoJoinConfig) error {
	if len(cfg.IdentifyingLabels) == 0 {
		return fmt.Errorf("info join requires at least one identifying label")
	}
	return nil
}
