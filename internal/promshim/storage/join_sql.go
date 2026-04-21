package storage

import (
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type BinaryJoinConfig struct {
	Op             parser.ItemType
	ReturnBool     bool
	VectorMatching *parser.VectorMatching
	JoinShape      string
}

func BuildInstantBinaryVectorJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg BinaryJoinConfig) (string, map[string]string, error) {
	return buildBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, false)
}

func BuildRangeBinaryVectorJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg BinaryJoinConfig) (string, map[string]string, error) {
	return buildBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, true)
}

func buildBinaryVectorJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg BinaryJoinConfig, rangeMode bool) (string, map[string]string, error) {
	valueExpr, valueFilter, err := buildBinaryValueSQL(cfg.Op, cfg.ReturnBool, "lhs.value", "rhs.value")
	if err != nil {
		return "", nil, err
	}
	joinKeyExpr := buildJoinGroupExpr("original_group", cfg.VectorMatching)
	resultTagsExpr := buildBinaryResultTagsSQL(cfg)
	dupKeyExpr := "join_group"
	resultGroupExpr := "result_tags"
	joinCondition := "lhs.join_group = rhs.join_group"
	lhsSource := lhsSQL
	rhsSource := rhsSQL
	resultTimestampExpr := "lhs.timestamp AS timestamp,"
	if rangeMode {
		dupKeyExpr = "join_group, timestamp"
		resultGroupExpr = "result_tags, timestamp"
		joinCondition += " AND lhs.timestamp = rhs.timestamp"
		lhsSource = flattenRangeSourceSQL(lhsSQL)
		rhsSource = flattenRangeSourceSQL(rhsSQL)
	} else {
		lhsSource = strings.TrimSpace(lhsSQL)
		rhsSource = strings.TrimSpace(rhsSQL)
	}

	lhsCheck := cfg.JoinShape == "one_to_one" || cfg.JoinShape == "one_to_many"
	rhsCheck := cfg.JoinShape == "one_to_one" || cfg.JoinShape == "many_to_one"
	lhsPrepared := buildPreparedJoinSideSQL("lhs", lhsSource, joinKeyExpr, dupKeyExpr, lhsCheck)
	rhsPrepared := buildPreparedJoinSideSQL("rhs", rhsSource, joinKeyExpr, dupKeyExpr, rhsCheck)
	whereClause := ""
	if valueFilter != "" {
		whereClause = "WHERE " + valueFilter
	}

	joinedRows := fmt.Sprintf(`
SELECT
    %s AS result_tags,
    %s
    %s AS value
FROM (
%s
) AS lhs
INNER JOIN (
%s
) AS rhs
    ON %s
%s
`, resultTagsExpr, resultTimestampExpr, valueExpr, indentSQL(lhsPrepared, 4), indentSQL(rhsPrepared, 4), joinCondition, whereClause)

	if rangeMode {
		params := mergeParamMaps(lhsParams, rhsParams)
		return fmt.Sprintf(`
SELECT
    result_tags AS tags,
    arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series
FROM (
    SELECT
        result_tags,
        timestamp,
        any(value) AS value
    FROM (
%s
    )
    GROUP BY %s
    HAVING throwIf(count() > 1, 'multiple matches for labels: grouping labels must ensure unique matches') = 0
)
GROUP BY result_tags
ORDER BY result_tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, indentSQL(joinedRows, 4), resultGroupExpr), params, nil
	}

	params := mergeParamMaps(lhsParams, rhsParams)
	return fmt.Sprintf(`
SELECT
    result_tags AS tags,
    any(timestamp) AS timestamp,
    any(value) AS value
FROM (
%s
)
GROUP BY %s
HAVING throwIf(count() > 1, 'multiple matches for labels: grouping labels must ensure unique matches') = 0
ORDER BY result_tags
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, indentSQL(joinedRows, 4), resultGroupExpr), params, nil
}

func buildPreparedJoinSideSQL(side, sourceSQL, joinKeyExpr, dupKeyExpr string, checkDuplicates bool) string {
	joinGroupExpr := strings.ReplaceAll(joinKeyExpr, "original_group", "tags")
	withTimestamp := strings.Contains(dupKeyExpr, "timestamp")
	if !checkDuplicates {
		return fmt.Sprintf(`
SELECT
    tags AS original_group,
    %s AS join_group,
    timestamp,
    value
FROM (
%s
)
`, joinGroupExpr, indentSQL(sourceSQL, 4))
	}
	groupBy := joinGroupExpr
	timestampExpr := "any(timestamp) AS timestamp"
	if withTimestamp {
		groupBy += ", timestamp"
		timestampExpr = "timestamp"
	}
	return fmt.Sprintf(`
SELECT
    any(tags) AS original_group,
    %s AS join_group,
    %s,
    any(value) AS value
FROM (
%s
)
GROUP BY %s
HAVING throwIf(count() > 1, 'found duplicate series for the match group on the %s hand-side of the operation') = 0
`, joinGroupExpr, timestampExpr, indentSQL(sourceSQL, 4), groupBy, side)
}

func flattenRangeSourceSQL(sourceSQL string) string {
	return fmt.Sprintf(`
SELECT
    tags,
    point.1 AS timestamp,
    point.2 AS value
FROM (
%s
)
ARRAY JOIN time_series AS point
`, indentSQL(strings.TrimSpace(sourceSQL), 4))
}

func buildJoinGroupExpr(tagsExpr string, vectorMatching *parser.VectorMatching) string {
	matching := normalizeStorageVectorMatching(vectorMatching)
	if matching.On {
		if len(matching.MatchingLabels) == 0 {
			return "CAST([], 'Array(Tuple(String, String))')"
		}
		return fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> has(%s, tag.1), %s))", sqlStringArrayLiteral(matching.MatchingLabels), tagsExpr)
	}
	ignored := append([]string{labels.MetricName}, matching.MatchingLabels...)
	return fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> NOT has(%s, tag.1), %s))", sqlStringArrayLiteral(ignored), tagsExpr)
}

func buildBinaryResultTagsSQL(cfg BinaryJoinConfig) string {
	matching := normalizeStorageVectorMatching(cfg.VectorMatching)
	base := "lhs.original_group"
	oneSide := "rhs.original_group"
	if cfg.JoinShape == "one_to_many" {
		base = "rhs.original_group"
		oneSide = "lhs.original_group"
	}
	result := base
	if cfg.JoinShape == "one_to_one" {
		if matching.On {
			if len(matching.MatchingLabels) == 0 {
				result = "CAST([], 'Array(Tuple(String, String))')"
			} else {
				result = fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> has(%s, tag.1), %s))", sqlStringArrayLiteral(matching.MatchingLabels), result)
			}
		} else if len(matching.MatchingLabels) > 0 {
			result = fmt.Sprintf("arraySort(tag -> tag.1, arrayFilter(tag -> NOT has(%s, tag.1), %s))", sqlStringArrayLiteral(matching.MatchingLabels), result)
		}
	}
	if !isComparisonJoinOperator(cfg.Op) || cfg.ReturnBool {
		result = fmt.Sprintf("arrayFilter(tag -> tag.1 != '__name__', %s)", result)
	}
	if len(matching.Include) > 0 {
		result = fmt.Sprintf("arraySort(tag -> tag.1, arrayConcat(arrayFilter(tag -> NOT has(%s, tag.1), %s), arrayFilter(tag -> has(%s, tag.1), %s)))", sqlStringArrayLiteral(matching.Include), result, sqlStringArrayLiteral(matching.Include), oneSide)
	}
	return result
}

func buildBinaryValueSQL(op parser.ItemType, returnBool bool, lhsExpr, rhsExpr string) (string, string, error) {
	condition := ""
	switch op {
	case parser.ADD:
		return lhsExpr + " + " + rhsExpr, "", nil
	case parser.SUB:
		return lhsExpr + " - " + rhsExpr, "", nil
	case parser.MUL:
		return lhsExpr + " * " + rhsExpr, "", nil
	case parser.DIV:
		return lhsExpr + " / " + rhsExpr, "", nil
	case parser.MOD:
		return fmt.Sprintf("modulo(%s, %s)", lhsExpr, rhsExpr), "", nil
	case parser.POW:
		return fmt.Sprintf("pow(%s, %s)", lhsExpr, rhsExpr), "", nil
	case parser.EQLC:
		condition = lhsExpr + " = " + rhsExpr
	case parser.NEQ:
		condition = lhsExpr + " != " + rhsExpr
	case parser.GTR:
		condition = lhsExpr + " > " + rhsExpr
	case parser.LSS:
		condition = lhsExpr + " < " + rhsExpr
	case parser.GTE:
		condition = lhsExpr + " >= " + rhsExpr
	case parser.LTE:
		condition = lhsExpr + " <= " + rhsExpr
	default:
		return "", "", fmt.Errorf("native vector join SQL for operator %q is not implemented yet", op.String())
	}
	if condition == "" {
		return "", "", nil
	}
	if returnBool {
		return fmt.Sprintf("toFloat64(if(%s, 1, 0))", condition), "", nil
	}
	return lhsExpr, condition, nil
}

func isComparisonJoinOperator(op parser.ItemType) bool {
	switch op {
	case parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func mergeParamMaps(groups ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, group := range groups {
		for key, value := range group {
			merged[key] = value
		}
	}
	return merged
}

func normalizeStorageVectorMatching(vectorMatching *parser.VectorMatching) *parser.VectorMatching {
	if vectorMatching == nil {
		return &parser.VectorMatching{Card: parser.CardOneToOne}
	}
	cloned := &parser.VectorMatching{Card: vectorMatching.Card, MatchingLabels: append([]string(nil), vectorMatching.MatchingLabels...), On: vectorMatching.On, Include: append([]string(nil), vectorMatching.Include...)}
	return cloned
}
