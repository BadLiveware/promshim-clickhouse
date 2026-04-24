package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// renderRangeFunctionLogical is the public entry point for range-function
// rendering from the logical plan. It walks the tree directly and
// recurses through Lower for child sub-trees.
//
// If a node shape cannot be lowered it returns errUnsupportedLowerNode so
// the caller falls back to the next execution tier.
//
// The seven plan kinds covered here are RangeFunctionPlan, RatePlan,
// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan, and QuantileOverTimePlan.
func renderRangeFunctionLogical(ctx LoweringCtx, n logicalpkg.Node) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: range function requires a node")
	}
	return renderRangeFunctionLogicalDirect(ctx, n)
}

// renderRangeFunctionLogicalDirect reads the range-function child and
// function identity from the logical plan via rangeFunctionChildNode
// and drives SQL rendering from the logical tree through
// renderRangeFunctionLogicalBody.
//
// The fast-path branches that need SelectorSource data (for the
// BuildRangeMatrixSelectorRowsQuerySQL,
// BuildRangeWindowSelectorDirectAggregateQuerySQLWithFinalTags, and
// BuildRangeWindowSelectorQuerySQLWithFinalTags helpers) read the
// cached Selector off the leaf's LoweringInfo (LeafSelector +
// SourceExprView). Subquery-child fast paths read the cached subquery
// info off the range-function node's own
// LoweringInfo.RangeFunctionSubquery.
func renderRangeFunctionLogicalDirect(ctx LoweringCtx, n logicalpkg.Node) (renderedFragment, error) {
	childNode, _, ok := rangeFunctionChildNode(n)
	if !ok || childNode == nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	return renderRangeFunctionLogicalBody(ctx, n)
}

// renderRangeFunctionLogicalBody renders a range-function plan from
// the logical tree in both instant and range modes:
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
// cached Selector off the leaf's LoweringInfo (LeafSelector +
// SourceExprView). Subquery-child fast paths read the cached subquery
// info off LoweringInfo.RangeFunctionSubquery (populated during
// native.Analyze at each of the seven range-function emission sites).
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
				// The leaf's LoweringInfo carries LeafSelector
				// (narrowed) and SourceExpr (identity wrapper for
				// range-vector leaves).
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
						tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
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
						tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
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
				// The subquery's child is inspected via
				// tryRenderSubqueryRowsSourceLogical, which matches the
				// aggregation-over-range-function fused shape directly
				// off the logical plan.
				if childRowsSQL, childParams, ok, err := tryRenderSubqueryRowsSourceLogical(ctx, child); err != nil {
					return renderedFragment{}, err
				} else if ok {
					sql, err := buildInstantRangeFunctionOverRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, subqueryRowsOutputTagsExprLogical(child, fn), params.EvaluationTimeMS)
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
				}
			}
		}
		// Instant-mode fallback: Lower the logical child directly.
		// The tags expression still prefers the narrowed selector
		// data when available.
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
					tagsExpr = rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
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
				// For range-vector leaves the wrapper is always
				// identity; we still check explicitly so the
				// fast-path gate is visible at the call site.
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
						tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
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
						tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
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
						tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
						sql, queryParams, err := storage.BuildRangeWindowSelectorQuerySQLWithFinalTags(cfg, *source.Selector, childRequiredStartMS, childRequiredEndMS, params.StartMS, params.EndMS, params.StepMS, fn, windowValueExpr, tagsExpr, minimumSeriesLengthForRangeFunction(fn))
						if err != nil {
							return renderedFragment{}, err
						}
						return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
					}
					// Range-mode leaf catch-all: Lower the logical leaf
					// directly with a RangeMode child context widened by
					// the selector's lookback/offset.
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
						RequireFullTags:     params.RequireFullTags,
						RequiredTagLabels:   params.RequiredTagLabels,
					}
					childRendered, err := Lower(childCtx, child)
					if err != nil {
						return renderedFragment{}, err
					}
					tagsExpr := rangeFunctionTagsExprFromInput(fn, paramsInputHasMetricName(params))
					sql, err := buildRangeFunctionOverWindowedArraysSQL(trimRenderedQuerySQL(childRendered.SQL), fn, tagsExpr, paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, lookbackMS, offsetMS)
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams}, nil
				}
			}
		case *logicalpkg.SubqueryPlan:
			if child != nil && child.Child != nil {
				// Subquery-child fast path first:
				// tryRenderSubqueryRowsSourceLogical inspects the
				// logical subquery child directly for the fused
				// aggregation-over-range-function shape.
				if childRowsSQL, childParams, ok, err := tryRenderSubqueryRowsSourceLogical(ctx, child); err != nil {
					return renderedFragment{}, err
				} else if ok {
					var sql string
					childTagsExpr := subqueryRowsOutputTagsExprLogical(child, fn)
					if canUseRangeFunctionRowsFastPath(fn) {
						sql, err = buildRangeFunctionOverRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, childTagsExpr, params.StartMS, params.EndMS, params.StepMS, child.Range.Milliseconds(), child.Offset.Milliseconds())
					} else {
						sql, err = buildRangeFunctionOverWindowedRowsSQL(trimRenderedQuerySQL(childRowsSQL), fn, paramNumber, paramNumbers, params.StartMS, params.EndMS, params.StepMS, child.Range.Milliseconds(), child.Offset.Milliseconds())
					}
					if err != nil {
						return renderedFragment{}, err
					}
					return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
				}
				// Subquery fallback: Lower the logical subquery node
				// directly; lowerSubquery carves out the child
				// step-grid over the outer range envelope.
				childCtx := ctx
				childCtx.Params = RenderParams{
					Mode:                native.RenderModeRange,
					StartMS:             params.StartMS,
					EndMS:               params.EndMS,
					StepMS:              params.StepMS,
					RequiredStartMS:     params.RequiredStartMS,
					RequiredEndMS:       params.RequiredEndMS,
					ResolveSourcePromQL: params.ResolveSourcePromQL,
					RequireFullTags:     params.RequireFullTags,
					RequiredTagLabels:   params.RequiredTagLabels,
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
// "quantile_over_time", "predict_linear").
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
// can be fused with its range-function child into a single SQL pass.
// Consumed by the AggregationPlan lowerer. Capability shape:
//
//   - range-mode only (params.Mode == RenderModeRange),
//   - the aggregation is a SUM (agg.Op == parser.SUM),
//   - the aggregation's child is one of the seven range-function plan
//     kinds,
//   - that range-function's child is either a LeafExprPlan wrapping a
//     parser.MatrixSelector or a *SubqueryPlan with a non-nil Child.
//
// Non-matching nodes return false, falling through to the non-fused
// aggregation rendering path.
func canFuseRangeAggregationLogical(agg *logicalpkg.AggregationPlan, params RenderParams) bool {
	return canFuseRangeAggregationLogicalDirect(agg, params)
}

// canFuseRangeAggregationLogicalDirect is a pure logical-plan shape
// predicate; see canFuseRangeAggregationLogical above for the shape
// requirements.
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

// tryRenderFusedRangeAggregationLogicalDirect consumes the logical
// AggregationPlan directly: the capability check is pure-logical
// (canFuseRangeAggregationLogicalDirect) and the SQL synthesis runs on
// the logical plan through tryRenderFusedRangeAggregationLogical.
func tryRenderFusedRangeAggregationLogicalDirect(cfg storage.QueryConfig, agg *logicalpkg.AggregationPlan, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, params RenderParams) (renderedFragment, bool, error) {
	ctx := LoweringCtx{
		Config:         cfg,
		Analysis:       logicalAnalysis,
		NativeAnalysis: analysis,
		Params:         params,
	}
	return tryRenderFusedRangeAggregationLogical(ctx, agg)
}

// tryRenderSubqueryRowsSourceLogical inspects the SubqueryPlan's logical
// child: when that child is an AggregationPlan whose shape matches
// canFuseRangeAggregationLogicalDirect, it renders the fused
// range+aggregation rows via renderFusedRangeAggregationLogicalRowsSQL over
// a child-rendering envelope carved from the subquery bounds. Non-matching
// shapes return ok=false so the caller falls back to the standard subquery
// rendering.
func tryRenderSubqueryRowsSourceLogical(ctx LoweringCtx, n *logicalpkg.SubqueryPlan) (string, map[string]string, bool, error) {
	if n == nil || n.Child == nil {
		return "", nil, false, nil
	}
	agg, ok := n.Child.(*logicalpkg.AggregationPlan)
	if !ok || agg == nil {
		return "", nil, false, nil
	}
	startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(n, ctx.Params)
	if err != nil {
		return "", nil, false, err
	}
	childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(n.Child, startMS, endMS)
	childCtx := LoweringCtx{
		Config:         ctx.Config,
		Analysis:       ctx.Analysis,
		NativeAnalysis: ctx.NativeAnalysis,
		Params: RenderParams{
			Mode:                native.RenderModeRange,
			StartMS:             startMS,
			EndMS:               endMS,
			StepMS:              stepMS,
			RequiredStartMS:     childRequiredStartMS,
			RequiredEndMS:       childRequiredEndMS,
			ResolveSourcePromQL: ctx.Params.ResolveSourcePromQL,
		},
	}
	if !canFuseRangeAggregationLogicalDirect(agg, childCtx.Params) {
		return "", nil, false, nil
	}
	rowsSQL, rowParams, err := renderFusedRangeAggregationLogicalRowsSQL(childCtx, agg)
	if err != nil {
		return "", nil, false, err
	}
	return trimRenderedQuerySQL(rowsSQL), rowParams, true, nil
}

// subqueryRowsOutputHasMetricNameLogical walks the logical subquery child
// (expected to be an AggregationPlan wrapping a range-function plan) and
// returns whether the inner range function preserves the metric name.
// Falls back to true (conservative) for shapes that do not match the
// aggregation-over-range-function pattern.
func subqueryRowsOutputHasMetricNameLogical(n *logicalpkg.SubqueryPlan) bool {
	if n == nil || n.Child == nil {
		return true
	}
	agg, ok := n.Child.(*logicalpkg.AggregationPlan)
	if !ok || agg == nil || agg.Child == nil {
		return true
	}
	_, fn, ok := rangeFunctionChildNode(agg.Child)
	if !ok {
		return true
	}
	return native.RangeFunctionPreservesMetricName(fn)
}

// subqueryRowsOutputTagsExprLogical returns the tags expression for the
// rows emitted by a subquery child, given the outer range function name.
func subqueryRowsOutputTagsExprLogical(n *logicalpkg.SubqueryPlan, outerFn string) string {
	return rangeFunctionTagsExprFromInput(outerFn, subqueryRowsOutputHasMetricNameLogical(n))
}
