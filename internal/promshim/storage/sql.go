package storage

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/emit"
	modelpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type QueryConfig struct {
	Database string
	Table    string

	// PromotedTagColumns lists Prometheus label names that are available as
	// first-class columns on the TimeSeries tags table via ClickHouse's
	// tags_to_columns setting. When present, selector matching can read the
	// typed column directly instead of probing the tags Map value.
	PromotedTagColumns map[string]struct{}

	// EnableNativeGridFunctions allows guarded tier-2 lowering to call
	// ClickHouse TimeSeries grid functions for supported range operators.
	EnableNativeGridFunctions bool
}

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
	return sql + schema.QuerySuffix, params
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
	return sql + schema.QuerySuffix, params
}

func BuildInstantAggregationQuerySQL(cfg QueryConfig, source AggregationSource, evaluationTimeMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	return BuildInstantAggregationQuerySQLWithBounds(cfg, source, evaluationTimeMS, evaluationTimeMS, evaluationTimeMS, op, grouping, without, paramNumber, paramString)
}

func BuildInstantAggregationQuerySQLWithBounds(cfg QueryConfig, source AggregationSource, evaluationTimeMS, requiredStartMS, requiredEndMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	sourceSQL, params, err := buildInstantSourceQuerySQL(cfg, source, evaluationTimeMS, requiredStartMS, requiredEndMS)
	if err != nil {
		return "", nil, err
	}
	return BuildInstantAggregationOverSubquerySQL(source, sourceSQL, params, evaluationTimeMS, op, grouping, without, paramNumber, paramString)
}

func BuildInstantAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, evaluationTimeMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	if IsSelectionAggregation(op) {
		return buildInstantSelectionAggregationOverSubquerySQL(source, sourceSQL, params, op, grouping, without, paramNumber)
	}
	if op == parser.COUNT_VALUES {
		return buildInstantCountValuesAggregationOverSubquerySQL(source, sourceSQL, params, evaluationTimeMS, grouping, without, paramString)
	}
	aggExpr, err := buildAggregationValueExpr(op, sqlb.Ident("value"), paramNumber)
	if err != nil {
		return "", nil, err
	}
	tagsExpr := buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without)
	fromSource := sqlb.Source(sqlb.RawSource{SQL: rawSubquerySQL(sourceSQL)})
	if !aggregationSourceIsIdentity(source) {
		sourceSubquery, err := renderAggregationInstantSourceSubquery(source, sourceSQL)
		if err != nil {
			return "", nil, err
		}
		fromSource = sqlb.SubSelect{S: sourceSubquery}
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: tagsExpr, Alias: "tags"}, {Expr: sqlb.RawLit{V: "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"}, Alias: "timestamp"}, {Expr: aggExpr, Alias: "value"}},
		From:    fromSource,
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
	return sql + schema.QuerySuffix, clonedParams, nil
}

func BuildRangeAggregationQuerySQL(cfg QueryConfig, source AggregationSource, startMS, endMS, stepMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	return BuildRangeAggregationQuerySQLWithBounds(cfg, source, startMS, endMS, stepMS, startMS, endMS, op, grouping, without, paramNumber, paramString)
}

func BuildRangeAggregationQuerySQLWithBounds(cfg QueryConfig, source AggregationSource, startMS, endMS, stepMS, requiredStartMS, requiredEndMS int64, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	sourceSQL, params, err := buildRangeSourceQuerySQL(cfg, source, requiredStartMS, requiredEndMS, startMS, endMS, stepMS)
	if err != nil {
		return "", nil, err
	}
	return BuildRangeAggregationOverSubquerySQL(source, sourceSQL, params, op, grouping, without, paramNumber, paramString)
}

func BuildRangeAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64, paramString string) (string, map[string]string, error) {
	if IsSelectionAggregation(op) {
		return buildRangeSelectionAggregationOverSubquerySQL(source, sourceSQL, params, op, grouping, without, paramNumber)
	}
	if op == parser.COUNT_VALUES {
		return buildRangeCountValuesAggregationOverSubquerySQL(source, sourceSQL, params, grouping, without, paramString)
	}
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
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
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
	return sql + schema.QuerySuffix, clonedParams, nil
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
	return sql + schema.FormatSuffix, params, nil
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
	return sql + schema.FormatSuffix, params, nil
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
	return sql + schema.FormatSuffix, params, nil
}

func baseParams(cfg QueryConfig) map[string]string {
	return map[string]string{"param_database": cfg.Database, "param_table": cfg.Table}
}

func aggregationSourceIsIdentity(source AggregationSource) bool {
	return strings.TrimSpace(source.ValueExpr) == "{value}" && (strings.TrimSpace(source.TagsExpr) == "" || strings.TrimSpace(source.TagsExpr) == "{tags}")
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

func IsSelectionAggregation(op parser.ItemType) bool {
	switch op {
	case parser.TOPK, parser.BOTTOMK, parser.LIMITK, parser.LIMIT_RATIO:
		return true
	default:
		return false
	}
}

func selectionAggregationKValue(op parser.ItemType, paramNumber *float64) (int, error) {
	if paramNumber == nil {
		return 0, fmt.Errorf("native SQL aggregation for operator %q requires a scalar parameter", op.String())
	}
	if math.IsNaN(*paramNumber) {
		return 0, fmt.Errorf("parameter value is NaN")
	}
	return int(*paramNumber), nil
}

func selectionAggregationRatioValue(paramNumber *float64) (float64, error) {
	if paramNumber == nil {
		return 0, fmt.Errorf("native SQL aggregation for operator %q requires a scalar parameter", "limit_ratio")
	}
	if math.IsNaN(*paramNumber) {
		return 0, fmt.Errorf("ratio value is NaN")
	}
	ratio := *paramNumber
	if ratio < -1 {
		ratio = -1
	}
	if ratio > 1 {
		ratio = 1
	}
	return ratio, nil
}

func selectionAggregationOrder(op parser.ItemType) string {
	switch op {
	case parser.TOPK:
		return "DESC"
	case parser.BOTTOMK:
		return "ASC"
	case parser.LIMITK:
		return "ASC"
	default:
		panic("unexpected selection aggregation operator")
	}
}

func selectionTagsHashExpr(tagsRef sqlb.Expr) (sqlb.Expr, error) {
	tagsSQL, params, err := sqlb.BuildExpr(tagsRef)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("selection aggregation tags expression unexpectedly produced params: %#v", params)
	}
	return sqlb.RawLit{V: "xxHash64(toJSONString(" + tagsSQL + "))"}, nil
}

func countValuesGrouping(grouping []string, without bool, valueLabel string) []string {
	if without {
		return append([]string(nil), grouping...)
	}
	result := append([]string(nil), grouping...)
	for _, label := range result {
		if label == valueLabel {
			return result
		}
	}
	return append(result, valueLabel)
}

func countValuesStringExpr(valueRef sqlb.Expr) (sqlb.Expr, error) {
	valueSQL, params, err := sqlb.BuildExpr(valueRef)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("count_values value expression unexpectedly produced params: %#v", params)
	}
	return sqlb.RawLit{V: "multiIf(isNaN(" + valueSQL + "), 'NaN', isInfinite(" + valueSQL + ") AND (" + valueSQL + ") > 0, '+Inf', isInfinite(" + valueSQL + ") AND (" + valueSQL + ") < 0, '-Inf', toString(" + valueSQL + "))"}, nil
}

func buildCountValuesMutatedTagsExpr(tagsRef, valueRef sqlb.Expr, valueLabel string) (sqlb.Expr, error) {
	tagsSQL, params, err := sqlb.BuildExpr(tagsRef)
	if err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("count_values tags expression unexpectedly produced params: %#v", params)
	}
	valueStringExpr, err := countValuesStringExpr(valueRef)
	if err != nil {
		return nil, err
	}
	valueStringSQL, valueParams, err := sqlb.BuildExpr(valueStringExpr)
	if err != nil {
		return nil, err
	}
	if len(valueParams) != 0 {
		return nil, fmt.Errorf("count_values value-string expression unexpectedly produced params: %#v", valueParams)
	}
	return sqlb.RawLit{V: "arrayConcat(arrayFilter(tag -> tag.1 != '__name__' AND tag.1 != '" + valueLabel + "', " + tagsSQL + "), [tuple('" + valueLabel + "', " + valueStringSQL + ")])"}, nil
}

func buildInstantCountValuesAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, evaluationTimeMS int64, grouping []string, without bool, paramString string) (string, map[string]string, error) {
	if strings.TrimSpace(paramString) == "" {
		return "", nil, fmt.Errorf("native SQL aggregation for operator %q requires a string label parameter", "count_values")
	}
	effectiveGrouping := countValuesGrouping(grouping, without, paramString)
	sourceSubquery, err := renderAggregationInstantSourceSubquery(source, sourceSQL)
	if err != nil {
		return "", nil, err
	}
	mutatedTagsExpr, err := buildCountValuesMutatedTagsExpr(sqlb.Ident("tags"), sqlb.Ident("value"), paramString)
	if err != nil {
		return "", nil, err
	}
	groupingTagsExpr := buildAggregationTagsExpr(mutatedTagsExpr, effectiveGrouping, without)
	middle := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: groupingTagsExpr, Alias: "grouping_tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}},
		From:    sqlb.SubSelect{S: sourceSubquery},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("grouping_tags"), Alias: "tags"}, {Expr: sqlb.RawLit{V: "fromUnixTimestamp64Milli(" + strconv.FormatInt(evaluationTimeMS, 10) + ")"}, Alias: "timestamp"}, {Expr: sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "count"}}}, Alias: "value"}},
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
	return sql + schema.QuerySuffix, clonedParams, nil
}

func buildRangeCountValuesAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, grouping []string, without bool, paramString string) (string, map[string]string, error) {
	if strings.TrimSpace(paramString) == "" {
		return "", nil, fmt.Errorf("native SQL aggregation for operator %q requires a string label parameter", "count_values")
	}
	effectiveGrouping := countValuesGrouping(grouping, without, paramString)
	sourceSubquery, err := renderAggregationRangeSourceSubquery(source, sourceSQL)
	if err != nil {
		return "", nil, err
	}
	mutatedTagsExpr, err := buildCountValuesMutatedTagsExpr(sqlb.Ident("tags"), sqlb.RawLit{V: "point.2"}, paramString)
	if err != nil {
		return "", nil, err
	}
	groupingTagsExpr := buildAggregationTagsExpr(mutatedTagsExpr, effectiveGrouping, without)
	points := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: groupingTagsExpr, Alias: "grouping_tags"}, {Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"}},
		From:    sqlb.ArrayJoin{Base: sqlb.SubSelect{S: sourceSubquery}, Expr: sqlb.RawLit{V: "time_series"}, Alias: "point"},
	}
	grouped := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("grouping_tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Call{Name: "toFloat64", Args: []sqlb.Expr{sqlb.Call{Name: "count"}}}, Alias: "value"}},
		From:    sqlb.SubSelect{S: points},
		GroupBy: []sqlb.Expr{sqlb.Ident("grouping_tags"), sqlb.Ident("timestamp")},
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
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
	return sql + schema.QuerySuffix, clonedParams, nil
}

func buildInstantSelectionAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	tagsExpr := buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without)
	sourceSubquery, err := renderAggregationInstantSourceSubquery(source, sourceSQL)
	if err != nil {
		return "", nil, err
	}
	middleColumns := []sqlb.ColExpr{{Expr: tagsExpr, Alias: "grouping_tags"}, {Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}}
	if op == parser.LIMIT_RATIO {
		hashExpr, err := selectionTagsHashExpr(sqlb.Ident("tags"))
		if err != nil {
			return "", nil, err
		}
		middleColumns = append(middleColumns, sqlb.ColExpr{Expr: hashExpr, Alias: "tag_hash"})
	}
	middle := &sqlb.Select{Columns: middleColumns, From: sqlb.SubSelect{S: sourceSubquery}}
	var outer *sqlb.Select
	switch op {
	case parser.TOPK, parser.BOTTOMK, parser.LIMITK:
		k, err := selectionAggregationKValue(op, paramNumber)
		if err != nil {
			return "", nil, err
		}
		orderExpr := "tags ASC"
		if op != parser.LIMITK {
			orderExpr = "isNaN(value) ASC, value " + selectionAggregationOrder(op) + ", tags ASC"
		}
		ranked := &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}, {Expr: sqlb.RawLit{V: "row_number() OVER (PARTITION BY grouping_tags ORDER BY " + orderExpr + ")"}, Alias: "rank"}},
			From:    sqlb.SubSelect{S: middle},
		}
		outer = &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
			From:    sqlb.SubSelect{S: ranked},
			Where:   sqlb.RawLit{V: "rank <= " + strconv.Itoa(k)},
			OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}},
		}
	case parser.LIMIT_RATIO:
		ratio, err := selectionAggregationRatioValue(paramNumber)
		if err != nil {
			return "", nil, err
		}
		if ratio == 0 {
			outer = &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}}, From: sqlb.SubSelect{S: middle}, Where: sqlb.RawLit{V: "0 = 1"}}
		} else if ratio >= 1 || ratio <= -1 {
			outer = &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}}, From: sqlb.SubSelect{S: middle}, OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}}}
		} else {
			threshold := uint64(math.Abs(ratio) * float64(^uint64(0)))
			cmp := "tag_hash < " + strconv.FormatUint(threshold, 10)
			if ratio < 0 {
				cmp = "tag_hash >= " + strconv.FormatUint(uint64((1+ratio)*float64(^uint64(0))), 10)
			}
			outer = &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}}, From: sqlb.SubSelect{S: middle}, Where: sqlb.RawLit{V: cmp}, OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("tags")}}}
		}
	default:
		return "", nil, fmt.Errorf("native SQL selection aggregation for operator %q is not implemented yet", op.String())
	}
	sql, _, err := outer.Build()
	if err != nil {
		return "", nil, err
	}
	clonedParams := map[string]string{}
	for key, value := range params {
		clonedParams[key] = value
	}
	return sql + schema.QuerySuffix, clonedParams, nil
}

func buildRangeSelectionAggregationOverSubquerySQL(source AggregationSource, sourceSQL string, params map[string]string, op parser.ItemType, grouping []string, without bool, paramNumber *float64) (string, map[string]string, error) {
	tagsExpr := buildAggregationTagsExpr(sqlb.Ident("tags"), grouping, without)
	sourceSubquery, err := renderAggregationRangeSourceSubquery(source, sourceSQL)
	if err != nil {
		return "", nil, err
	}
	pointColumns := []sqlb.ColExpr{{Expr: tagsExpr, Alias: "grouping_tags"}, {Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"}, {Expr: sqlb.RawLit{V: "point.2"}, Alias: "value"}}
	if op == parser.LIMIT_RATIO {
		hashExpr, err := selectionTagsHashExpr(sqlb.Ident("tags"))
		if err != nil {
			return "", nil, err
		}
		pointColumns = append(pointColumns, sqlb.ColExpr{Expr: hashExpr, Alias: "tag_hash"})
	}
	points := &sqlb.Select{Columns: pointColumns, From: sqlb.ArrayJoin{Base: sqlb.SubSelect{S: sourceSubquery}, Expr: sqlb.RawLit{V: "time_series"}, Alias: "point"}}
	var selected *sqlb.Select
	switch op {
	case parser.TOPK, parser.BOTTOMK, parser.LIMITK:
		k, err := selectionAggregationKValue(op, paramNumber)
		if err != nil {
			return "", nil, err
		}
		orderExpr := "tags ASC"
		if op != parser.LIMITK {
			orderExpr = "isNaN(value) ASC, value " + selectionAggregationOrder(op) + ", tags ASC"
		}
		ranked := &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}, {Expr: sqlb.RawLit{V: "row_number() OVER (PARTITION BY grouping_tags, timestamp ORDER BY " + orderExpr + ")"}, Alias: "rank"}},
			From:    sqlb.SubSelect{S: points},
		}
		selected = &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
			From:    sqlb.SubSelect{S: ranked},
			Where:   sqlb.RawLit{V: "rank <= " + strconv.Itoa(k)},
		}
	case parser.LIMIT_RATIO:
		ratio, err := selectionAggregationRatioValue(paramNumber)
		if err != nil {
			return "", nil, err
		}
		selected = &sqlb.Select{
			Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: sqlb.Ident("timestamp"), Alias: "timestamp"}, {Expr: sqlb.Ident("value"), Alias: "value"}},
			From:    sqlb.SubSelect{S: points},
		}
		if ratio == 0 {
			selected.Where = sqlb.RawLit{V: "0 = 1"}
		} else if ratio >= 1 || ratio <= -1 {
			// keep all rows
		} else if ratio > 0 {
			selected.Where = sqlb.RawLit{V: "tag_hash < " + strconv.FormatUint(uint64(ratio*float64(^uint64(0))), 10)}
		} else {
			selected.Where = sqlb.RawLit{V: "tag_hash >= " + strconv.FormatUint(uint64((1+ratio)*float64(^uint64(0))), 10)}
		}
	default:
		return "", nil, fmt.Errorf("native SQL selection aggregation for operator %q is not implemented yet", op.String())
	}
	outer := &sqlb.Select{
		Columns: []sqlb.ColExpr{{Expr: sqlb.Ident("tags"), Alias: "tags"}, {Expr: emit.SortedTimeSeriesGroupArray(), Alias: "time_series"}},
		From:    sqlb.SubSelect{S: selected},
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
	return sql + schema.QuerySuffix, clonedParams, nil
}

func buildAggregationTagsExpr(column sqlb.Expr, grouping []string, without bool) sqlb.Expr {
	columnSQL := renderStorageExprNoParams(column)
	if without {
		labels := append([]string{labels.MetricName}, grouping...)
		return sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
			sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
				sqlb.RawLit{V: "tag -> NOT has(" + sqlStringArrayLiteral(labels) + ", tag.1)"},
				sqlb.RawLit{V: columnSQL},
			}},
		}}
	}
	if len(grouping) == 0 {
		return emit.EmptyTagsArray()
	}
	filtered := sqlb.Call{Name: "arrayFilter", Args: []sqlb.Expr{
		sqlb.RawLit{V: "tag -> has(" + sqlStringArrayLiteral(grouping) + ", tag.1)"},
		sqlb.RawLit{V: columnSQL},
	}}
	if len(grouping) == 1 {
		return filtered
	}
	return sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
		sqlb.Lambda{Params: []sqlb.Ident{"tag"}, Body: sqlb.Ident("tag.1")},
		filtered,
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
