package renderer

import (
	"fmt"
	"strings"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

// clampBoundBinding collects the per-bound (min or max) SQL pieces and the
// namespaced parameters produced by rendering a scalar bound. The SQL pieces
// are consumed by assembleClampSQL; the params are accumulated into the shared
// queryParams map via mergeClampBoundBindingParams.
type clampBoundBinding struct {
	ValueExpr       string
	InstantJoin     string
	InstantJoinSQL  string
	RangeJoin       string
	RangeBindingSQL string
	InstantParams   map[string]string
	BindingParams   map[string]string
}

// mergeClampBoundBindingParams merges a bound's namespaced params into the
// shared queryParams map: instant params first, then range binding params.
func mergeClampBoundBindingParams(queryParams map[string]string, binding clampBoundBinding) {
	mergeRenderedQueryParams(queryParams, binding.InstantParams)
	mergeRenderedQueryParams(queryParams, binding.BindingParams)
}

// assembleClampSQL builds the final clamp wrapper SQL. Used by the
// direct-render path (lowerClampDirect). childSQL must already be
// namespaced under the "clamp_child" alias and queryParams must already contain
// the child's and both bounds' rendered params.
func assembleClampSQL(funcName, childSQL string, queryParams map[string]string, minBinding, maxBinding clampBoundBinding, params RenderParams) (renderedFragment, error) {
	tagsExpr := "arrayFilter(tag -> tag.1 != '__name__', base.tags)"
	instantTagsExpr := strings.ReplaceAll(tagsExpr, "base.", "clamp_child.")
	valueExpr := clampValueExpr(funcName, "clamp_child.value", minBinding.ValueExpr, maxBinding.ValueExpr)
	rangeValueExpr := clampValueExpr(funcName, "base.value", minBinding.ValueExpr, maxBinding.ValueExpr)
	whereClause := clampInvalidBoundsWhere(funcName, minBinding.ValueExpr, maxBinding.ValueExpr)
	switch params.Mode {
	case native.RenderModeInstant:
		sql := "SELECT " + instantTagsExpr + " AS tags, clamp_child.timestamp AS timestamp, " + valueExpr + " AS value FROM (" + childSQL + ") AS clamp_child" + minBinding.InstantJoin + maxBinding.InstantJoin + whereClause
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		baseSQL := "SELECT clamp_child.tags AS tags, point.1 AS timestamp, point.2 AS value FROM (" + childSQL + ") AS clamp_child ARRAY JOIN clamp_child.time_series AS point"
		rowSQL := "SELECT " + tagsExpr + " AS tags, base.timestamp AS timestamp, " + rangeValueExpr + " AS value FROM (" + baseSQL + ") AS base" + minBinding.RangeJoin + maxBinding.RangeJoin + whereClause
		finalSQL := "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" + rowSQL + ") AS clamp_rows GROUP BY tags"
		return renderedFragment{RawSQL: trimRenderedQuerySQL(finalSQL), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

// renderInstantScalarBindingFromLogical is the logical-node counterpart for
// scalar bounds in instant mode. It short-circuits scalar literals to an
// inline float literal and falls through to Lower for any other shape,
// bubbling errUnsupportedLowerNode from there so the whole query falls back
// hierarchically.
func renderInstantScalarBindingFromLogical(ctx LoweringCtx, node logicalpkg.Node, prefix string) (string, map[string]string, string, error) {
	if node == nil {
		return "", nil, "", nil
	}
	if literal, ok := node.(*logicalpkg.ScalarLiteralPlan); ok {
		return "", nil, storage.NativeFloatLiteral(literal.Value), nil
	}
	rq, err := Lower(ctx, node)
	if err != nil {
		return "", nil, "", err
	}
	sql, queryParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rq.SQL), rq.QueryParams, prefix)
	if err != nil {
		return "", nil, "", err
	}
	return "SELECT if(count() = 1, any(value), nan) AS value FROM (" + sql + ") AS " + prefix, queryParams, prefix + ".value", nil
}

// renderRangeScalarBindingFromLogical binds a scalar parameter node to SQL
// for range-function arguments. Literals short-circuit to inline floats; other
// shapes lower via Lower.
func renderRangeScalarBindingFromLogical(ctx LoweringCtx, node logicalpkg.Node, prefix string) (string, map[string]string, string, error) {
	if node == nil {
		return "", nil, "", nil
	}
	if literal, ok := node.(*logicalpkg.ScalarLiteralPlan); ok {
		return "", nil, storage.NativeFloatLiteral(literal.Value), nil
	}
	rq, err := Lower(ctx, node)
	if err != nil {
		return "", nil, "", err
	}
	sql, queryParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rq.SQL), rq.QueryParams, prefix)
	if err != nil {
		return "", nil, "", err
	}
	bindingSQL := "SELECT point.1 AS timestamp, if(count() = 1, any(point.2), nan) AS value FROM (" + sql + ") AS " + prefix + " ARRAY JOIN " + prefix + ".time_series AS point GROUP BY point.1"
	return bindingSQL, queryParams, prefix + ".value", nil
}

// renderClampBoundBindingLogical mirrors renderClampBoundBindingFragment but
// drives off a logical.Node scalar bound. Nil node yields the neutral binding
// (±Inf). Non-literal nodes lower through Lower; errUnsupportedLowerNode
// bubbles up so the whole-query fallback takes over.
func renderClampBoundBindingLogical(ctx LoweringCtx, node logicalpkg.Node, prefix string, defaultValue float64) (clampBoundBinding, error) {
	binding := clampBoundBinding{ValueExpr: storage.NativeFloatLiteral(defaultValue)}
	if node == nil {
		return binding, nil
	}
	joinSQL, instantParams, valueExpr, err := renderInstantScalarBindingFromLogical(ctx, node, prefix)
	if err != nil {
		return clampBoundBinding{}, err
	}
	binding.InstantParams = instantParams
	if joinSQL != "" {
		binding.InstantJoin = " CROSS JOIN (" + joinSQL + ") AS " + prefix
		binding.InstantJoinSQL = joinSQL
	}
	binding.ValueExpr = valueExpr
	rangeBindingSQL, bindingParams, rangeValueExpr, err := renderRangeScalarBindingFromLogical(ctx, node, prefix)
	if err != nil {
		return clampBoundBinding{}, err
	}
	binding.BindingParams = bindingParams
	if rangeBindingSQL != "" {
		binding.RangeJoin = " LEFT JOIN (" + rangeBindingSQL + ") AS " + prefix + " ON " + prefix + ".timestamp = base.timestamp"
		binding.RangeBindingSQL = rangeBindingSQL
	}
	if rangeValueExpr != "" {
		binding.ValueExpr = rangeValueExpr
	}
	return binding, nil
}

func clampValueExpr(name, valueExpr, minValueExpr, maxValueExpr string) string {
	switch name {
	case "clamp":
		return "if(isNaN(" + valueExpr + ") OR isNaN(" + minValueExpr + ") OR isNaN(" + maxValueExpr + "), nan, greatest(" + minValueExpr + ", least(" + maxValueExpr + ", " + valueExpr + ")))"
	case "clamp_min":
		return "if(isNaN(" + valueExpr + ") OR isNaN(" + minValueExpr + "), nan, greatest(" + valueExpr + ", " + minValueExpr + "))"
	case "clamp_max":
		return "if(isNaN(" + valueExpr + ") OR isNaN(" + maxValueExpr + "), nan, least(" + valueExpr + ", " + maxValueExpr + "))"
	default:
		return valueExpr
	}
}

func clampInvalidBoundsWhere(name, minValueExpr, maxValueExpr string) string {
	if name != "clamp" {
		return ""
	}
	return " WHERE NOT (" + maxValueExpr + " < " + minValueExpr + ")"
}
