package storage

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	modelpkg "github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type QueryConfig struct{ Database, Table string }

type AggregationSource struct {
	PromQLLeaf string
	Selector   *SelectorSource
	ValueExpr  string
	TagsExpr   string
}

var (
	aggValueOnLeftBinaryPattern  = regexp.MustCompile(`^\(\{value\}\)\s*([+\-*/])\s*(.+)$`)
	aggValueOnRightBinaryPattern = regexp.MustCompile(`^(.+)\s*([+\-*/])\s*\(\{value\}\)$`)
	aggModuloValueLeftPattern    = regexp.MustCompile(`^modulo\(\(\{value\}\),\s*(.+)\)$`)
	aggModuloValueRightPattern   = regexp.MustCompile(`^modulo\((.+),\s*\(\{value\}\)\)$`)
	aggPowValueLeftPattern       = regexp.MustCompile(`^pow\(\(\{value\}\),\s*(.+)\)$`)
	aggPowValueRightPattern      = regexp.MustCompile(`^pow\((.+),\s*\(\{value\}\)\)$`)
)

func BuildInstantQuerySQL(cfg QueryConfig, promql string, evaluationTimeMS int64) (string, map[string]string) {
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("*")}},
		From: sqlb.RawSource{SQL: strings.TrimSpace(`prometheusQuery(
    {database:String},
    {table:String},
    {promql:String},
    fromUnixTimestamp64Milli({evaluation_ms:Int64})
)`)},
	}
	sql, params, err := query.Build()
	if err != nil {
		panic(err)
	}
	params["param_database"] = cfg.Database
	params["param_table"] = cfg.Table
	params["param_promql"] = promql
	params["param_evaluation_ms"] = strconv.FormatInt(evaluationTimeMS, 10)
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", params
}

func BuildRangeQuerySQL(cfg QueryConfig, promql string, startMS, endMS, stepMS int64) (string, map[string]string) {
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("*")}},
		From: sqlb.RawSource{SQL: strings.TrimSpace(`prometheusQueryRange(
    {database:String},
    {table:String},
    {promql:String},
    fromUnixTimestamp64Milli({start_ms:Int64}),
    fromUnixTimestamp64Milli({end_ms:Int64}),
    toDecimal64({step_ms:Int64}, 3) / 1000
)`)},
	}
	sql, params, err := query.Build()
	if err != nil {
		panic(err)
	}
	params["param_database"] = cfg.Database
	params["param_table"] = cfg.Table
	params["param_promql"] = promql
	params["param_start_ms"] = strconv.FormatInt(startMS, 10)
	params["param_end_ms"] = strconv.FormatInt(endMS, 10)
	params["param_step_ms"] = strconv.FormatInt(stepMS, 10)
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", params
}

func BuildInstantAggregationQuerySQL(cfg QueryConfig, source AggregationSource, evaluationTimeMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	return BuildInstantAggregationQuerySQLWithBounds(cfg, source, evaluationTimeMS, evaluationTimeMS, evaluationTimeMS, op, grouping, without, paramNumber)
}

func BuildInstantAggregationQuerySQLWithBounds(cfg QueryConfig, source AggregationSource, evaluationTimeMS, requiredStartMS, requiredEndMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	sourceSQL, params, err := buildInstantSourceQuerySQL(cfg, source, evaluationTimeMS, requiredStartMS, requiredEndMS)
	if err != nil {
		return "", nil, err
	}
	return BuildInstantAggregationOverSubquerySQL(source, sourceSQL, params, op, grouping, without, paramNumber)
}

func BuildInstantAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	aggExpr, err := buildAggregationValueExpr(op, sqlb.Ident("value"), paramNumber)
	if err != nil {
		return "", nil, err
	}
	tagsExpr := buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without)
	sourceSubquery, err := renderAggregationInstantSourceSubquery(source, sourceSQL)
	if err != nil {
		return "", nil, err
	}
	middle := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: tagsExpr, Alias: "grouping_tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
		From:    sqlb.SubSelect{S: sourceSubquery},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("grouping_tags"), Alias: "tags"}, {Expr: sqlb.Call{Name: "max", Args: []sqlb.Expr{sqlb.Ident("timestamp")}}, Alias: "timestamp"}, {Expr: aggExpr, Alias: "value"}},
		From:    sqlb.SubSelect{S: middle},
		GroupBy: []sqlb.Expr{sqlb.Ident("grouping_tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("grouping_tags")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", clonedParams, nil
}

func BuildRangeAggregationQuerySQL(cfg QueryConfig, source AggregationSource, startMS, endMS, stepMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	return BuildRangeAggregationQuerySQLWithBounds(cfg, source, startMS, endMS, stepMS, startMS, endMS, op, grouping, without, paramNumber)
}

func BuildRangeAggregationQuerySQLWithBounds(cfg QueryConfig, source AggregationSource, startMS, endMS, stepMS, requiredStartMS, requiredEndMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	sourceSQL, params, err := buildRangeSourceQuerySQL(cfg, source, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	if err != nil {
		return "", nil, err
	}
	return BuildRangeAggregationOverSubquerySQL(source, sourceSQL, params, op, grouping, without, paramNumber)
}

func BuildRangeAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	aggExpr, err := buildAggregationValueExpr(op, sqlb.RawLit{V: "point.2"}, paramNumber)
	if err != nil {
		return "", nil, err
	}
	tagsExpr := buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without)
	sourceSubquery, err := renderAggregationRangeSourceSubquery(source, sourceSQL)
	if err != nil {
		return "", nil, err
	}
	points := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: tagsExpr, Alias: "grouping_tags"}, {Expr: sqlb.Call{Name: "arrayJoin", Args: []sqlb.Expr{sqlb.Ident("time_series")}}, Alias: "point"}},
		From:    sqlb.SubSelect{S: sourceSubquery},
	}
	grouped := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("grouping_tags"), Alias: "tags"}, {Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"}, {Expr: aggExpr, Alias: "value"}},
		From:    sqlb.SubSelect{S: points},
		GroupBy: []sqlb.Expr{sqlb.Ident("grouping_tags"), sqlb.Ident("timestamp")},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.RawLit{V: "arraySort(item -> item.1, groupArray((timestamp, value)))"}, Alias: "time_series"}},
		From:    sqlb.SubSelect{S: grouped},
		GroupBy: []sqlb.Expr{sqlb.Ident("tags")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + "\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n", clonedParams, nil
}

func BuildLabelsQuery(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	inner := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "arrayJoin(arrayMap(tag -> tag.1, series_tags))"}, Alias: "label"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("label"), Alias: "label"}},
		From:    sqlb.SubSelect{S: inner},
		GroupBy: []sqlb.Expr{sqlb.Ident("label")},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("label")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + "\nFORMAT JSONEachRow\n", params, nil
}

func BuildLabelValuesQuery(cfg QueryConfig, request *http.Request, labelName string) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	params["param_label_name"] = labelName
	inner := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "arrayJoin(series_tags)"}, Alias: "tag"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: "tag.2"}, Alias: "value"}},
		From:    sqlb.SubSelect{S: inner},
		Where:   sqlb.RawLit{V: "tag.1 = {label_name:String}"},
		GroupBy: []sqlb.Expr{sqlb.RawLit{V: "tag.2"}},
		OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("value")}},
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + "\nFORMAT JSONEachRow\n", params, nil
}

func BuildSeriesQuery(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	sourceSQL, params, err := buildSeriesTagsSource(cfg, request)
	if err != nil {
		return "", nil, err
	}
	query := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("series_tags"), Alias: "tags"}},
		From:    sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)},
		GroupBy: []sqlb.Expr{sqlb.Ident("series_tags")},
	}
	sql, _, err := query.Build()
	if err != nil {
		return "", nil, err
	}
	return sql + "\nFORMAT JSONEachRow\n", params, nil
}

func baseParams(cfg QueryConfig) map[string]string {
	return map[string]string{"param_database": cfg.Database, "param_table": cfg.Table}
}

func renderAggregationInstantSourceSubquery(source AggregationSource, sourceSQL string) (*sqlb.Select, error) {
	sourceValueExpr, err := CompileSourceValueTemplate(source.ValueExpr, sqlb.Ident("value"), sqlb.Ident("timestamp"))
	if err != nil {
		return nil, err
	}
	columns := []sqlb.ColExpr{{Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sourceValueExpr, Alias: "value"}}
	if aggregationSourceNeedsTags(source) {
		sourceTagsExpr, err := CompileSourceTagsTemplate(source.TagsExpr, sqlb.Ident("tags"))
		if err != nil {
			return nil, err
		}
		columns = append([]sqlb.ColExpr{{Expr: sourceTagsExpr, Alias: "tags"}}, columns...)
	}
	return &sqlb.Select{Columns: columns, From: sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)}}, nil
}

func renderAggregationRangeSourceSubquery(source AggregationSource, sourceSQL string) (*sqlb.Select, error) {
	sourceValueExpr, err := CompileSourceValueTemplate(source.ValueExpr, sqlb.RawLit{V: "point.2"}, sqlb.RawLit{V: "point.1"})
	if err != nil {
		return nil, err
	}
	sourceValueSQL, params, err := sqlb.BuildExpr(sourceValueExpr)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("aggregation source value template unexpectedly produced params: %#v", params)
	}
	timeSeriesExpr := sqlb.RawLit{V: "arrayMap(point -> (point.1, " + sourceValueSQL + "), time_series)"}
	columns := []sqlb.ColExpr{{Expr: timeSeriesExpr, Alias: "time_series"}}
	if aggregationSourceNeedsTags(source) {
		sourceTagsExpr, err := CompileSourceTagsTemplate(source.TagsExpr, sqlb.Ident("tags"))
		if err != nil {
			return nil, err
		}
		columns = append([]sqlb.ColExpr{{Expr: sourceTagsExpr, Alias: "tags"}}, columns...)
	}
	return &sqlb.Select{Columns: columns, From: sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)}}, nil
}

func aggregationSourceNeedsTags(source AggregationSource) bool {
	if source.Selector == nil {
		return true
	}
	return source.Selector.NeedTags
}

func CompileSourceValueTemplate(template string, base, timestamp sqlb.Expr) (sqlb.Expr, error) {
	baseSQL, params, err := sqlb.BuildExpr(base)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("aggregation base expression unexpectedly produced params: %#v", params)
	}
	timestampSQL, timestampParams, err := sqlb.BuildExpr(timestamp)
	if err != nil {
		return nil, err
	}
	if len(timestampParams) != 0 {
		return nil, fmt.Errorf("aggregation timestamp expression unexpectedly produced params: %#v", timestampParams)
	}
	template = strings.TrimSpace(template)
	switch template {
	case "{value}":
		return base, nil
	case "{timestamp}":
		return timestamp, nil
	case "-({value})":
		return sqlb.RawLit{V: "-(" + baseSQL + ")"}, nil
	}
	if strings.Contains(template, "{timestamp}") {
		replaced := strings.NewReplacer("{value}", baseSQL, "{timestamp}", timestampSQL).Replace(template)
		return sqlb.RawLit{V: replaced}, nil
	}
	if match := aggValueOnLeftBinaryPattern.FindStringSubmatch(template); match != nil {
		return sqlb.RawLit{V: "(" + baseSQL + ") " + match[1] + " " + strings.TrimSpace(match[2])}, nil
	}
	if match := aggValueOnRightBinaryPattern.FindStringSubmatch(template); match != nil {
		return sqlb.RawLit{V: strings.TrimSpace(match[1]) + " " + match[2] + " (" + baseSQL + ")"}, nil
	}
	if match := aggModuloValueLeftPattern.FindStringSubmatch(template); match != nil {
		return sqlb.RawLit{V: "modulo((" + baseSQL + "), " + strings.TrimSpace(match[1]) + ")"}, nil
	}
	if match := aggModuloValueRightPattern.FindStringSubmatch(template); match != nil {
		return sqlb.RawLit{V: "modulo(" + strings.TrimSpace(match[1]) + ", (" + baseSQL + "))"}, nil
	}
	if match := aggPowValueLeftPattern.FindStringSubmatch(template); match != nil {
		return sqlb.RawLit{V: "pow((" + baseSQL + "), " + strings.TrimSpace(match[1]) + ")"}, nil
	}
	if match := aggPowValueRightPattern.FindStringSubmatch(template); match != nil {
		return sqlb.RawLit{V: "pow(" + strings.TrimSpace(match[1]) + ", (" + baseSQL + "))"}, nil
	}
	if strings.Contains(template, "{value}") || strings.Contains(template, "{timestamp}") {
		replaced := strings.NewReplacer("{value}", baseSQL, "{timestamp}", timestampSQL).Replace(template)
		return sqlb.RawLit{V: replaced}, nil
	}
	return nil, fmt.Errorf("unsupported aggregation value template %q", template)
}

func CompileSourceTagsTemplate(template string, base sqlb.Expr) (sqlb.Expr, error) {
	baseSQL, params, err := sqlb.BuildExpr(base)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("aggregation base tags expression unexpectedly produced params: %#v", params)
	}
	switch strings.TrimSpace(template) {
	case "{tags}":
		return base, nil
	case "arrayFilter(tag -> tag.1 != '__name__', {tags})":
		return sqlb.RawLit{V: "arrayFilter(tag -> tag.1 != '__name__', " + baseSQL + ")"}, nil
	default:
		return nil, fmt.Errorf("unsupported aggregation tags template %q", template)
	}
}

func rawSubquerySQL(sql string) string {
	return "(\n" + indentSQL(strings.TrimSpace(sql), 4) + "\n)"
}

func buildAggregationTagsExpr(column sqlb.Expr, grouping []string, without bool) sqlb.Expr {
	columnSQL := renderStorageExprNoParams(column)
	if without {
		labels := append([]string{labels.MetricName}, grouping...)
		return sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.RawLit{V: "tag -> tag.1"},
			sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
				sqlb.RawLit{V: "tag -> NOT has(" + sqlStringArrayLiteral(labels) + ", tag.1)"},
				sqlb.RawLit{V: columnSQL},
			}},
		}}
	}
	if len(grouping) == 0 {
		return sqlb.RawLit{V: "CAST([], 'Array(Tuple(String, String))')"}
	}
	return sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
		sqlb.RawLit{V: "tag -> tag.1"},
		sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
			sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(grouping) + ", tag.1)"},
			sqlb.RawLit{V: columnSQL},
		}},
	}}
}

func buildAggregationValueExpr(op parser.ItemType, valueRef sqlb.Expr, paramNumber *float64) (sqlb.Expr, error) {
	valueSQL := renderStorageExprNoParams(valueRef)
	isNaNExpr := renderStorageExprNoParams(sqlb.Call{Name: "isNaN", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}}})
	notNaNExpr := "NOT " + isNaNExpr
	countNaNExpr := renderStorageExprNoParams(sqlb.Call{Name: "countIf", Args: []sqlb.Expr{sqlb.RawLit{V: isNaNExpr}}})
	countFiniteExpr := renderStorageExprNoParams(sqlb.Call{Name: "countIf", Args: []sqlb.Expr{sqlb.RawLit{V: notNaNExpr}}})
	nullFloatExpr := sqlb.RawLit{V: "CAST(NULL, 'Nullable(Float64)')"}
	switch op {
	case parser.SUM:
		return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countNaNExpr + " > 0"}, nullFloatExpr, sqlb.Call{Name: "sum", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}}}}}, nil
	case parser.COUNT:
		return sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "count", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}}}}}, nil
	case parser.MIN:
		return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countFiniteExpr + " = 0"}, nullFloatExpr, sqlb.Call{Name: "minIf", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}, sqlb.RawLit{V: notNaNExpr}}}}}, nil
	case parser.MAX:
		return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countFiniteExpr + " = 0"}, nullFloatExpr, sqlb.Call{Name: "maxIf", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}, sqlb.RawLit{V: notNaNExpr}}}}}, nil
	case parser.AVG:
		return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countNaNExpr + " > 0 OR count() = 0"}, nullFloatExpr, sqlb.Call{Name: "avg", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}}}}}, nil
	case parser.STDDEV:
		return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countNaNExpr + " > 0 OR count() = 0"}, nullFloatExpr, sqlb.Call{Name: "stddevPop", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}}}}}, nil
	case parser.STDVAR:
		return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countNaNExpr + " > 0 OR count() = 0"}, nullFloatExpr, sqlb.Call{Name: "varPop", Args: []sqlb.Expr{sqlb.RawLit{V: valueSQL}}}}}, nil
	case parser.QUANTILE:
		if paramNumber == nil {
			return nil, fmt.Errorf("native SQL aggregation for operator %q requires a scalar parameter", op.String())
		}
		switch {
		case math.IsNaN(*paramNumber):
			return sqlb.RawLit{V: "nan"}, nil
		case *paramNumber < 0:
			return sqlb.RawLit{V: "-inf"}, nil
		case *paramNumber > 1:
			return sqlb.RawLit{V: "inf"}, nil
		default:
			return sqlb.Call{Name: "if", Args: []sqlb.Expr{sqlb.RawLit{V: countNaNExpr + " > 0 OR count() = 0"}, nullFloatExpr, sqlb.RawLit{V: "quantile(" + NativeFloatLiteral(*paramNumber) + ")(" + valueSQL + ")"}}}, nil
		}
	case parser.GROUP:
		return sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.RawLit{V: "1"}}}, nil
	default:
		return nil, fmt.Errorf("native SQL aggregation for operator %q is not implemented yet", op.String())
	}
}

func buildSeriesTagsSource(cfg QueryConfig, request *http.Request) (string, map[string]string, error) {
	params := baseParams(cfg)
	tableRef := timeSeriesTableRef(cfg)
	matchers := request.URL.Query()["match[]"]
	if len(matchers) == 0 {
		matchers = request.URL.Query()["match"]
	}
	start, end, hasRange, err := readOptionalRange(request)
	if err != nil {
		return "", nil, err
	}
	selectors := [][]*labels.Matcher{{}}
	if len(matchers) > 0 {
		parsed, err := parser.NewParser(parser.Options{}).ParseMetricSelectors(matchers)
		if err != nil {
			return "", nil, err
		}
		selectors = parsed
	}
	parts := make([]string, 0, len(selectors))
	seriesTagsExpr := renderStorageExprNoParams(sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{
		sqlb.RawLit{V: "[tuple('__name__', metric_name)]"},
		sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
			sqlb.RawLit{V: "(k, v) -> tuple(k, v)"},
			sqlb.Call{Name: "mapKeys", Args: []sqlb.Expr{sqlb.RawLit{V: "tags"}}},
			sqlb.Call{Name: "mapValues", Args: []sqlb.Expr{sqlb.RawLit{V: "tags"}}},
		}},
	}})
	for index, selector := range selectors {
		whereClauses := make([]string, 0, len(selector)+2)
		for matcherIndex, matcher := range selector {
			clause, extraParams := compileMatcher(index, matcherIndex, matcher)
			whereClauses = append(whereClauses, clause)
			for key, value := range extraParams {
				params[key] = value
			}
		}
		if hasRange {
			params["param_start_ms"] = strconv.FormatInt(start.UnixMilli(), 10)
			params["param_end_ms"] = strconv.FormatInt(end.UnixMilli(), 10)
			whereClauses = append(whereClauses, "max_time >= fromUnixTimestamp64Milli({start_ms:Int64})", "min_time <= fromUnixTimestamp64Milli({end_ms:Int64})")
		}
		query := &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: sqlb.RawLit{V: seriesTagsExpr}, Alias: "series_tags"}},
			From:    sqlb.RawSource{SQL: "timeSeriesTags(" + tableRef + ")"},
		}
		if len(whereClauses) > 0 {
			query.Where = sqlb.RawLit{V: strings.Join(whereClauses, " AND ")}
		}
		sql, _, err := query.Build()
		if err != nil {
			return "", nil, err
		}
		parts = append(parts, sql)
	}
	return strings.Join(parts, "\nUNION ALL\n"), params, nil
}

func compileMatcher(selectorIndex, matcherIndex int, matcher *labels.Matcher) (string, map[string]string) {
	columnExpr := sqlb.Expr(sqlb.RawLit{V: "metric_name"})
	params := map[string]string{}
	if matcher.Name != labels.MetricName {
		keyName := "selector_" + strconv.Itoa(selectorIndex) + "_matcher_" + strconv.Itoa(matcherIndex) + "_key"
		columnExpr = sqlb.Subscr{Array: sqlb.RawLit{V: "tags"}, Index: sqlb.Call{Name: "concat", Args: []sqlb.Expr{sqlb.RawLit{V: "''"}, sqlb.Param{Name: keyName, Type: "String", V: matcher.Name}}}}
	}
	valueName := "selector_" + strconv.Itoa(selectorIndex) + "_matcher_" + strconv.Itoa(matcherIndex) + "_value"
	valueExpr := sqlb.Param{Name: valueName, Type: "String", V: matcherSQLPattern(matcher)}
	columnSQL, columnParams, err := sqlb.BuildExpr(columnExpr)
	if err != nil {
		panic(err)
	}
	mergeParams(params, columnParams)
	valueSQL, valueParams, err := sqlb.BuildExpr(valueExpr)
	if err != nil {
		panic(err)
	}
	mergeParams(params, valueParams)
	switch matcher.Type {
	case labels.MatchEqual:
		return columnSQL + " = " + valueSQL, params
	case labels.MatchNotEqual:
		return columnSQL + " != " + valueSQL, params
	case labels.MatchRegexp:
		return renderStorageExprNoParams(sqlb.Call{Name: "match", Args: []sqlb.Expr{sqlb.RawLit{V: columnSQL}, sqlb.RawLit{V: valueSQL}}}), params
	case labels.MatchNotRegexp:
		return "NOT " + renderStorageExprNoParams(sqlb.Call{Name: "match", Args: []sqlb.Expr{sqlb.RawLit{V: columnSQL}, sqlb.RawLit{V: valueSQL}}}), params
	default:
		return "1", params
	}
}

func readOptionalRange(request *http.Request) (start, end time.Time, hasRange bool, err error) {
	query := request.URL.Query()
	startRaw := query.Get("start")
	endRaw := query.Get("end")
	if startRaw == "" && endRaw == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	if startRaw == "" || endRaw == "" {
		return time.Time{}, time.Time{}, false, fmt.Errorf("start and end must be provided together")
	}
	start, err = modelpkg.ParsePrometheusTimestamp(startRaw)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	end, err = modelpkg.ParsePrometheusTimestamp(endRaw)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	return start, end, true, nil
}

func indentSQL(sql string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimSpace(sql), "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
func escapeIdentifier(value string) string { return strings.ReplaceAll(value, "`", "``") }
func sqlStringArrayLiteral(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, sqlStringLiteral(v))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
func sqlStringLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}
func NativeFloatLiteral(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
