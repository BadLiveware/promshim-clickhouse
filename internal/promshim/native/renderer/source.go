package renderer

import (
	"github.com/BadLiveware/promshim-ch/internal/promshim/emit"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"fmt"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func renderSourceFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	source, err := renderAggregationSource(fragment, params)
	if err != nil {
		return renderedFragment{}, err
	}
	switch params.Mode {
	case native.RenderModeInstant:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return renderedFragment{}, err
			}
			if sourceWrapperIsIdentity(fragment) {
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
			}
			wrappedSQL, err := wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		wrappedSQL, err := wrapInstantSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		if source.Selector != nil {
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return renderedFragment{}, err
			}
			if sourceWrapperIsIdentity(fragment) {
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
			}
			wrappedSQL, err := wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
		}
		if params.ResolveSourcePromQL == nil || fragment.SourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(fragment.SourcePromQL)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		wrappedSQL, err := wrapRangeSourceQuery(sql, fragment.ValueExpr, fragment.TagsExpr)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderSyntheticFragment(fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.Synthetic == nil {
		return renderedFragment{}, fmt.Errorf("synthetic series fragment is missing synthetic metadata")
	}
	emptyTags := emit.EmptyTagsArray()
	switch params.Mode {
	case native.RenderModeInstant:
		valueSQL, err := syntheticSeriesValueSQL(fragment.Synthetic, "{evaluation_ms:Int64}")
		if err != nil {
			return renderedFragment{}, err
		}
		evalParam := sqlb.Param{Name: "evaluation_ms", Type: "Int64", V: params.EvaluationTimeMS}
		return renderedFragment{Select: &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: emptyTags, Alias: "tags"},
				{Expr: sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{evalParam}}, Alias: "timestamp"},
				{Expr: sqlb.RawLit{V: valueSQL}, Alias: "value"},
			},
		}}, nil
	case native.RenderModeRange:
		if params.StepMS <= 0 {
			return renderedFragment{}, fmt.Errorf("synthetic range render requires a positive step")
		}
		valueSQL, err := syntheticSeriesValueSQL(fragment.Synthetic, "ts_ms")
		if err != nil {
			return renderedFragment{}, err
		}
		startParam := sqlb.Param{Name: "start_ms", Type: "Int64", V: params.StartMS}
		endParam := sqlb.Param{Name: "end_ms", Type: "Int64", V: params.EndMS}
		stepParam := sqlb.Param{Name: "step_ms", Type: "Int64", V: params.StepMS}
		timeSeriesExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
			sqlb.Lambda{
				Params: []sqlb.Ident{"ts_ms"},
				Body: sqlb.Tuple{Elems: []sqlb.Expr{
					sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{sqlb.Ident("ts_ms")}},
					sqlb.RawLit{V: valueSQL},
				}},
			},
			sqlb.Call{Name: "range", Args: []sqlb.Expr{
				startParam,
				sqlb.Binary{Op: "+", L: endParam, R: stepParam},
				stepParam,
			}},
		}}
		return renderedFragment{Select: &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: emptyTags, Alias: "tags"},
				{Expr: timeSeriesExpr, Alias: "time_series"},
			},
		}}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func syntheticSeriesValueSQL(fragment *native.SyntheticSeriesFragment, tsMSExpr string) (string, error) {
	if fragment == nil {
		return "", fmt.Errorf("synthetic series metadata is missing")
	}
	utcTs := "toTimeZone(fromUnixTimestamp64Milli(" + tsMSExpr + "), 'UTC')"
	switch fragment.Func {
	case "literal":
		if fragment.Value == nil {
			return "", fmt.Errorf("synthetic literal fragment requires a value")
		}
		return storage.NativeFloatLiteral(*fragment.Value), nil
	case "pi":
		return "toFloat64(3.141592653589793)", nil
	case "time":
		return "toFloat64(" + tsMSExpr + ") / 1000.0", nil
	case "minute":
		return "toFloat64(toMinute(" + utcTs + "))", nil
	case "hour":
		return "toFloat64(toHour(" + utcTs + "))", nil
	case "day_of_week":
		return "toFloat64(modulo(toDayOfWeek(" + utcTs + "), 7))", nil
	case "day_of_month":
		return "toFloat64(toDayOfMonth(" + utcTs + "))", nil
	case "day_of_year":
		return "toFloat64(toDayOfYear(" + utcTs + "))", nil
	case "days_in_month":
		return "toFloat64(toDaysInMonth(" + utcTs + "))", nil
	case "month":
		return "toFloat64(toMonth(" + utcTs + "))", nil
	case "year":
		return "toFloat64(toYear(" + utcTs + "))", nil
	default:
		return "", fmt.Errorf("synthetic series function %q is not implemented yet", fragment.Func)
	}
}

func renderScalarConvertFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.ScalarConvert == nil || fragment.ScalarConvert.Child == nil {
		return renderedFragment{}, fmt.Errorf("scalar convert fragment is missing child metadata")
	}
	childSource, childParams, err := renderChildAsSource(cfg, fragment.ScalarConvert.Child, params, "scalar_child")
	if err != nil {
		return renderedFragment{}, err
	}
	emptyTags := emit.EmptyTagsArray()
	switch params.Mode {
	case native.RenderModeInstant:
		evalParam := sqlb.Param{Name: "evaluation_ms", Type: "Int64", V: params.EvaluationTimeMS}
		valueExpr := sqlb.Call{Name: "if", Args: []sqlb.Expr{
			sqlb.RawLit{V: "count() = 1"},
			sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}},
			sqlb.RawLit{V: "nan"},
		}}
		return renderedFragment{
			Select: &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: emptyTags, Alias: "tags"},
					{Expr: sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{evalParam}}, Alias: "timestamp"},
					{Expr: valueExpr, Alias: "value"},
				},
				From: childSource,
			},
			ExtraParams: childParams,
		}, nil
	case native.RenderModeRange:
		startParam := sqlb.Param{Name: "start_ms", Type: "Int64", V: params.StartMS}
		endParam := sqlb.Param{Name: "end_ms", Type: "Int64", V: params.EndMS}
		stepParam := sqlb.Param{Name: "step_ms", Type: "Int64", V: params.StepMS}

		gridTimestampExpr := sqlb.Call{Name: "arrayJoin", Args: []sqlb.Expr{
			sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
				sqlb.Lambda{Params: []sqlb.Ident{"ts_ms"}, Body: sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{sqlb.Ident("ts_ms")}}},
				sqlb.Call{Name: "range", Args: []sqlb.Expr{startParam, sqlb.Binary{Op: "+", L: endParam, R: stepParam}, stepParam}},
			}},
		}}
		grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: gridTimestampExpr, Alias: "timestamp"}}}

		scalarValues := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"},
				{Expr: sqlb.Call{Name: "count"}, Alias: "sample_count"},
				{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.RawLit{V: "point.2"}}}, Alias: "any_value"},
			},
			From: sqlb.ArrayJoin{
				Base:  childSource,
				Expr:  sqlb.RawLit{V: "scalar_child.time_series"},
				Alias: "point",
			},
			GroupBy: []sqlb.Expr{sqlb.RawLit{V: "point.1"}},
		}

		middleValue := sqlb.Call{Name: "if", Args: []sqlb.Expr{
			sqlb.RawLit{V: "ifNull(scalar_values.sample_count, 0) = 1"},
			sqlb.RawLit{V: "scalar_values.any_value"},
			sqlb.RawLit{V: "nan"},
		}}
		middle := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.RawLit{V: "grid.timestamp"}, Alias: "timestamp"},
				{Expr: middleValue, Alias: "value"},
			},
			From: sqlb.Join{
				Kind:  "LEFT",
				Left:  sqlb.SubSelect{S: grid, Alias: "grid"},
				Right: sqlb.SubSelect{S: scalarValues, Alias: "scalar_values"},
				On:    sqlb.RawLit{V: "scalar_values.timestamp = grid.timestamp"},
			},
			OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("timestamp")}},
		}

		timeSeriesExpr := sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.RawLit{V: "item -> item.1"},
			sqlb.Call{Name: "groupArray", Args: []sqlb.Expr{sqlb.Tuple{Elems: []sqlb.Expr{sqlb.Ident("timestamp"), sqlb.Ident("value")}}}},
		}}
		return renderedFragment{
			Select: &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: emptyTags, Alias: "tags"},
					{Expr: timeSeriesExpr, Alias: "time_series"},
				},
				From: sqlb.SubSelect{S: middle},
			},
			ExtraParams: childParams,
		}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func renderAggregationSource(fragment *native.NativeFragment, params RenderParams) (storage.AggregationSource, error) {
	if fragment == nil {
		return storage.AggregationSource{}, fmt.Errorf("aggregation fragment is missing its source fragment")
	}
	switch fragment.Kind {
	case native.FragmentKindLeafSource, native.FragmentKindUnarySourceExpr, native.FragmentKindBinaryScalarSourceExpr:
		// Supported by the initial renderer skeleton.
	default:
		return storage.AggregationSource{}, fmt.Errorf("aggregation source fragment kind %q is not renderable yet", fragment.Kind)
	}
	if fragment.Selector != nil {
		return storage.AggregationSource{
			Selector: &storage.SelectorSource{
				Kind:              storage.SelectorKind(fragment.Selector.Kind),
				MetricName:        fragment.Selector.MetricName,
				Matchers:          selectorEffectiveMatchers(fragment.Selector),
				NeedTags:          selectorNeedsTags(fragment.Selector),
				RequireFullTags:   fragment.Selector.RequireFullTags,
				RequiredTagLabels: append([]string(nil), fragment.Selector.RequiredTagLabels...),
				LookbackMS:        fragment.Selector.Lookback.Milliseconds(),
				OffsetMS:          fragment.Selector.Offset.Milliseconds(),
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

func forceFragmentFullTags(fragment *native.NativeFragment) {
	if fragment == nil {
		return
	}
	if fragment.Selector != nil {
		fragment.Selector.RequireFullTags = true
	}
	if fragment.Aggregation != nil {
		forceFragmentFullTags(fragment.Aggregation.Source)
	}
	if fragment.RangeFunction != nil {
		forceFragmentFullTags(fragment.RangeFunction.Child)
	}
	if fragment.Subquery != nil {
		forceFragmentFullTags(fragment.Subquery.Child)
	}
	if fragment.ScalarConvert != nil {
		forceFragmentFullTags(fragment.ScalarConvert.Child)
	}
	if fragment.InfoJoin != nil {
		forceFragmentFullTags(fragment.InfoJoin.Child)
	}
	if fragment.BinaryJoin != nil {
		forceFragmentFullTags(fragment.BinaryJoin.LHS)
		forceFragmentFullTags(fragment.BinaryJoin.RHS)
	}
	if fragment.Absent != nil {
		forceFragmentFullTags(fragment.Absent.Child)
	}
	if fragment.HistogramProjection != nil {
		forceFragmentFullTags(fragment.HistogramProjection.Child)
	}
	if fragment.HistogramFunction != nil {
		forceFragmentFullTags(fragment.HistogramFunction.Child)
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			forceFragmentFullTags(quantile)
		}
	}
	if fragment.SortTransform != nil {
		forceFragmentFullTags(fragment.SortTransform.Child)
	}
	if fragment.LabelTransform != nil {
		forceFragmentFullTags(fragment.LabelTransform.Child)
	}
	if fragment.ClampTransform != nil {
		forceFragmentFullTags(fragment.ClampTransform.Child)
	}
	if fragment.ValueTransform != nil {
		forceFragmentFullTags(fragment.ValueTransform.Child)
	}
}
