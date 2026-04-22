package renderer

import (
	"fmt"
	"strings"

	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"

	"github.com/prometheus/prometheus/promql/parser"
)

type RenderParams struct {
	Mode                native.RenderMode
	EvaluationTimeMS    int64
	StartMS             int64
	EndMS               int64
	StepMS              int64
	RequiredStartMS     int64
	RequiredEndMS       int64
	ResolveSourcePromQL func(parser.Expr) (string, error)
}

type RenderedQuery struct {
	SQL         string
	QueryParams map[string]string
}

func RenderFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil {
		return RenderedQuery{}, fmt.Errorf("native fragment render requires a fragment")
	}
	switch fragment.Kind {
	case native.FragmentKindLeafSource, native.FragmentKindUnarySourceExpr, native.FragmentKindBinaryScalarSourceExpr:
		return renderSourceFragment(cfg, fragment, params)
	case native.FragmentKindSyntheticSeries:
		return renderSyntheticFragment(fragment, params)
	case native.FragmentKindScalarConvert:
		return renderScalarConvertFragment(cfg, fragment, params)
	case native.FragmentKindInfoJoin:
		return renderInfoJoinFragment(cfg, fragment, params)
	case native.FragmentKindAbsent:
		return renderAbsentFragment(cfg, fragment, params)
	case native.FragmentKindHistogramProjection:
		return renderHistogramProjectionFragment(cfg, fragment, params)
	case native.FragmentKindHistogramFunction:
		return renderHistogramFunctionFragment(cfg, fragment, params)
	case native.FragmentKindSubquery:
		return renderSubqueryFragment(cfg, fragment, params)
	case native.FragmentKindRangeFunction:
		return renderRangeFunctionFragment(cfg, fragment, params)
	case native.FragmentKindBinaryVectorJoin:
		return renderBinaryJoinFragment(cfg, fragment, params)
	case native.FragmentKindAggregation:
		return renderAggregationFragment(cfg, fragment, params)
	case native.FragmentKindValueTransform:
		return renderValueTransformFragment(cfg, fragment, params)
	default:
		return RenderedQuery{}, fmt.Errorf("native SQL rendering for fragment kind %q is not implemented yet", fragment.Kind)
	}
}

func mergeRenderedQueryParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func trimRenderedQuerySQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if idx := strings.LastIndex(sql, "SETTINGS allow_experimental_time_series_table = 1"); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	if idx := strings.LastIndex(sql, "FORMAT JSONEachRow"); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	return sql
}
