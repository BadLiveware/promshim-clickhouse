package storage

import (
	"fmt"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage/schema"
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
	if isStorageSetOperator(cfg.Op) {
		return buildSetVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, rangeMode)
	}
	valueExpr, valueFilter, err := buildBinaryValueExpr(cfg.Op, cfg.ReturnBool, sqlb.Ident("lhs.value"), sqlb.Ident("rhs.value"))
	if err != nil {
		return "", nil, err
	}
	joinOn := sqlb.Expr(sqlb.RawLit{V: "lhs.join_group = rhs.join_group"})
	if rangeMode {
		joinOn = sqlb.RawLit{V: "lhs.join_group = rhs.join_group AND lhs.timestamp = rhs.timestamp"}
	}

	lhsPrepared, err := buildPreparedJoinSideSelect("lhs", buildJoinSource(lhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), cfg.VectorMatching), rangeMode, cfg.JoinShape == "one_to_one" || cfg.JoinShape == "one_to_many")
	if err != nil {
		return "", nil, err
	}
	rhsPrepared, err := buildPreparedJoinSideSelect("rhs", buildJoinSource(rhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), cfg.VectorMatching), rangeMode, cfg.JoinShape == "one_to_one" || cfg.JoinShape == "many_to_one")
	if err != nil {
		return "", nil, err
	}

	joinedRows := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: buildBinaryResultTagsExpr(cfg), Alias: "result_tags"},
			{Expr: sqlb.Ident("lhs.timestamp"), Alias: "timestamp"},
			{Expr: valueExpr, Alias: "value"},
		},
		From: sqlb.Join{
			Left:  sqlb.SubSelect{S: lhsPrepared, Alias: "lhs"},
			Right: sqlb.SubSelect{S: rhsPrepared, Alias: "rhs"},
			Kind:  "INNER",
			On:    joinOn,
		},
		Where: valueFilter,
	}

	params := mergeParamMaps(lhsParams, rhsParams)
	if rangeMode {
		grouped := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.Ident("result_tags"), Alias: "result_tags"},
				{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
				{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}}, Alias: "value"},
			},
			From:    sqlb.SubSelect{S: joinedRows},
			GroupBy: []sqlb.Expr{sqlb.Ident("result_tags"), sqlb.Ident("timestamp")},
			Having:  sqlb.RawLit{V: "throwIf(count() > 1, 'multiple matches for labels: grouping labels must ensure unique matches') = 0"},
		}
		outer := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.Ident("result_tags"), Alias: "tags"},
				{Expr: schema.SortedTimeSeriesGroupArrayExpr(), Alias: "time_series"},
			},
			From:    sqlb.SubSelect{S: grouped},
			GroupBy: []sqlb.Expr{sqlb.Ident("result_tags")},
			OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("result_tags")}},
		}
		sql, _, err := outer.Build()
		if err != nil {
			return "", nil, err
		}
		return sql + schema.QuerySuffix, params, nil
	}

	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("result_tags"), Alias: "tags"},
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("timestamp")}}, Alias: "timestamp"},
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}}, Alias: "value"},
		},
		From:    sqlb.SubSelect{S: joinedRows},
		GroupBy: []sqlb.Expr{sqlb.Ident("result_tags")},
		Having:  sqlb.RawLit{V: "throwIf(count() > 1, 'multiple matches for labels: grouping labels must ensure unique matches') = 0"},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("result_tags")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func isStorageSetOperator(op parser.ItemType) bool {
	switch op {
	case parser.LAND, parser.LOR, parser.LUNLESS:
		return true
	default:
		return false
	}
}

func buildSetVectorJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg BinaryJoinConfig, rangeMode bool) (string, map[string]string, error) {
	joinOn := "lhs.join_group = rhs.join_group"
	groupCols := "join_group"
	presenceCols := "join_group"
	lhsTs := "lhs.timestamp"
	rhsTs := "rhs.timestamp"
	if rangeMode {
		joinOn += " AND lhs.timestamp = rhs.timestamp"
		groupCols += ", timestamp"
		presenceCols += ", timestamp"
	}
	lhsPrepared, err := buildPreparedJoinSideSelect("lhs", buildJoinSource(lhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), cfg.VectorMatching), rangeMode, false)
	if err != nil {
		return "", nil, err
	}
	rhsPrepared, err := buildPreparedJoinSideSelect("rhs", buildJoinSource(rhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), cfg.VectorMatching), rangeMode, false)
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
	lhsPresenceSQL := "SELECT " + presenceCols + " FROM (" + lhsPreparedSQL + ") AS lhs GROUP BY " + groupCols
	rhsPresenceSQL := "SELECT " + presenceCols + " FROM (" + rhsPreparedSQL + ") AS rhs GROUP BY " + groupCols
	var combinedRowsSQL string
	switch cfg.Op {
	case parser.LAND:
		combinedRowsSQL = "SELECT lhs.original_group AS result_tags, " + lhsTs + " AS timestamp, lhs.value AS value FROM (" + lhsPreparedSQL + ") AS lhs INNER JOIN (" + rhsPresenceSQL + ") AS rhs ON " + joinOn
	case parser.LUNLESS:
		combinedRowsSQL = "SELECT lhs.original_group AS result_tags, " + lhsTs + " AS timestamp, lhs.value AS value FROM (" + lhsPreparedSQL + ") AS lhs LEFT JOIN (" + rhsPresenceSQL + ") AS rhs ON " + joinOn + " WHERE rhs.join_group IS NULL"
	case parser.LOR:
		combinedRowsSQL = "SELECT lhs.original_group AS result_tags, " + lhsTs + " AS timestamp, lhs.value AS value FROM (" + lhsPreparedSQL + ") AS lhs UNION ALL " +
			"SELECT rhs.original_group AS result_tags, " + rhsTs + " AS timestamp, rhs.value AS value FROM (" + rhsPreparedSQL + ") AS rhs LEFT JOIN (" + lhsPresenceSQL + ") AS lhs ON " + joinOn + " WHERE lhs.join_group IS NULL"
	default:
		return "", nil, fmt.Errorf("native vector join SQL for operator %q is not implemented yet", cfg.Op.String())
	}
	params := mergeParamMaps(lhsParams, rhsParams)
	if rangeMode {
		sql := "SELECT result_tags AS tags, " + renderStorageExprNoParams(schema.SortedTimeSeriesGroupArrayExpr()) + " AS time_series FROM (" + combinedRowsSQL + ") AS joined_rows GROUP BY result_tags ORDER BY result_tags" + schema.QuerySuffix
		return sql, params, nil
	}
	sql := "SELECT result_tags AS tags, timestamp AS timestamp, value AS value FROM (" + combinedRowsSQL + ") AS joined_rows ORDER BY result_tags" + schema.QuerySuffix
	return sql, params, nil
}

func buildPreparedJoinSideSelect(side string, source sqlb.Source, joinGroupExpr sqlb.Expr, withTimestamp bool, checkDuplicates bool) (*sqlb.Select, error) {
	joinGroup := joinGroupExpr
	if !checkDuplicates {
		return &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.Ident("tags"), Alias: "original_group"},
				{Expr: joinGroup, Alias: "join_group"},
				{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
				{Expr: sqlb.Ident("value"), Alias: "value"},
			},
			From: source,
		}, nil
	}

	columns := []sqlb.ColExpr{
		{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("tags")}}, Alias: "original_group"},
		{Expr: joinGroup, Alias: "join_group"},
		{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}}, Alias: "value"},
	}
	groupBy := []sqlb.Expr{joinGroup}
	if withTimestamp {
		columns = []sqlb.ColExpr{
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("tags")}}, Alias: "original_group"},
			{Expr: joinGroup, Alias: "join_group"},
			{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"},
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}}, Alias: "value"},
		}
		groupBy = append(groupBy, sqlb.Ident("timestamp"))
	} else {
		columns = []sqlb.ColExpr{
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("tags")}}, Alias: "original_group"},
			{Expr: joinGroup, Alias: "join_group"},
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("timestamp")}}, Alias: "timestamp"},
			{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}}, Alias: "value"},
		}
	}
	return &sqlb.Select{
		Columns: columns,
		From:    source,
		GroupBy: groupBy,
		Having:  sqlb.RawLit{V: "throwIf(count() > 1, 'found duplicate series for the match group on the " + side + " hand-side of the operation') = 0"},
	}, nil
}

func buildJoinSource(sourceSQL string, rangeMode bool) sqlb.Source {
	if !rangeMode {
		return sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)}
	}
	return sqlb.SubSelect{S: flattenRangeSourceSelect(sourceSQL)}
}

func flattenRangeSourceSelect(sourceSQL string) *sqlb.Select {
	return &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: sqlb.Ident("tags"), Alias: "tags"},
			{Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"},
			{Expr: sqlb.RawLit{V: "point.2"}, Alias: "value"},
		},
		From: sqlb.ArrayJoin{
			Base:  sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)},
			Expr:  sqlb.Ident("time_series"),
			Alias: "point",
		},
	}
}

func buildJoinGroupExpr(tagsExpr sqlb.Expr, vectorMatching *parser.VectorMatching) sqlb.Expr {
	matching := normalizeStorageVectorMatching(vectorMatching)
	if matching.On {
		if len(matching.MatchingLabels) == 0 {
			return schema.EmptyTagsArrayExpr()
		}
		return sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
			sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(matching.MatchingLabels) + ", tag.1)"}, tagsExpr}},
		}}
	}
	ignored := append([]string{labels.MetricName}, matching.MatchingLabels...)
	return sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
		sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
		sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> NOT has(" + sqlStringArrayLiteral(ignored) + ", tag.1)"}, tagsExpr}},
	}}
}

func buildBinaryResultTagsExpr(cfg BinaryJoinConfig) sqlb.Expr {
	matching := normalizeStorageVectorMatching(cfg.VectorMatching)
	base := sqlb.Expr(sqlb.Ident("lhs.original_group"))
	oneSide := sqlb.Expr(sqlb.Ident("rhs.original_group"))
	if cfg.JoinShape == "one_to_many" {
		base = sqlb.Ident("rhs.original_group")
		oneSide = sqlb.Ident("lhs.original_group")
	}
	result := base
	if cfg.JoinShape == "one_to_one" {
		if matching.On {
			if len(matching.MatchingLabels) == 0 {
				result = schema.EmptyTagsArrayExpr()
			} else {
				result = sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
					sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
					sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(matching.MatchingLabels) + ", tag.1)"}, result}},
				}}
			}
		} else if len(matching.MatchingLabels) > 0 {
			result = sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
				sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
				sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> NOT has(" + sqlStringArrayLiteral(matching.MatchingLabels) + ", tag.1)"}, result}},
			}}
		}
	}
	if !isComparisonJoinOperator(cfg.Op) || cfg.ReturnBool {
		result = sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> tag.1 != '__name__'"}, result}}
	}
	if len(matching.Include) > 0 {
		result = sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
			sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{
				sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> NOT has(" + sqlStringArrayLiteral(matching.Include) + ", tag.1)"}, result}},
				sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(matching.Include) + ", tag.1)"}, oneSide}},
			}},
		}}
	}
	return result
}

func buildBinaryValueExpr(op parser.ItemType, returnBool bool, lhsExpr, rhsExpr sqlb.Expr) (sqlb.Expr, sqlb.Expr, error) {
	condition := sqlb.Expr(nil)
	switch op {
	case parser.ADD:
		return sqlb.Binary{Op: "+", L: lhsExpr, R: rhsExpr}, nil, nil
	case parser.SUB:
		return sqlb.Binary{Op: "-", L: lhsExpr, R: rhsExpr}, nil, nil
	case parser.MUL:
		return sqlb.Binary{Op: "*", L: lhsExpr, R: rhsExpr}, nil, nil
	case parser.DIV:
		return sqlb.Binary{Op: "/", L: lhsExpr, R: rhsExpr}, nil, nil
	case parser.MOD:
		return sqlb.Call{Name: "modulo", Args: []sqlb.Expr{lhsExpr, rhsExpr}}, nil, nil
	case parser.POW:
		return sqlb.Call{Name: "pow", Args: []sqlb.Expr{lhsExpr, rhsExpr}}, nil, nil
	case parser.EQLC:
		condition = sqlb.Binary{Op: "=", L: lhsExpr, R: rhsExpr}
	case parser.NEQ:
		condition = sqlb.Binary{Op: "!=", L: lhsExpr, R: rhsExpr}
	case parser.GTR:
		condition = sqlb.Binary{Op: ">", L: lhsExpr, R: rhsExpr}
	case parser.LSS:
		condition = sqlb.Binary{Op: "<", L: lhsExpr, R: rhsExpr}
	case parser.GTE:
		condition = sqlb.Binary{Op: ">=", L: lhsExpr, R: rhsExpr}
	case parser.LTE:
		condition = sqlb.Binary{Op: "<=", L: lhsExpr, R: rhsExpr}
	default:
		return nil, nil, fmt.Errorf("native vector join SQL for operator %q is not implemented yet", op.String())
	}
	if condition == nil {
		return nil, nil, nil
	}
	if returnBool {
		return sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.RawLit{V: "if(" + mustBuildBinaryJoinExpr(condition) + ", 1, 0)"}}}, nil, nil
	}
	return lhsExpr, condition, nil
}

func mustBuildBinaryJoinExpr(expr sqlb.Expr) string {
	sql, params, err := sqlb.BuildExpr(expr)
	if err != nil {
		panic(err)
	}
	if len(params) != 0 {
		panic(fmt.Errorf("binary join expression unexpectedly produced params: %#v", params))
	}
	return sql
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
