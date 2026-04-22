package storage

import (
	"fmt"

	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/promql/parser"
)

type InfoJoinConfig struct {
	IdentifyingLabels []string
	CopyLabelNames    []string
	DropUnmatched     bool
}

func BuildInstantInfoJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg InfoJoinConfig) (string, map[string]string, error) {
	return buildInfoJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, false)
}

func BuildRangeInfoJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg InfoJoinConfig) (string, map[string]string, error) {
	return buildInfoJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, cfg, true)
}

func buildInfoJoinSQL(lhsSQL string, lhsParams map[string]string, rhsSQL string, rhsParams map[string]string, cfg InfoJoinConfig, rangeMode bool) (string, map[string]string, error) {
	matching := &parser.VectorMatching{On: true, MatchingLabels: append([]string(nil), cfg.IdentifyingLabels...)}
	lhsPrepared, err := buildPreparedJoinSideSelect("lhs", buildJoinSource(lhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), matching), rangeMode, false)
	if err != nil {
		return "", nil, err
	}
	rhsPrepared, err := buildPreparedJoinSideSelect("rhs", buildJoinSource(rhsSQL, rangeMode), buildJoinGroupExpr(sqlb.Ident("tags"), matching), rangeMode, true)
	if err != nil {
		return "", nil, err
	}
	joinOn := sqlb.Expr(sqlb.RawLit{V: "lhs.join_group = rhs.join_group"})
	if rangeMode {
		joinOn = sqlb.RawLit{V: "lhs.join_group = rhs.join_group AND lhs.timestamp = rhs.timestamp"}
	}
	joinedRows := &sqlb.Select{
		Columns: []sqlb.ColExpr{
			{Expr: buildInfoJoinResultTagsExpr(cfg), Alias: "result_tags"},
			{Expr: sqlb.Ident("lhs.timestamp"), Alias: "timestamp"},
			{Expr: sqlb.Ident("lhs.value"), Alias: "value"},
		},
		From: sqlb.Join{
			Left:  sqlb.SubSelect{S: lhsPrepared, Alias: "lhs"},
			Right: sqlb.SubSelect{S: rhsPrepared, Alias: "rhs"},
			Kind:  "LEFT",
			On:    joinOn,
		},
	}
	if cfg.DropUnmatched {
		joinedRows.Where = sqlb.RawLit{V: "length(rhs.original_group) > 0"}
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
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("result_tags")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + schema.QuerySuffix, params, nil
}

func buildInfoJoinResultTagsExpr(cfg InfoJoinConfig) sqlb.Expr {
	lhsNames := "arrayMap(tag -> tag.1, lhs.original_group)"
	ignored := append([]string{"__name__"}, cfg.IdentifyingLabels...)
	condition := "NOT has(" + sqlStringArrayLiteral(ignored) + ", tag.1) AND NOT has(" + lhsNames + ", tag.1)"
	if len(cfg.CopyLabelNames) > 0 {
		condition += " AND has(" + sqlStringArrayLiteral(cfg.CopyLabelNames) + ", tag.1)"
	}
	filteredRHS := sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{sqlb.RawLit{V: "tag -> " + condition}, sqlb.Ident("rhs.original_group")}}
	merged := sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")}, sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{sqlb.Ident("lhs.original_group"), filteredRHS}}}}
	return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: "length(rhs.original_group) = 0"}, sqlb.Ident("lhs.original_group"), merged}}
}

func validateInfoJoinConfig(cfg InfoJoinConfig) error {
	if len(cfg.IdentifyingLabels) == 0 {
		return fmt.Errorf("info join requires at least one identifying label")
	}
	return nil
}
