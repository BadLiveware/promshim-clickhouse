package renderer

import (
	"fmt"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage/schema"

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
	rf, err := renderFragment(cfg, fragment, params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

func renderFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil {
		return renderedFragment{}, fmt.Errorf("native fragment render requires a fragment")
	}
	if params.Mode == native.RenderModeRange && native.HasFixedTemporalAnchor(fragment) && fragment.Kind != native.FragmentKindBinaryVectorJoin {
		return renderAnchoredRangeFragment(cfg, fragment, params)
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
	case native.FragmentKindSortTransform:
		return renderSortTransformFragment(cfg, fragment, params)
	case native.FragmentKindLabelTransform:
		return renderLabelTransformFragment(cfg, fragment, params)
	case native.FragmentKindClampTransform:
		return renderClampTransformFragment(cfg, fragment, params)
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
		return renderedFragment{}, fmt.Errorf("native SQL rendering for fragment kind %q is not implemented yet", fragment.Kind)
	}
}

func mergeRenderedQueryParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func trimRenderedQuerySQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if idx := strings.LastIndex(sql, schema.SettingsLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	if idx := strings.LastIndex(sql, schema.FormatLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	return sql
}
