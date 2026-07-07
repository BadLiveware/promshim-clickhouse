package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// tryRenderFusedRangeAggregationLogical consumes a
// *logicalpkg.AggregationPlan and renders the fused `sum [by (...)]
// (range_function(selector|subquery))` SQL directly off the logical
// tree.
//
// Aggregation fields (Op, Grouping, Without, ParamNumber, ParamString)
// are read directly from the logical plan; EmitZeroOnEmpty is read from
// the cached AggregationSupport side-map (populated during
// native.Analyze) because it is computed from a sum(... or vector(0))
// structural pattern that does not have a standalone logical
// representation. The selector data for the leaf branches is read from
// info.SourceExpr; tag-narrowing for the grouping parent flows through
// RenderParams (RequireFullTags / RequiredTagLabels) and is merged onto
// the storage selector inside renderAggregationSourceView via
// applyRenderParamsNarrowing.
//
// Hierarchical fallback: non-fusable shapes return ok=false so the
// caller falls through to the standard (non-fused) aggregation
// rendering path. The fusion capability check itself is pure-logical
// (canFuseRangeAggregationLogicalDirect).
func tryRenderFusedRangeAggregationLogical(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (renderedFragment, bool, error) {
	if !canFuseRangeAggregationLogicalDirect(n, ctx.Params) {
		return renderedFragment{}, false, nil
	}
	if ctx.NativeAnalysis == nil {
		return renderedFragment{}, false, fmt.Errorf("fused range aggregation (logical) requires a native analysis")
	}
	aggInfo := ctx.NativeAnalysis.InfoFor(n)
	if aggInfo == nil || aggInfo.Aggregation == nil {
		return renderedFragment{}, false, nil
	}
	emitZeroOnEmpty := aggInfo.Aggregation.EmitZeroOnEmpty

	childCtx := ctx
	childCtx.Params = aggregationChildRenderParams(n, ctx.Params)
	sql, queryParams, decisions, err := renderFusedRangeAggregationLogicalSQL(childCtx, n)
	if err != nil {
		return renderedFragment{}, false, err
	}
	settings, settingDecisions := fusedRateAggregationThreadSettings(ctx.Params, n)
	if emitZeroOnEmpty {
		rendered := wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, ctx.Params)
		rendered.ExtraSettings = settings
		rendered.ExtraPhysicalDecisions = appendRenderedQueryPhysicalDecisions(rendered.ExtraPhysicalDecisions, decisions...)
		rendered.ExtraPhysicalDecisions = appendRenderedQueryPhysicalDecisions(rendered.ExtraPhysicalDecisions, settingDecisions...)
		return rendered, true, nil
	}
	allDecisions := appendRenderedQueryPhysicalDecisions(nil, decisions...)
	allDecisions = appendRenderedQueryPhysicalDecisions(allDecisions, settingDecisions...)
	return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams, ExtraSettings: settings, ExtraPhysicalDecisions: allDecisions}, true, nil
}

// renderFusedRangeAggregationLogicalSQL builds the row-level SQL via
// renderFusedRangeAggregationLogicalRowsSQL and wraps it with the
// standard matrix-subquery shell.
func renderFusedRangeAggregationLogicalSQL(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (string, map[string]string, []physical.Decision, error) {
	if sql, queryParams, decisions, ok, err := tryRenderNativeGridRangeSumAggregationSQL(ctx, n); err != nil {
		return "", nil, nil, err
	} else if ok {
		return sql, queryParams, decisions, nil
	}
	rowsSQL, rowParams, decisions, err := renderFusedRangeAggregationLogicalRowsSQL(ctx, n)
	if err != nil {
		return "", nil, nil, err
	}
	sql, queryParams, err := storage.BuildRangeRowsToMatrixSubquerySQL(rowsSQL, rowParams)
	return sql, queryParams, decisions, err
}

func tryRenderNativeGridRangeSumAggregationSQL(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (string, map[string]string, []physical.Decision, bool, error) {
	if n == nil || n.Op != parser.SUM || !ctx.Config.EnableNativeGridFunctions {
		return "", nil, nil, false, nil
	}
	childNode, fn, ok := rangeFunctionChildNode(n.Child)
	if !ok || childNode == nil {
		return "", nil, nil, false, nil
	}
	child, ok := childNode.(*logicalpkg.LeafExprPlan)
	if !ok {
		return "", nil, nil, false, nil
	}
	if _, isMatrix := child.Expr.(*parser.MatrixSelector); !isMatrix {
		return "", nil, nil, false, nil
	}
	leafInfo := ctx.NativeAnalysis.InfoFor(child)
	if leafInfo == nil || leafInfo.LeafSelector == nil || leafInfo.SourceExpr == nil {
		return "", nil, nil, false, fmt.Errorf("native-grid range sum aggregation leaf selector metadata missing")
	}
	view := leafInfo.SourceExpr
	sel := leafInfo.LeafSelector
	lookbackMS := sel.Lookback.Milliseconds()
	offsetMS := sel.Offset.Milliseconds()
	isIdentity := view.ValueExpr == "{value}" && view.TagsExpr == "{tags}" && !view.DropsMetric
	decision := physical.ChooseFusedRangeAggregation(physical.FusedRangeAggregationInput{
		IsSumAggregation:          n.Op == parser.SUM,
		EnableNativeGridFunctions: ctx.Config.EnableNativeGridFunctions,
		IsMatrixSelectorLeaf:      true,
		IsIdentitySelectorInput:   isIdentity,
		Func:                      fn,
		LookbackMS:                lookbackMS,
		OffsetMS:                  offsetMS,
		StepMS:                    ctx.Params.StepMS,
	})
	if decision.Strategy != physical.FusedRangeAggregationStrategyNativeGridSumAggregation {
		return "", nil, nil, false, nil
	}
	childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
	source, err := renderAggregationSourceView(view, ctx.Params)
	if err != nil {
		return "", nil, nil, false, err
	}
	tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
	sql, queryParams, err := storage.BuildRangeNativeGridSelectorSumAggregationQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, tagsExpr, n.Grouping, n.Without)
	if err != nil {
		return "", nil, nil, false, err
	}
	return sql, queryParams, []physical.Decision{decision.Explain("fused_range_aggregation")}, true, nil
}

// renderFusedRangeAggregationLogicalRowsSQL renders the inner range
// function over the aggregation's child via
// renderRangeFunctionRowsLogicalSQL and wraps the result with the
// aggregation subquery built from the AggregationPlan's Op, Grouping,
// Without, ParamNumber, and ParamString fields (all read directly off
// the logical plan). Callers pass Params already adjusted for the
// aggregation child so label projection is applied exactly once.
func renderFusedRangeAggregationLogicalRowsSQL(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (string, map[string]string, []physical.Decision, error) {
	if !canFuseRangeAggregationLogicalDirect(n, ctx.Params) {
		return "", nil, nil, fmt.Errorf("fused range aggregation rows (logical) require a supported aggregation plan")
	}
	rowsSQL, rowParams, decisions, err := renderRangeFunctionRowsLogicalSQL(ctx, n.Child)
	if err != nil {
		return "", nil, nil, err
	}
	sql, queryParams, err := storage.BuildRangeAggregationRowsSubquerySQL(rowsSQL, rowParams, n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString)
	return sql, queryParams, decisions, err
}

// renderRangeFunctionRowsLogicalSQL renders the inner range function
// rows over four structural branches:
//
//  1. leaf range-vector selector with func=="rate" +
//     sparse direct-rate strategy: direct aggregate rows path.
//  2. leaf range-vector selector + direct window-join strategy:
//     direct window rows path.
//  3. leaf range-vector selector without direct-window fast path:
//     recurse into the leaf via renderer.Lower, then build windowed
//     arrays rows.
//  4. subquery child: recurse into the subquery via renderer.Lower,
//     then build windowed arrays rows.
//
// The first two branches reuse the leaf's cached Selector (already
// narrowed by applySelectorProjection during native.Analyze). The last
// two branches recurse via Lower on the equivalent logical node
// (LeafExprPlan or SubqueryPlan).
func canUseHistogramNativeGridLateTags(params RenderParams, fn string) bool {
	if !params.HistogramPreparation || params.Mode != native.RenderModeRange || fn != "rate" {
		return false
	}
	return len(params.RequiredTagLabels) == 1 && params.RequiredTagLabels[0] == "le" && !params.RequireFullTags
}

func renderRangeFunctionRowsLogicalSQL(ctx LoweringCtx, rangeNode logicalpkg.Node) (string, map[string]string, []physical.Decision, error) {
	childNode, fn, ok := rangeFunctionChildNode(rangeNode)
	if !ok || childNode == nil {
		return "", nil, nil, fmt.Errorf("range function row rendering (logical) requires a range-function plan")
	}
	paramNumber, paramNumbers := rangeFunctionParamNumbers(rangeNode)

	switch child := childNode.(type) {
	case *logicalpkg.LeafExprPlan:
		_, isMatrix := child.Expr.(*parser.MatrixSelector)
		if !isMatrix {
			return "", nil, nil, fmt.Errorf("fused range aggregation (logical) requires a matrix-selector leaf child")
		}
		// Read the leaf's SourceExprView / LeafSelector off the analysis
		// side-map. Tag-narrowing flows through RenderParams and is
		// merged onto the storage AggregationSource selector inside
		// renderAggregationSourceView. Both fields are populated by
		// native.Analyze for the leaf node.
		leafInfo := ctx.NativeAnalysis.InfoFor(child)
		if leafInfo == nil || leafInfo.LeafSelector == nil || leafInfo.SourceExpr == nil {
			return "", nil, nil, fmt.Errorf("fused range aggregation (logical) leaf selector metadata missing")
		}
		view := leafInfo.SourceExpr
		sel := leafInfo.LeafSelector
		lookbackMS := sel.Lookback.Milliseconds()
		offsetMS := sel.Offset.Milliseconds()
		isIdentity := view.ValueExpr == "{value}" && view.TagsExpr == "{tags}" && !view.DropsMetric
		rowsDecision := physical.ChooseRangeFunctionRows(physical.RangeFunctionRowsInput{
			EnableNativeGridFunctions: ctx.Config.EnableNativeGridFunctions,
			IsIdentitySelectorInput:   isIdentity,
			Func:                      fn,
			LookbackMS:                lookbackMS,
			OffsetMS:                  offsetMS,
			StepMS:                    ctx.Params.StepMS,
		})
		switch rowsDecision.Strategy {
		case physical.RangeFunctionRowsStrategyNativeGridRows:
			childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
			source, err := renderAggregationSourceView(view, ctx.Params)
			if err != nil {
				return "", nil, nil, err
			}
			tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
			if canUseHistogramNativeGridLateTags(ctx.Params, fn) {
				sql, queryParams, err := storage.BuildHistogramRangeNativeGridSelectorRowsLateTagsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, tagsExpr)
				return sql, queryParams, []physical.Decision{rowsDecision.Explain("range_function_rows"), physical.HistogramNativeGridRowsDecision()}, err
			}
			sql, queryParams, err := storage.BuildRangeNativeGridSelectorRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, tagsExpr)
			return sql, queryParams, []physical.Decision{rowsDecision.Explain("range_function_rows")}, err
		case physical.RangeFunctionRowsStrategySparseDirectRateAggregation:
			childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
			source, err := renderAggregationSourceView(view, ctx.Params)
			if err != nil {
				return "", nil, nil, err
			}
			tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
			sql, queryParams, err := storage.BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
			return sql, queryParams, []physical.Decision{rowsDecision.Explain("range_function_rows")}, err
		case physical.RangeFunctionRowsStrategyRangeWindowAggregate:
			decision := physical.ChooseRangeWindowAggregate(physical.RangeWindowAggregateInput{
				Func:                        fn,
				LookbackMS:                  lookbackMS,
				OffsetMS:                    offsetMS,
				StepMS:                      ctx.Params.StepMS,
				EnableCumulativeAvgOverTime: ctx.Config.EnableCumulativeAvgOverTime,
				Preferences:                 ctx.Params.Physical,
			})
			switch decision.Strategy {
			case physical.RangeWindowAggregateStrategyCumulativeAvg:
				childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
				source, err := renderAggregationSourceView(view, ctx.Params)
				if err != nil {
					return "", nil, nil, err
				}
				tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
				sql, queryParams, err := storage.BuildRangeWindowSelectorCumulativeAvgRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
				return sql, queryParams, []physical.Decision{decision.Explain("range_window_aggregate")}, err
			case physical.RangeWindowAggregateStrategyDirectAggregate, physical.RangeWindowAggregateStrategySparseDirectAggregate:
				childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
				source, err := renderAggregationSourceView(view, ctx.Params)
				if err != nil {
					return "", nil, nil, err
				}
				tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
				sql, queryParams, err := storage.BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
				return sql, queryParams, []physical.Decision{decision.Explain("range_window_aggregate")}, err
			case physical.RangeWindowAggregateStrategyWindowJoin:
				childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
				source, err := renderAggregationSourceView(view, ctx.Params)
				if err != nil {
					return "", nil, nil, err
				}
				windowValueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", rangeExtrapolationAnchorExpr(offsetMS), lookbackMS)
				tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
				sql, queryParams, err := storage.BuildRangeWindowSelectorRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, windowValueExpr, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
				return sql, queryParams, []physical.Decision{decision.Explain("range_window_aggregate")}, err
			}
		}
		// Non-fast-path leaf branch: recurse into the leaf via
		// renderer.Lower so the leaf SQL is driven off the logical plan.
		childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, ctx.Params.StartMS, ctx.Params.EndMS)
		childCtx := ctx
		childCtx.Params = RenderParams{
			Mode:                 native.RenderModeRange,
			StartMS:              ctx.Params.StartMS,
			EndMS:                ctx.Params.EndMS,
			StepMS:               ctx.Params.StepMS,
			RequiredStartMS:      childRequiredStartMS,
			RequiredEndMS:        childRequiredEndMS,
			ResolveSourcePromQL:  ctx.Params.ResolveSourcePromQL,
			RequireFullTags:      ctx.Params.RequireFullTags,
			RequiredTagLabels:    ctx.Params.RequiredTagLabels,
			HistogramPreparation: ctx.Params.HistogramPreparation,
			Physical:             preferRangeInstantSelectorStrategy(ctx.Params.Physical, storage.RangeInstantSelectorStrategyBucketedArgMax),
		}
		childRendered, err := Lower(childCtx, child)
		if err != nil {
			return "", nil, nil, err
		}
		tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(ctx.Params))
		sql, err := buildRangeFunctionOverWindowedArraysRowsSQL(trimRenderedQuerySQL(childRendered.SQL), fn, tagsExpr, paramNumber, paramNumbers, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, lookbackMS, offsetMS)
		if err != nil {
			return "", nil, nil, err
		}
		return sql, childRendered.QueryParams, childRendered.PhysicalDecisions, nil

	case *logicalpkg.SubqueryPlan:
		if child == nil || child.Child == nil {
			return "", nil, nil, fmt.Errorf("fused range aggregation (logical) subquery child missing")
		}
		// Subquery branch: recurse into the subquery node via renderer.Lower.
		childCtx := ctx
		childCtx.Params = RenderParams{
			Mode:                 native.RenderModeRange,
			StartMS:              ctx.Params.StartMS,
			EndMS:                ctx.Params.EndMS,
			StepMS:               ctx.Params.StepMS,
			RequiredStartMS:      ctx.Params.RequiredStartMS,
			RequiredEndMS:        ctx.Params.RequiredEndMS,
			ResolveSourcePromQL:  ctx.Params.ResolveSourcePromQL,
			RequireFullTags:      ctx.Params.RequireFullTags,
			RequiredTagLabels:    ctx.Params.RequiredTagLabels,
			HistogramPreparation: ctx.Params.HistogramPreparation,
			Physical:             preferRangeInstantSelectorStrategy(ctx.Params.Physical, storage.RangeInstantSelectorStrategyBucketedArgMax),
		}
		childRendered, err := Lower(childCtx, child)
		if err != nil {
			return "", nil, nil, err
		}
		sql, err := buildRangeFunctionOverWindowedArraysRowsSQL(trimRenderedQuerySQL(childRendered.SQL), fn, rangeFunctionTagsExpr(fn), paramNumber, paramNumbers, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, child.Range.Milliseconds(), child.Offset.Milliseconds())
		if err != nil {
			return "", nil, nil, err
		}
		return sql, childRendered.QueryParams, childRendered.PhysicalDecisions, nil

	default:
		return "", nil, nil, fmt.Errorf("fused range aggregation (logical) currently requires a matrix-selector leaf child or subquery child")
	}
}

// rangeFunctionParamNumbers extracts the (ParamNumber, ParamNumbers) pair
// from any of the seven range-function plan kinds. The values mirror the
// RangeFunctionFragment fields populated during native.Analyze.
// RatePlan, IncreasePlan, DeltaPlan, ChangesPlan, and DerivPlan carry no
// scalar parameters (both return nil). QuantileOverTimePlan carries a
// single quantile via ParamNumber. RangeFunctionPlan carries the generic
// scalar-parameter form for predict_linear, resets, holt_winters, and
// the *_over_time aggregates.
func rangeFunctionParamNumbers(rangeNode logicalpkg.Node) (*float64, []*float64) {
	switch r := rangeNode.(type) {
	case *logicalpkg.RangeFunctionPlan:
		return r.ParamNumber, r.ParamNumbers
	case *logicalpkg.QuantileOverTimePlan:
		q := r.Quantile
		return &q, nil
	default:
		return nil, nil
	}
}
