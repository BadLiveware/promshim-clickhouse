package renderer

import (
	"fmt"
	"math"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
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
	minValueExpr := storage.NativeFloatLiteral(math.Inf(-1))
	instantMinJoin := ""
	rangeMinJoin := ""
	if spec.Min != nil {
		joinSQL, instantParams, valueExpr, err := renderInstantScalarBinding(cfg, spec.Min, params, "clamp_min")
		if err != nil {
			return renderedFragment{}, err
		}
		mergeRenderedQueryParams(queryParams, instantParams)
		if joinSQL != "" {
			instantMinJoin = " CROSS JOIN (" + joinSQL + ") AS clamp_min"
		}
		minValueExpr = valueExpr
		rangeBindingSQL, bindingParams, rangeValueExpr, err := renderRangeScalarBinding(cfg, spec.Min, params, "clamp_min")
		if err != nil {
			return renderedFragment{}, err
		}
		mergeRenderedQueryParams(queryParams, bindingParams)
		if rangeBindingSQL != "" {
			rangeMinJoin = " LEFT JOIN (" + rangeBindingSQL + ") AS clamp_min ON clamp_min.timestamp = base.timestamp"
		}
		if rangeValueExpr != "" {
			minValueExpr = rangeValueExpr
		}
	}
	maxValueExpr := storage.NativeFloatLiteral(math.Inf(1))
	instantMaxJoin := ""
	rangeMaxJoin := ""
	if spec.Max != nil {
		joinSQL, instantParams, valueExpr, err := renderInstantScalarBinding(cfg, spec.Max, params, "clamp_max")
		if err != nil {
			return renderedFragment{}, err
		}
		mergeRenderedQueryParams(queryParams, instantParams)
		if joinSQL != "" {
			instantMaxJoin = " CROSS JOIN (" + joinSQL + ") AS clamp_max"
		}
		maxValueExpr = valueExpr
		rangeBindingSQL, bindingParams, rangeValueExpr, err := renderRangeScalarBinding(cfg, spec.Max, params, "clamp_max")
		if err != nil {
			return renderedFragment{}, err
		}
		mergeRenderedQueryParams(queryParams, bindingParams)
		if rangeBindingSQL != "" {
			rangeMaxJoin = " LEFT JOIN (" + rangeBindingSQL + ") AS clamp_max ON clamp_max.timestamp = base.timestamp"
		}
		if rangeValueExpr != "" {
			maxValueExpr = rangeValueExpr
		}
	}
	tagsExpr := "arrayFilter(tag -> tag.1 != '__name__', base.tags)"
	instantTagsExpr := strings.ReplaceAll(tagsExpr, "base.", "clamp_child.")
	valueExpr := clampValueExpr(spec.Func, "clamp_child.value", minValueExpr, maxValueExpr)
	rangeValueExpr := clampValueExpr(spec.Func, "base.value", minValueExpr, maxValueExpr)
	whereClause := clampInvalidBoundsWhere(spec.Func, minValueExpr, maxValueExpr)
	switch params.Mode {
	case native.RenderModeInstant:
		sql := "SELECT " + instantTagsExpr + " AS tags, clamp_child.timestamp AS timestamp, " + valueExpr + " AS value FROM (" + childSQL + ") AS clamp_child" + instantMinJoin + instantMaxJoin + whereClause
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		baseSQL := "SELECT clamp_child.tags AS tags, point.1 AS timestamp, point.2 AS value FROM (" + childSQL + ") AS clamp_child ARRAY JOIN clamp_child.time_series AS point"
		rowSQL := "SELECT " + tagsExpr + " AS tags, base.timestamp AS timestamp, " + rangeValueExpr + " AS value FROM (" + baseSQL + ") AS base" + rangeMinJoin + rangeMaxJoin + whereClause
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
