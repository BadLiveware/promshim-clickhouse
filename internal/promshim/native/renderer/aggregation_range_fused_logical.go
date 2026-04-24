package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// tryRenderFusedRangeAggregationLogical is the Phase-6c direct-render
// counterpart of tryRenderFusedRangeAggregationFragment. It consumes a
// *logicalpkg.AggregationPlan and renders the fused `sum [by (...)]
// (range_function(selector|subquery))` SQL without materializing the
// aggregation Fragment at the lowerer boundary.
//
// Phase 6c (Task 13a Phase 6c): the two transitional RenderFragment calls
// that previously covered the leaf-selector-with-non-fast-path and the
// subquery-child-of-range-function branches now recurse via renderer.Lower
// on the equivalent logical nodes (the LeafExprPlan{MatrixSelector} leaf
// or the *SubqueryPlan grandchild). Aggregation fields (Op, Grouping,
// Without, ParamNumber, ParamString) are read directly from the logical
// plan; EmitZeroOnEmpty is read from the cached aggregation Fragment
// (populated during native.Analyze) because it is computed from a
// sum(... or vector(0)) structural pattern that does not have a
// standalone logical representation. The selector data for the leaf
// branches is read from the already-optimized cached Fragment so the
// selector projection applied during analysis (narrowing
// RequireFullTags / RequiredTagLabels for the grouping parent) flows
// through byte-identically with the Fragment path.
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
	if aggInfo == nil || aggInfo.Fragment == nil || aggInfo.Fragment.Aggregation == nil {
		return renderedFragment{}, false, nil
	}
	emitZeroOnEmpty := aggInfo.Fragment.Aggregation.EmitZeroOnEmpty

	sql, queryParams, err := renderFusedRangeAggregationLogicalSQL(ctx, n)
	if err != nil {
		return renderedFragment{}, false, err
	}
	if emitZeroOnEmpty {
		return wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, ctx.Params), true, nil
	}
	return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, true, nil
}

// renderFusedRangeAggregationLogicalSQL is the logical-plan sibling of
// renderFusedRangeAggregationSQL. It builds the row-level SQL via
// renderFusedRangeAggregationLogicalRowsSQL and wraps it with the
// standard matrix-subquery shell.
func renderFusedRangeAggregationLogicalSQL(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (string, map[string]string, error) {
	rowsSQL, rowParams, err := renderFusedRangeAggregationLogicalRowsSQL(ctx, n)
	if err != nil {
		return "", nil, err
	}
	return storage.BuildRangeRowsToMatrixSubquerySQL(rowsSQL, rowParams)
}

// renderFusedRangeAggregationLogicalRowsSQL is the logical-plan sibling
// of renderFusedRangeAggregationRowsSQL. It renders the inner range
// function over the aggregation's child via
// renderRangeFunctionRowsLogicalSQL and wraps the result with the
// aggregation subquery built from the AggregationPlan's Op, Grouping,
// Without, ParamNumber, and ParamString fields (all read directly off
// the logical plan — no Fragment materialization at this boundary).
func renderFusedRangeAggregationLogicalRowsSQL(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (string, map[string]string, error) {
	if !canFuseRangeAggregationLogicalDirect(n, ctx.Params) {
		return "", nil, fmt.Errorf("fused range aggregation rows (logical) require a supported aggregation plan")
	}
	rowsSQL, rowParams, err := renderRangeFunctionRowsLogicalSQL(ctx, n.Child)
	if err != nil {
		return "", nil, err
	}
	return storage.BuildRangeAggregationRowsSubquerySQL(rowsSQL, rowParams, n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString)
}

// renderRangeFunctionRowsLogicalSQL is the logical-plan sibling of
// renderRangeFunctionRowsSQL. It mirrors the four structural branches
// of the Fragment-side helper one-for-one:
//
//  1. leaf range-vector selector with func=="rate" +
//     preferDirectSelectorWindowJoin: direct aggregate rows path.
//  2. leaf range-vector selector + preferDirectSelectorWindowJoin:
//     direct window rows path.
//  3. leaf range-vector selector without direct-window fast path:
//     recurse into the leaf via renderer.Lower, then build windowed
//     arrays rows.
//  4. subquery child: recurse into the subquery via renderer.Lower,
//     then build windowed arrays rows.
//
// The first two branches reuse the cached Fragment's Selector (already
// narrowed by applySelectorProjection during native.Analyze) for byte
// identity with the Fragment-side renderAggregationSource call. The
// last two branches replace RenderFragment calls with Lower on the
// equivalent logical node (LeafExprPlan or SubqueryPlan).
//
// Phase 6c (Task 13a Phase 6c): branches 3 and 4 are the RenderFragment
// call sites retired in this phase.
func renderRangeFunctionRowsLogicalSQL(ctx LoweringCtx, rangeNode logicalpkg.Node) (string, map[string]string, error) {
	childNode, fn, ok := rangeFunctionChildNode(rangeNode)
	if !ok || childNode == nil {
		return "", nil, fmt.Errorf("range function row rendering (logical) requires a range-function plan")
	}
	paramNumber, paramNumbers := rangeFunctionParamNumbers(rangeNode)

	switch child := childNode.(type) {
	case *logicalpkg.LeafExprPlan:
		matrix, isMatrix := child.Expr.(*parser.MatrixSelector)
		if !isMatrix {
			return "", nil, fmt.Errorf("fused range aggregation (logical) requires a matrix-selector leaf child")
		}
		// Read the selector from the cached aggregation Fragment so the
		// already-applied projection narrowing (RequireFullTags /
		// RequiredTagLabels) flows through to the AggregationSource
		// byte-identically with the Fragment-side helper.
		selectorFragment, err := fusedRangeLeafSelectorFragment(ctx, rangeNode)
		if err != nil {
			return "", nil, err
		}
		if selectorFragment == nil || selectorFragment.Selector == nil {
			return "", nil, fmt.Errorf("fused range aggregation (logical) leaf selector fragment missing")
		}
		_ = matrix // matrix is the parser-side structural anchor; selector data comes from the fragment cache.

		lookbackMS := selectorFragment.Selector.Lookback.Milliseconds()
		offsetMS := selectorFragment.Selector.Offset.Milliseconds()
		if sourceWrapperIsIdentity(selectorFragment) && fn == "rate" && preferDirectSelectorWindowJoin(lookbackMS, ctx.Params.StepMS) {
			childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, ctx.Params.StartMS, ctx.Params.EndMS)
			source, err := renderAggregationSource(selectorFragment, ctx.Params)
			if err != nil {
				return "", nil, err
			}
			tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
			return storage.BuildRangeWindowSelectorDirectAggregateRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
		}
		if sourceWrapperIsIdentity(selectorFragment) && preferDirectSelectorWindowJoin(lookbackMS, ctx.Params.StepMS) {
			childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, ctx.Params.StartMS, ctx.Params.EndMS)
			source, err := renderAggregationSource(selectorFragment, ctx.Params)
			if err != nil {
				return "", nil, err
			}
			windowValueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", lookbackMS)
			tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
			return storage.BuildRangeWindowSelectorRowsQuerySQLWithFinalTags(ctx.Config, *source.Selector, childRequiredStartMS, childRequiredEndMS, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, fn, windowValueExpr, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
		}
		// Non-fast-path leaf branch: Phase-6c replacement for the
		// Fragment-side RenderFragment at line 92 of
		// renderRangeFunctionRowsSQL. Recurse into the leaf via
		// renderer.Lower so the leaf SQL is driven off the logical plan.
		childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, ctx.Params.StartMS, ctx.Params.EndMS)
		childCtx := ctx
		childCtx.Params = RenderParams{
			Mode:                native.RenderModeRange,
			StartMS:             ctx.Params.StartMS,
			EndMS:               ctx.Params.EndMS,
			StepMS:              ctx.Params.StepMS,
			RequiredStartMS:     childRequiredStartMS,
			RequiredEndMS:       childRequiredEndMS,
			ResolveSourcePromQL: ctx.Params.ResolveSourcePromQL,
		}
		childRendered, err := Lower(childCtx, child)
		if err != nil {
			return "", nil, err
		}
		tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
		sql, err := buildRangeFunctionOverWindowedArraysRowsSQL(trimRenderedQuerySQL(childRendered.SQL), fn, tagsExpr, paramNumber, paramNumbers, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, lookbackMS, offsetMS)
		if err != nil {
			return "", nil, err
		}
		return sql, childRendered.QueryParams, nil

	case *logicalpkg.SubqueryPlan:
		if child == nil || child.Child == nil {
			return "", nil, fmt.Errorf("fused range aggregation (logical) subquery child missing")
		}
		// Subquery branch: Phase-6c replacement for the Fragment-side
		// RenderFragment at line 104 of renderRangeFunctionRowsSQL.
		// Recurse into the subquery node via renderer.Lower.
		childCtx := ctx
		childCtx.Params = RenderParams{
			Mode:                native.RenderModeRange,
			StartMS:             ctx.Params.StartMS,
			EndMS:               ctx.Params.EndMS,
			StepMS:              ctx.Params.StepMS,
			RequiredStartMS:     ctx.Params.RequiredStartMS,
			RequiredEndMS:       ctx.Params.RequiredEndMS,
			ResolveSourcePromQL: ctx.Params.ResolveSourcePromQL,
		}
		childRendered, err := Lower(childCtx, child)
		if err != nil {
			return "", nil, err
		}
		sql, err := buildRangeFunctionOverWindowedArraysRowsSQL(trimRenderedQuerySQL(childRendered.SQL), fn, rangeFunctionTagsExpr(fn), paramNumber, paramNumbers, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, child.Range.Milliseconds(), child.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return sql, childRendered.QueryParams, nil

	default:
		return "", nil, fmt.Errorf("fused range aggregation (logical) currently requires a matrix-selector leaf child or subquery child")
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

// fusedRangeLeafSelectorFragment fetches the already-optimized
// leaf-selector Fragment reached from a fused-range aggregation shape.
// The caller has already verified that ctx.NativeAnalysis.InfoFor(agg)
// exists and that the shape is fusable; here we walk into the cached
// aggregation fragment to reach Aggregation.Source.RangeFunction.Child,
// which holds the leaf SelectorSource with applySelectorProjection-
// narrowed RequireFullTags / RequiredTagLabels.
//
// This is a transitional read of the cached Fragment. A future phase
// can replace it by re-deriving the narrowing on the logical side, at
// which point the cached aggregation Fragment is no longer consulted
// here.
func fusedRangeLeafSelectorFragment(ctx LoweringCtx, rangeNode logicalpkg.Node) (*native.NativeFragment, error) {
	if ctx.NativeAnalysis == nil {
		return nil, fmt.Errorf("fused range aggregation (logical) requires a native analysis")
	}
	info := ctx.NativeAnalysis.InfoFor(rangeNode)
	if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil || info.Fragment.RangeFunction.Child == nil {
		return nil, fmt.Errorf("fused range aggregation (logical) leaf selector fragment missing")
	}
	return info.Fragment.RangeFunction.Child, nil
}
