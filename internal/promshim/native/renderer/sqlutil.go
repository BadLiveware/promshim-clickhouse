package renderer

import (
	"ch-observability/internal/promshim/native"
	"fmt"
	"sort"
	"strings"

	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
	"ch-observability/internal/promshim/storage/schema"

	"github.com/prometheus/prometheus/model/labels"
)

func outputMetricTagsSQL(metric map[string]string) string {
	if len(metric) == 0 {
		return "CAST([], '" + schema.TagsArrayType + "')"
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
	return "CAST([" + strings.Join(items, ", ") + "], '" + schema.TagsArrayType + "')"
}

func sqlStringLiteral(value string) string {
	escaped := strings.ReplaceAll(value, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	return "'" + escaped + "'"
}

func sourceWrapperIsIdentity(fragment *native.NativeFragment) bool {
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
	return sql + schema.QuerySuffix, nil
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

func localIndentSQL(sql string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
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

func selectorEffectiveMatchers(selector *native.SelectorSource) []*labels.Matcher {
	if selector == nil {
		return nil
	}
	if len(selector.PushedMatchers) > 0 {
		return native.CloneMatchers(selector.PushedMatchers)
	}
	matchers := native.CloneMatchers(selector.Matchers)
	matchers = append(matchers, native.CloneMatchers(selector.InferredMatchers)...)
	return matchers
}

func selectorNeedsTags(selector *native.SelectorSource) bool {
	if selector == nil {
		return true
	}
	return selector.RequireFullTags || len(selector.RequiredTagLabels) > 0
}

func alignSubqueryStepStart(windowStartMS, stepMS int64) int64 {
	if stepMS <= 0 {
		return windowStartMS
	}
	aligned := (windowStartMS / stepMS) * stepMS
	if aligned < windowStartMS {
		aligned += stepMS
	}
	return aligned
}

func rangeRequiredBoundsForChild(fragment *native.NativeFragment, startMS, endMS int64) (int64, int64) {
	selector := native.BaseSelectorSource(fragment)
	if selector == nil {
		return startMS, endMS
	}
	lookbackMS := selector.Lookback.Milliseconds()
	offsetMS := selector.Offset.Milliseconds()
	return startMS - offsetMS - lookbackMS, endMS - offsetMS
}

func renderFragmentSubquery(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams, prefix string) (string, map[string]string, error) {
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
