package native

import (
	"fmt"
	"strings"

	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

type RenderMode string

const (
	RenderModeInstant RenderMode = "instant"
	RenderModeRange   RenderMode = "range"
)

type RenderParams struct {
	Mode                RenderMode
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

func RenderFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment == nil {
		return RenderedQuery{}, fmt.Errorf("native fragment render requires a fragment")
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr:
		return renderSourceFragment(cfg, fragment, params)
	case FragmentKindAggregation:
		return renderAggregationFragment(cfg, fragment, params)
	default:
		return RenderedQuery{}, fmt.Errorf("native SQL rendering for fragment kind %q is not implemented yet", fragment.Kind)
	}
}

func renderAggregationFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	if fragment.Aggregation == nil {
		return RenderedQuery{}, fmt.Errorf("aggregation fragment is missing aggregation metadata")
	}
	source, err := renderAggregationSource(fragment.Aggregation.Source, params)
	if err != nil {
		return RenderedQuery{}, err
	}
	switch params.Mode {
	case RenderModeInstant:
		sql, queryParams, err := storage.BuildInstantAggregationQuerySQLWithBounds(cfg, source, params.EvaluationTimeMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	case RenderModeRange:
		sql, queryParams, err := storage.BuildRangeAggregationQuerySQLWithBounds(cfg, source, params.StartMS, params.EndMS, params.StepMS, params.RequiredStartMS, params.RequiredEndMS, fragment.Aggregation.Op, fragment.Aggregation.Grouping, fragment.Aggregation.Without)
		if err != nil {
			return RenderedQuery{}, err
		}
		return RenderedQuery{SQL: sql, QueryParams: queryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderSourceFragment(cfg storage.QueryConfig, fragment *NativeFragment, params RenderParams) (RenderedQuery, error) {
	source, err := renderAggregationSource(fragment, params)
	if err != nil {
		return RenderedQuery{}, err
	}
	switch params.Mode {
	case RenderModeInstant:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return RenderedQuery{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		return RenderedQuery{SQL: wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
	case RenderModeRange:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return RenderedQuery{}, err
			}
			return RenderedQuery{SQL: wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return RenderedQuery{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		return RenderedQuery{SQL: wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr), QueryParams: queryParams}, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAggregationSource(fragment *NativeFragment, params RenderParams) (storage.AggregationSource, error) {
	if fragment == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation fragment is missing its source fragment")
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr:
		// Supported by the initial renderer skeleton.
	default:
		return storage.AggregationSource{}, fmt.Errorf("aggregation source fragment kind %q is not renderable yet", fragment.Kind)
	}
	if fragment.Selector != nil {
		return storage.AggregationSource{
			Selector: &storage.SelectorSource{
				Kind:       storage.SelectorKind(fragment.Selector.Kind),
				MetricName: fragment.Selector.MetricName,
				Matchers:   cloneMatchers(fragment.Selector.Matchers),
				LookbackMS: fragment.Selector.Lookback.Milliseconds(),
				OffsetMS:   fragment.Selector.Offset.Milliseconds(),
			},
			ValueExpr: fragment.ValueExpr,
			TagsExpr:  fragment.TagsExpr,
		}, nil
	}
	if fragment.SourcePromQL == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation source fragment is missing its PromQL leaf")
	}
	if params.ResolveSourcePromQL == nil {
		return storage.AggregationSource{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
	}
	promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
	if err != nil {
		return storage.AggregationSource{}, err
	}
	return storage.AggregationSource{PromQLLeaf: promQL, ValueExpr: fragment.ValueExpr, TagsExpr: fragment.TagsExpr}, nil
}

func wrapInstantSourceQuery(sourceSQL, valueExpr, tagsExpr string) string {
	sourceTagsExpr := strings.ReplaceAll(tagsExpr, "{tags}", "tags")
	sourceValueExpr := strings.ReplaceAll(valueExpr, "{value}", "value")
	return fmt.Sprintf(`
SELECT
    %s AS tags,
    timestamp,
    %s AS value
FROM (
%s
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, sourceTagsExpr, sourceValueExpr, localIndentSQL(strings.TrimSpace(sourceSQL), 4))
}

func wrapRangeSourceQuery(sourceSQL, valueExpr, tagsExpr string) string {
	sourceTagsExpr := strings.ReplaceAll(tagsExpr, "{tags}", "tags")
	sourceValueExpr := strings.ReplaceAll(valueExpr, "{value}", "point.2")
	return fmt.Sprintf(`
SELECT
    %s AS tags,
    arrayMap(point -> (point.1, %s), time_series) AS time_series
FROM (
%s
)
SETTINGS allow_experimental_time_series_table = 1
FORMAT JSONEachRow
`, sourceTagsExpr, sourceValueExpr, localIndentSQL(strings.TrimSpace(sourceSQL), 4))
}

func localIndentSQL(sql string, spaces int) string {
	indent := strings.Repeat(" ", spaces)
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
