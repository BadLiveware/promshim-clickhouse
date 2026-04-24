package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// renderRangeFunctionLogical renders any of the seven logical node kinds that
// produce FragmentKindRangeFunction without constructing a top-level
// NativeFragment at the lowerer boundary. It is the public entry point for
// lower_range_function.go.
//
// Phase 6d (Task 13a Phase 6d): the scoped Fragment-builder call that
// previously materialized the range-function Fragment inside
// renderRangeFunctionLogicalDirect has been retired. The body now hands
// the node straight to renderRangeFunctionLogicalBody, which walks the
// logical plan directly and replaces the two Fragment-side RenderFragment
// calls (instant-mode fallback and range-mode selector/subquery fallbacks)
// with recursive renderer.Lower calls on the equivalent logical child.
// Because the body now recurses through Lower (which requires the logical
// Analysis), the helper takes a LoweringCtx rather than the narrower
// (cfg, analysis, params) bundle it accepted prior to Phase 6d.
//
// Hierarchical fallback: if the logical body encounters a node shape it
// cannot lower it returns errUnsupportedLowerNode so the caller falls back
// to the Fragment rendering path wholesale.
//
// The seven plan kinds covered here are RangeFunctionPlan, RatePlan,
// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan, and QuantileOverTimePlan.
func renderRangeFunctionLogical(ctx LoweringCtx, n logicalpkg.Node) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: range function requires a node")
	}
	return renderRangeFunctionLogicalDirect(ctx, n)
}

// renderRangeFunctionLogicalDirect is the Phase-6d direct-render counterpart
// of renderRangeFunctionFragment. It reads the range-function child and
// function identity from the logical plan via rangeFunctionChildNode and
// drives the SQL rendering from the logical tree through
// renderRangeFunctionLogicalBody — no BuildFragment call remains on this
// path.
//
// The fast-path branches that need SelectorSource data (for the
// BuildRangeMatrixSelectorRowsQuerySQL,
// BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags, and
// BuildRangeWindowSelectorQuerySQLWithFinalTags helpers) still consult the
// cached range-function Fragment for its applySelectorProjection-narrowed
// Selector, via rangeFunctionSelectorFragment. That cached read mirrors the
// same mechanism fusedRangeLeafSelectorFragment uses and retires when the
// selector-narrowing derivation moves fully onto the logical side.
func renderRangeFunctionLogicalDirect(ctx LoweringCtx, n logicalpkg.Node) (renderedFragment, error) {
	childNode, _, ok := rangeFunctionChildNode(n)
	if !ok || childNode == nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	return renderRangeFunctionLogicalBody(ctx, n)
}

// renderRangeFunctionLogicalBody is the logical-plan port of
// renderRangeFunctionFragment (range.go:75-245). It mirrors the Fragment-side
// branches one-for-one in both instant and range modes:
//
// Instant mode:
//  1. leaf range-vector selector + identity wrapper + fn=="rate":
//     BuildRangeMatrixSelectorRowsQuerySQL + buildInstantRateOverRowsSQL.
//  2. leaf range-vector selector + identity wrapper +
//     canUseInstantRangeFunctionRowsFastPath(fn):
//     BuildRangeMatrixSelectorRowsQuerySQL + buildInstantRangeFunctionOverRowsSQL.
//  3. subquery child + canUseInstantRangeFunctionRowsFastPath(fn):
//     tryRenderSubqueryRowsSource + buildInstantRangeFunctionOverRowsSQL.
//  4. fallback: Lower on the logical child + buildInstantRangeFunctionSQL.
//
// Range mode:
//  1. leaf range-vector selector + identity + canUseRangeFunctionRowsFastPath(fn):
//     BuildRangeMatrixSelectorRowsQuerySQL + buildRangeFunctionOverRowsSQL.
//  2. leaf range-vector selector + identity + avg_over_time +
//     preferDirectSelectorWindowJoin:
//     BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags.
//  3. leaf range-vector selector + identity + preferDirectSelectorWindowJoin:
//     BuildRangeWindowSelectorQuerySQLWithFinalTags.
//  4. leaf range-vector selector (catch-all): Lower on the leaf +
//     buildRangeFunctionOverWindowedArraysSQL.
//  5. subquery child: tryRenderSubqueryRowsSource fast path OR
//     Lower on the subquery + buildRangeFunctionOverWindowedArraysSQL.
//
// The fast-path branches that carry SelectorSource data consult the cached
// range-function Fragment's Child (via rangeFunctionSelectorFragment) so the
// applySelectorProjection narrowing applied during native.Analyze flows
// through byte-identically with the Fragment path. Selector-narrowing
// derivation moves fully onto the logical side in a later phase.
//
// Phase 6d (Task 13a Phase 6d): branches 4 (instant) and 4, 5-fallback
// (range) are the RenderFragment call sites retired in this phase — they
// now recurse via renderer.Lower on the equivalent logical child.
func renderRangeFunctionLogicalBody(ctx LoweringCtx, n logicalpkg.Node) (renderedFragment, error) {
	childNode, fn, ok := rangeFunctionChildNode(n)
	if !ok || childNode == nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	paramNumber, paramNumbers := rangeFunctionParamNumbers(n)
	params := ctx.Params
	cfg := ctx.Config

	switch params.Mode {
	case native.RenderModeInstant:
		switch child := childNode.(type) {
		case *logicalpkg.LeafExprPlan:
			if _, isMatrix := child.Expr.(*parser.MatrixSelector); isMatrix {
				selectorFragment, err := rangeFunctionSelectorFragment(ctx, n)
				if err != nil {
					return renderedFragment{}, err
				}
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && fn == "rate" {
					source, err := renderAggregationSource(selectorFragment, params)
					if err != nil {
						return renderedFragment{}, err
					}
					rowsSQL, rowParams, err := storage.BuildRangeMatrixSelectorRowsQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
					if err != nil {
						return renderedFragment{}, err
					}
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					sql, err := buildInstantRateOverRowsSQL(trimRenderedQuerySQL(rowsSQL), tagsExpr, params.EvaluationTimeMS)
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: rowParams}, nil
				}
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && canUseInstantRangeFunctionRowsFastPath(fn) {
					source, err := renderAggregationSource(selectorFragment, params)
					if err != nil {
						return renderedFragment{}, err
					}
					rowsSQL, rowParams, err := storage.BuildRangeMatrixSelectorRowsQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
					if err != nil {
						return renderedFragment{}, err
					}
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					sql, err := buildInstantRangeFunctionOverRowsSQL(trimRenderedQuerySQL(rowsSQL), fn, tagsExpr, params.EvaluationTimeMS)
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: rowParams}, nil
				}
			}
		case *logicalpkg.SubqueryPlan:
			if child != nil && child.Child != nil && canUseInstantRangeFunctionRowsFastPath(fn) {
				subqueryFragment, err := rangeFunctionSelectorFragment(ctx, n)
				if err != nil {
					return renderedFragment{}, err
				}
				if subqueryFragment != nil && subqueryFragment.Kind == native.FragmentKindSubquery && subqueryFragment.Subquery != nil && subqueryFragment.Subquery.Child != nil {
					if childRowsSQL, childParams, ok, err := tryRenderSubqueryRowsSource(cfg, subqueryFragment.Subquery, params); err != nil {
						return renderedFragment{}, err
					} else if ok {
						sql, err := buildInstantRangeFunctionOverRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, subqueryRowsOutputTagsExpr(subqueryFragment.Subquery, fn), params.EvaluationTimeMS)
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
					}
				}
			}
		}
		// Instant-mode fallback (Phase-6d retirement of the Fragment-side
		// RenderFragment at range.go:124). Lower the logical child directly
		// so the SQL is driven off the logical tree. The tags expression
		// still prefers the narrowed selector data when available, matching
		// the Fragment-side behavior on leaf children.
		childCtx := ctx
		childRendered, err := Lower(childCtx, childNode)
		if err != nil {
			return renderedFragment{}, err
		}
		tagsExpr := rangeFunctionTagsExpr(fn)
		childRangeMS := int64(0)
		if leaf, isLeaf := childNode.(*logicalpkg.LeafExprPlan); isLeaf {
			if _, isMatrix := leaf.Expr.(*parser.MatrixSelector); isMatrix {
				selectorFragment, selErr := rangeFunctionSelectorFragment(ctx, n)
				if selErr != nil {
					return renderedFragment{}, selErr
				}
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil {
					tagsExpr = rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					if selectorFragment.Selector.Kind == native.SelectorKindRangeVector {
						childRangeMS = selectorFragment.Selector.Lookback.Milliseconds()
					}
				}
			}
		} else if sub, isSub := childNode.(*logicalpkg.SubqueryPlan); isSub && sub != nil {
			childRangeMS = sub.Range.Milliseconds()
		}
		sql, err := buildInstantRangeFunctionSQL(childRendered.SQL, fn, tagsExpr, paramNumber, paramNumbers, params.EvaluationTimeMS, childRangeMS)
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil

	case native.RenderModeRange:
		switch child := childNode.(type) {
		case *logicalpkg.LeafExprPlan:
			if _, isMatrix := child.Expr.(*parser.MatrixSelector); isMatrix {
				selectorFragment, err := rangeFunctionSelectorFragment(ctx, n)
				if err != nil {
					return renderedFragment{}, err
				}
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && canUseRangeFunctionRowsFastPath(fn) {
					childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
					source, err := renderAggregationSource(selectorFragment, params)
					if err != nil {
						return renderedFragment{}, err
					}
					rowsSQL, rowParams, err := storage.BuildRangeMatrixSelectorRowsQuerySQL(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS)
					if err != nil {
						return renderedFragment{}, err
					}
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					sql, err := buildRangeFunctionOverRowsSQL(trimRenderedQuerySQL(rowsSQL), fn, tagsExpr, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Selector.Lookback.Milliseconds(), selectorFragment.Selector.Offset.Milliseconds())
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: rowParams}, nil
				}
				// Keep the selector-scoped grid→data join that benchmarks well on long-range
				// windows, but skip per-step window_series/window_values materialization for
				// direct avg_over_time selectors by aggregating inside that grouped join.
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && fn == "avg_over_time" && preferDirectSelectorWindowJoin(selectorFragment.Selector.Lookback.Milliseconds(), params.StepMS) {
					childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
					source, err := renderAggregationSource(selectorFragment, params)
					if err != nil {
						return renderedFragment{}, err
					}
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					sql, queryParams, err := storage.BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fn, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
				}
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector && sourceWrapperIsIdentity(selectorFragment) && preferDirectSelectorWindowJoin(selectorFragment.Selector.Lookback.Milliseconds(), params.StepMS) {
					childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
					source, err := renderAggregationSource(selectorFragment, params)
					if err != nil {
						return renderedFragment{}, err
					}
					windowValueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", selectorFragment.Selector.Lookback.Milliseconds())
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					sql, queryParams, err := storage.BuildRangeWindowSelectorQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fn, windowValueExpr, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
				}
				if selectorFragment != nil && selectorFragment.Kind == native.FragmentKindLeafSource && selectorFragment.Selector != nil && selectorFragment.Selector.Kind == native.SelectorKindRangeVector {
					// Range-mode leaf catch-all (Phase-6d retirement of the
					// Fragment-side RenderFragment at range.go:188). Lower the
					// logical leaf directly with a RangeMode child context
					// widened by the selector's lookback/offset.
					childRequiredStartMS, childRequiredEndMS := rangeRequiredBoundsForChild(selectorFragment, params.StartMS, params.EndMS)
					childCtx := ctx
					childCtx.Params = RenderParams{
						Mode:                native.RenderModeRange,
						StartMS:             params.StartMS,
						EndMS:               params.EndMS,
						StepMS:              params.StepMS,
						RequiredStartMS:     childRequiredStartMS,
						RequiredEndMS:       childRequiredEndMS,
						ResolveSourcePromQL: params.ResolveSourcePromQL,
					}
					childRendered, err := Lower(childCtx, child)
					if err != nil {
						return renderedFragment{}, err
					}
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(selectorFragment.Selector))
					sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fn, tagsExpr, paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, selectorFragment.Selector.Lookback.Milliseconds(), selectorFragment.Selector.Offset.Milliseconds())
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
				}
			}
		case *logicalpkg.SubqueryPlan:
			if child != nil && child.Child != nil {
				// Subquery-child fast path first: reuse tryRenderSubqueryRowsSource
				// against the cached subquery fragment (same as Phase 6c).
				subqueryFragment, err := rangeFunctionSelectorFragment(ctx, n)
				if err != nil {
					return renderedFragment{}, err
				}
				if subqueryFragment != nil && subqueryFragment.Kind == native.FragmentKindSubquery && subqueryFragment.Subquery != nil && subqueryFragment.Subquery.Child != nil {
					if childRowsSQL, childParams, ok, err := tryRenderSubqueryRowsSource(cfg, subqueryFragment.Subquery, params); err != nil {
						return renderedFragment{}, err
					} else if ok {
						var sql string
						childTagsExpr := subqueryRowsOutputTagsExpr(subqueryFragment.Subquery, fn)
						if canUseRangeFunctionRowsFastPath(fn) {
							sql, err = buildRangeFunctionOverRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, childTagsExpr, params.StartMS, params.EndMS, params.StepMS, subqueryFragment.Subquery.Range.Milliseconds(), subqueryFragment.Subquery.Offset.Milliseconds())
						} else {
							sql, err = buildRangeFunctionOverWindowedRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, subqueryFragment.Subquery.Range.Milliseconds(), subqueryFragment.Subquery.Offset.Milliseconds())
						}
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
					}
				}
				// Subquery fallback (Phase-6d retirement of the Fragment-side
				// RenderFragment at range.go:223). Lower the logical subquery
				// node directly; lowerSubquery carves out the child step-grid
				// over the outer range envelope.
				childCtx := ctx
				childCtx.Params = RenderParams{
					Mode:                native.RenderModeRange,
					StartMS:             params.StartMS,
					EndMS:               params.EndMS,
					StepMS:              params.StepMS,
					RequiredStartMS:     params.RequiredStartMS,
					RequiredEndMS:       params.RequiredEndMS,
					ResolveSourcePromQL: params.ResolveSourcePromQL,
				}
				childRendered, err := Lower(childCtx, child)
				if err != nil {
					return renderedFragment{}, err
				}
				sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fn, rangeFunctionTagsExpr(fn), paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, child.Range.Milliseconds(), child.Offset.Milliseconds())
				if err != nil {
					return renderedFragment{}, err
				}
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
			}
		}
		return renderedFragment{}, fmt.Errorf("native range-mode rendering for %s currently requires a direct range-vector selector child or supported subquery child", fn)

	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

// rangeFunctionSelectorFragment fetches the already-optimized range-function
// child Fragment (the selector or subquery fragment) from the cached
// native.Analysis. Fast-path branches of renderRangeFunctionLogicalBody need
// the narrowed SelectorSource data (RequireFullTags / RequiredTagLabels)
// populated by applySelectorProjection during native.Analyze; that
// narrowing doesn't have a standalone logical representation yet, so we
// read the cached Fragment directly — the same mechanism
// fusedRangeLeafSelectorFragment uses.
//
// This is a transitional read of the cached Fragment. A future phase can
// replace it by re-deriving the narrowing on the logical side, at which
// point the cached range-function Fragment is no longer consulted here.
func rangeFunctionSelectorFragment(ctx LoweringCtx, rangeNode logicalpkg.Node) (*native.NativeFragment, error) {
	if ctx.NativeAnalysis == nil {
		return nil, fmt.Errorf("range function (logical) requires a native analysis")
	}
	info := ctx.NativeAnalysis.InfoFor(rangeNode)
	if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
		return nil, nil
	}
	return info.Fragment.RangeFunction.Child, nil
}

// rangeFunctionChildNode returns the child logical node and the range-function
// name for any of the seven range-function plan kinds. Returns ok=false for
// unrelated nodes.
//
// The function name matches the PromQL builtin name the logical plan was
// synthesized from (e.g. "rate", "increase", "avg_over_time",
// "quantile_over_time", "predict_linear"). It mirrors the fragment-side
// RangeFunction.Func field, so downstream consumers that previously read
// fragment.RangeFunction.Func can switch to this helper.
func rangeFunctionChildNode(n logicalpkg.Node) (logicalpkg.Node, string, bool) {
	switch r := n.(type) {
	case *logicalpkg.RangeFunctionPlan:
		return r.Child, r.Func, true
	case *logicalpkg.RatePlan:
		return r.Child, r.Func, true
	case *logicalpkg.IncreasePlan:
		return r.Child, "increase", true
	case *logicalpkg.DeltaPlan:
		return r.Child, r.Func, true
	case *logicalpkg.ChangesPlan:
		return r.Child, "changes", true
	case *logicalpkg.DerivPlan:
		return r.Child, "deriv", true
	case *logicalpkg.QuantileOverTimePlan:
		return r.Child, "quantile_over_time", true
	default:
		return nil, "", false
	}
}

// canFuseRangeAggregationLogical reports whether a logical AggregationPlan
// can be fused with its range-function child into a single SQL pass. It is
// the logical-plan analog of canFuseRangeAggregationFragment and is
// consumed by the AggregationPlan lowerer (Phase 4).
//
// Phase 3 (Task 13a Phase 3): the helper now walks the logical tree
// directly — no BuildFragment call is needed. The capability check mirrors
// the Fragment-side shape discrimination one-for-one:
//
//   - range-mode only (params.Mode == RenderModeRange),
//   - the aggregation is a SUM (agg.Op == parser.SUM),
//   - the aggregation's child is one of the seven range-function plan
//     kinds (equivalent to fragment.Aggregation.Source.Kind ==
//     FragmentKindRangeFunction),
//   - that range-function's child is either a LeafExprPlan wrapping a
//     parser.MatrixSelector (equivalent to FragmentKindLeafSource with
//     SelectorKindRangeVector) or a *SubqueryPlan with a non-nil Child
//     (equivalent to FragmentKindSubquery with a child).
//
// The function name identity and shape of the range-function child match
// what canFuseRangeAggregationFragment checks via fragment.Kind and
// fragment.Selector.Kind. Non-matching nodes return false, falling through
// to the non-fused aggregation rendering path.
func canFuseRangeAggregationLogical(agg *logicalpkg.AggregationPlan, params RenderParams) bool {
	return canFuseRangeAggregationLogicalDirect(agg, params)
}

// canFuseRangeAggregationLogicalDirect is the Phase-3 direct-render
// counterpart of canFuseRangeAggregationFragment. It is a pure logical-plan
// shape predicate: no Fragment materialization, no analysis side-info. The
// check is equivalent to the Fragment-side test — see the doc comment on
// canFuseRangeAggregationLogical above for the one-for-one mapping.
func canFuseRangeAggregationLogicalDirect(agg *logicalpkg.AggregationPlan, params RenderParams) bool {
	if params.Mode != native.RenderModeRange || agg == nil || agg.Child == nil {
		return false
	}
	if agg.Op != parser.SUM {
		return false
	}
	rangeChild, _, ok := rangeFunctionChildNode(agg.Child)
	if !ok || rangeChild == nil {
		return false
	}
	// Leaf range-vector selector: LeafExprPlan wrapping MatrixSelector.
	if leaf, ok := rangeChild.(*logicalpkg.LeafExprPlan); ok {
		if _, isMatrix := leaf.Expr.(*parser.MatrixSelector); isMatrix {
			return true
		}
		return false
	}
	// Subquery child with a non-nil inner child.
	if sub, ok := rangeChild.(*logicalpkg.SubqueryPlan); ok && sub != nil && sub.Child != nil {
		return true
	}
	return false
}

// tryRenderFusedRangeAggregationLogicalDirect is the Phase-6c direct-render
// counterpart of tryRenderFusedRangeAggregationFragment. It consumes the
// logical AggregationPlan without materializing the aggregation Fragment at
// the lowerer boundary: the capability check is pure-logical
// (canFuseRangeAggregationLogicalDirect) and the SQL synthesis runs entirely
// on the logical plan through tryRenderFusedRangeAggregationLogical.
//
// Phase 6c (Task 13a Phase 6c): the transitional BuildFragment call that
// previously re-materialized the AggregationPlan Fragment here and
// delegated to tryRenderFusedRangeAggregationFragment has retired. The
// body now just packages the (config, analyses, params) bundle into a
// LoweringCtx and hands it to the logical-port helper.
//
// The public signature is stable so the existing
// TestFusedRangeAggregationLogicalMatchesFragment byte-equality guard
// keeps calling this function unchanged.
func tryRenderFusedRangeAggregationLogicalDirect(cfg storage.QueryConfig, agg *logicalpkg.AggregationPlan, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, params RenderParams) (renderedFragment, bool, error) {
	ctx := LoweringCtx{
		Config:         cfg,
		Analysis:       logicalAnalysis,
		NativeAnalysis: analysis,
		Params:         params,
	}
	return tryRenderFusedRangeAggregationLogical(ctx, agg)
}
