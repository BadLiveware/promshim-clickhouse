package renderer

import (
	"fmt"
	"math"
	"strings"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

func renderClampTransformFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.ClampTransform == nil || fragment.ClampTransform.Child == nil {
		return renderedFragment{}, fmt.Errorf("clamp transform fragment is missing child metadata")
	}
	spec := fragment.ClampTransform
	queryParams := map[string]string{}
	childSQL, childParams, err := renderFragmentSubquery(cfg, spec.Child, params, "clamp_child")
	if err != nil {
		return renderedFragment{}, err
	}
	mergeRenderedQueryParams(queryParams, childParams)
	minBinding, err := renderClampBoundBindingFragment(cfg, spec.Min, params, "clamp_min", math.Inf(-1))
	if err != nil {
		return renderedFragment{}, err
	}
	mergeClampBoundBindingParams(queryParams, minBinding)
	maxBinding, err := renderClampBoundBindingFragment(cfg, spec.Max, params, "clamp_max", math.Inf(1))
	if err != nil {
		return renderedFragment{}, err
	}
	mergeClampBoundBindingParams(queryParams, maxBinding)
	return assembleClampSQL(spec.Func, childSQL, queryParams, minBinding, maxBinding, params)
}

// clampBoundBinding collects the per-bound (min or max) SQL pieces and the
// namespaced parameters produced by rendering a scalar bound. The SQL pieces
// are consumed by assembleClampSQL; the params are accumulated into the shared
// queryParams map via mergeClampBoundBindingParams so the Fragment and direct
// paths produce identical parameter sets.
type clampBoundBinding struct {
	ValueExpr       string
	InstantJoin     string
	InstantJoinSQL  string
	RangeJoin       string
	RangeBindingSQL string
	InstantParams   map[string]string
	BindingParams   map[string]string
}

// renderClampBoundBindingFragment renders a Fragment-path scalar bound. When
// spec is nil the helper returns the neutral binding (±Inf) with no joins. The
// defaultValue argument is the literal to use for the absent side of a
// clamp_min / clamp_max call where only one bound is present.
func renderClampBoundBindingFragment(cfg storage.QueryConfig, spec *native.NativeFragment, params RenderParams, prefix string, defaultValue float64) (clampBoundBinding, error) {
	binding := clampBoundBinding{ValueExpr: storage.NativeFloatLiteral(defaultValue)}
	if spec == nil {
		return binding, nil
	}
	joinSQL, instantParams, valueExpr, err := renderInstantScalarBinding(cfg, spec, params, prefix)
	if err != nil {
		return clampBoundBinding{}, err
	}
	binding.InstantParams = instantParams
	if joinSQL != "" {
		binding.InstantJoin = " CROSS JOIN (" + joinSQL + ") AS " + prefix
		binding.InstantJoinSQL = joinSQL
	}
	binding.ValueExpr = valueExpr
	rangeBindingSQL, bindingParams, rangeValueExpr, err := renderRangeScalarBinding(cfg, spec, params, prefix)
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

// mergeClampBoundBindingParams merges a bound's namespaced params into the
// shared queryParams map in the same order as the pre-extraction code: instant
// params first, then range binding params.
func mergeClampBoundBindingParams(queryParams map[string]string, binding clampBoundBinding) {
	mergeRenderedQueryParams(queryParams, binding.InstantParams)
	mergeRenderedQueryParams(queryParams, binding.BindingParams)
}

// assembleClampSQL builds the final clamp wrapper SQL. Shared between the
// Fragment path (renderClampTransformFragment) and the direct-render path
// (lowerClampDirect) so both emit byte-identical SQL. childSQL must already be
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

func renderInstantScalarBinding(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams, prefix string) (string, map[string]string, string, error) {
	if fragment == nil {
		return "", nil, "", nil
	}
	if fragment.Kind == native.FragmentKindSyntheticSeries && fragment.Synthetic != nil && fragment.Synthetic.Func == "literal" && fragment.Synthetic.Value != nil {
		return "", nil, storage.NativeFloatLiteral(*fragment.Synthetic.Value), nil
	}
	sql, queryParams, err := renderFragmentSubquery(cfg, fragment, params, prefix)
	if err != nil {
		return "", nil, "", err
	}
	return "SELECT if(count() = 1, any(value), nan) AS value FROM (" + sql + ") AS " + prefix, queryParams, prefix + ".value", nil
}

func renderRangeScalarBinding(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams, prefix string) (string, map[string]string, string, error) {
	if fragment == nil {
		return "", nil, "", nil
	}
	if fragment.Kind == native.FragmentKindSyntheticSeries && fragment.Synthetic != nil && fragment.Synthetic.Func == "literal" && fragment.Synthetic.Value != nil {
		return "", nil, storage.NativeFloatLiteral(*fragment.Synthetic.Value), nil
	}
	sql, queryParams, err := renderFragmentSubquery(cfg, fragment, params, prefix)
	if err != nil {
		return "", nil, "", err
	}
	bindingSQL := "SELECT point.1 AS timestamp, if(count() = 1, any(point.2), nan) AS value FROM (" + sql + ") AS " + prefix + " ARRAY JOIN " + prefix + ".time_series AS point GROUP BY point.1"
	return bindingSQL, queryParams, prefix + ".value", nil
}

// renderInstantScalarBindingFromLogical is the logical-node counterpart of
// renderInstantScalarBinding. It short-circuits scalar literals to an inline
// float literal (matching the Fragment fast path) and falls through to Lower
// for any other shape, bubbling errUnsupportedLowerNode from there so the
// whole query falls back hierarchically.
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

// renderRangeScalarBindingFromLogical is the logical-node counterpart of
// renderRangeScalarBinding. Literals short-circuit to inline floats; other
// shapes lower via Lower so SQL stays byte-identical to the Fragment path.
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
