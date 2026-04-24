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
// BuildRangeWindowSelectorQuerySQLWithFinalTags helpers) read the
// applySelectorProjection-narrowed Selector off the leaf's LoweringInfo
// (LeafSelector + SourceExprView) directly. Subquery-child fast paths
// read the cached SubqueryFragment off the range-function node's own
// LoweringInfo.RangeFunctionSubquery (Task 13c-7b).
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
// The fast-path branches that carry SelectorSource data read the
// applySelectorProjection-narrowed Selector off the leaf's LoweringInfo
// (LeafSelector + SourceExprView) directly. Subquery-child fast paths
// read the cached SubqueryFragment off LoweringInfo.RangeFunctionSubquery
// — populated during native.Analyze at each of the seven range-function
// emission sites. No info.Fragment dereference remains on this path.
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
				// Pure-logical read: the leaf's LoweringInfo carries
				// LeafSelector (applySelectorProjection-narrowed) and
				// SourceExpr (identity wrapper for LeafSource/range-vector).
				leafInfo := ctx.NativeAnalysis.InfoFor(child)
				if leafInfo != nil && leafInfo.LeafSelector != nil && leafInfo.SourceExpr != nil &&
					leafInfo.LeafSelector.Kind == native.SelectorKindRangeVector &&
					leafInfo.SourceExpr.ValueExpr == "{value}" && leafInfo.SourceExpr.TagsExpr == "{tags}" && !leafInfo.SourceExpr.DropsMetric {
					if fn == "rate" {
						source, err := renderAggregationSourceView(leafInfo.SourceExpr, params)
						if err != nil {
							return renderedFragment{}, err
						}
						rowsSQL, rowParams, err := storage.BuildRangeMatrixSelectorRowsQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
						if err != nil {
							return renderedFragment{}, err
						}
						tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(leafInfo.LeafSelector))
						sql, err := buildInstantRateOverRowsSQL(trimRenderedQuerySQL(rowsSQL), tagsExpr, params.EvaluationTimeMS)
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: rowParams}, nil
					}
					if canUseInstantRangeFunctionRowsFastPath(fn) {
						source, err := renderAggregationSourceView(leafInfo.SourceExpr, params)
						if err != nil {
							return renderedFragment{}, err
						}
						rowsSQL, rowParams, err := storage.BuildRangeMatrixSelectorRowsQuerySQL(cfg, *source.Selector, params.RequiredStartMS, params.RequiredEndMS)
						if err != nil {
							return renderedFragment{}, err
						}
						tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(leafInfo.LeafSelector))
						sql, err := buildInstantRangeFunctionOverRowsSQL(trimRenderedQuerySQL(rowsSQL), fn, tagsExpr, params.EvaluationTimeMS)
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: rowParams}, nil
					}
				}
			}
		case *logicalpkg.SubqueryPlan:
			if child != nil && child.Child != nil && canUseInstantRangeFunctionRowsFastPath(fn) {
				// Pure-logical read: the range-function node's LoweringInfo
				// carries the cached SubqueryFragment directly via
				// RangeFunctionSubquery (see analysis.go — populated at each
				// of the seven range-function Analyze emission sites for
				// subquery children). We no longer dereference
				// info.Fragment.RangeFunction here.
				info := ctx.NativeAnalysis.InfoFor(n)
				if info != nil && info.RangeFunctionSubquery != nil && info.RangeFunctionSubquery.Child != nil {
					subqueryFrag := info.RangeFunctionSubquery
					if childRowsSQL, childParams, ok, err := tryRenderSubqueryRowsSource(cfg, subqueryFrag, params); err != nil {
						return renderedFragment{}, err
					} else if ok {
						sql, err := buildInstantRangeFunctionOverRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, subqueryRowsOutputTagsExpr(subqueryFrag, fn), params.EvaluationTimeMS)
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
				leafInfo := ctx.NativeAnalysis.InfoFor(leaf)
				if leafInfo != nil && leafInfo.LeafSelector != nil {
					tagsExpr = rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(leafInfo.LeafSelector))
					if leafInfo.LeafSelector.Kind == native.SelectorKindRangeVector {
						childRangeMS = leafInfo.LeafSelector.Lookback.Milliseconds()
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
				// Pure-logical read: LeafSelector mirrors
				// fragment.Selector (applySelectorProjection-narrowed)
				// and SourceExpr mirrors fragment.{ValueExpr,TagsExpr,
				// DropsMetric}. For LeafSource/range-vector leaves the
				// wrapper is always identity; we still check explicitly
				// so the fast-path gate is visible at the call site.
				leafInfo := ctx.NativeAnalysis.InfoFor(child)
				if leafInfo != nil && leafInfo.LeafSelector != nil && leafInfo.SourceExpr != nil &&
					leafInfo.LeafSelector.Kind == native.SelectorKindRangeVector {
					sel := leafInfo.LeafSelector
					view := leafInfo.SourceExpr
					isIdentity := view.ValueExpr == "{value}" && view.TagsExpr == "{tags}" && !view.DropsMetric
					lookbackMS := sel.Lookback.Milliseconds()
					offsetMS := sel.Offset.Milliseconds()
					if isIdentity && canUseRangeFunctionRowsFastPath(fn) {
						childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, params.StartMS, params.EndMS)
						source, err := renderAggregationSourceView(view, params)
						if err != nil {
							return renderedFragment{}, err
						}
						rowsSQL, rowParams, err := storage.BuildRangeMatrixSelectorRowsQuerySQL(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS)
						if err != nil {
							return renderedFragment{}, err
						}
						tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(sel))
						sql, err := buildRangeFunctionOverRowsSQL(trimRenderedQuerySQL(rowsSQL), fn, tagsExpr, params.StartMS, params.EndMS, params.StepMS, lookbackMS, offsetMS)
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: rowParams}, nil
					}
					// Keep the selector-scoped grid→data join that benchmarks well on long-range
					// windows, but skip per-step window_series/window_values materialization for
					// direct avg_over_time selectors by aggregating inside that grouped join.
					if isIdentity && fn == "avg_over_time" && preferDirectSelectorWindowJoin(lookbackMS, params.StepMS) {
						childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, params.StartMS, params.EndMS)
						source, err := renderAggregationSourceView(view, params)
						if err != nil {
							return renderedFragment{}, err
						}
						tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(sel))
						sql, queryParams, err := storage.BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fn, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
					}
					if isIdentity && preferDirectSelectorWindowJoin(lookbackMS, params.StepMS) {
						childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, params.StartMS, params.EndMS)
						source, err := renderAggregationSourceView(view, params)
						if err != nil {
							return renderedFragment{}, err
						}
						windowValueExpr := rangeFunctionValueExpr(fn, "window_series", "window_values", paramNumber, paramNumbers, "window_timestamps", "toFloat64(toUnixTimestamp64Milli(eval_ts))", lookbackMS)
						tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(sel))
						sql, queryParams, err := storage.BuildRangeWindowSelectorQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fn, windowValueExpr, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
					}
					// Range-mode leaf catch-all (Phase-6d retirement of the
					// Fragment-side RenderFragment at range.go:188). Lower the
					// logical leaf directly with a RangeMode child context
					// widened by the selector's lookback/offset.
					childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(child, params.StartMS, params.EndMS)
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
					tagsExpr := rangeFunctionTagsExprFromInput(fn, selectorOutputHasMetricName(sel))
					sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fn, tagsExpr, paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, lookbackMS, offsetMS)
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
				}
			}
		case *logicalpkg.SubqueryPlan:
			if child != nil && child.Child != nil {
				// Subquery-child fast path first: reuse tryRenderSubqueryRowsSource
				// against the cached subquery fragment, read off the range-
				// function node's LoweringInfo.RangeFunctionSubquery — no
				// info.Fragment dereference required.
				info := ctx.NativeAnalysis.InfoFor(n)
				if info != nil && info.RangeFunctionSubquery != nil && info.RangeFunctionSubquery.Child != nil {
					subqueryFrag := info.RangeFunctionSubquery
					if childRowsSQL, childParams, ok, err := tryRenderSubqueryRowsSource(cfg, subqueryFrag, params); err != nil {
						return renderedFragment{}, err
					} else if ok {
						var sql string
						childTagsExpr := subqueryRowsOutputTagsExpr(subqueryFrag, fn)
						if canUseRangeFunctionRowsFastPath(fn) {
							sql, err = buildRangeFunctionOverRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, childTagsExpr, params.StartMS, params.EndMS, params.StepMS, subqueryFrag.Range.Milliseconds(), subqueryFrag.Offset.Milliseconds())
						} else {
							sql, err = buildRangeFunctionOverWindowedRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, subqueryFrag.Range.Milliseconds(), subqueryFrag.Offset.Milliseconds())
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
