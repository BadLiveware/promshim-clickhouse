package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// renderAggregationLogical renders a *logicalpkg.AggregationPlan without
// constructing a top-level NativeFragment at the lowerer boundary. It is the
// logical-plan entry point for every aggregation op (sum, avg, count, min,
// max, stddev, stdvar, group, topk, bottomk, quantile, count_values) across
// leaf, grouping, range-fused, and scalar-binary child shapes.
//
// Phase 4 (Task 13a Phase 4): the top-level BuildFragment call has been
// moved out of this function and into renderAggregationLogicalDirect. The
// fused range+aggregation branch routes entirely through the logical plan
// via canFuseRangeAggregationLogicalDirect +
// tryRenderFusedRangeAggregationLogicalDirect (Phase 3 helpers).
//
// Phase 6e (Task 13a Phase 6e): the non-fused branch no longer materializes
// a scoped aggregation Fragment at the lowerer boundary either. The body is
// now ported in renderAggregationLogicalBody, which reads Op, Grouping,
// Without, ParamNumber, and ParamString directly from the AggregationPlan
// and recurses via renderer.Lower for the subquery-fallback path.
// EmitZeroOnEmpty and the cloned-with-full-tags source Fragment (for
// selection aggregations and count_values) are still consulted off the
// cached native.Analysis entry because both encode narrowing/structural
// decisions that do not yet have a standalone logical representation.
//
// Hierarchical fallback: if renderAggregationLogicalBody cannot lower the
// child shape (e.g. selector-source construction fails and the subquery
// fallback also declines) the error propagates so the caller can fall
// back to the Fragment rendering path wholesale.
func renderAggregationLogical(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: aggregation requires a node")
	}
	return renderAggregationLogicalDirect(ctx, n)
}

// renderAggregationLogicalDirect is the Phase-4 direct-render counterpart
// of renderAggregationFragment. It first checks the fused range+aggregation
// shape purely on the logical plan via canFuseRangeAggregationLogicalDirect;
// when the shape fuses, it delegates to
// tryRenderFusedRangeAggregationLogicalDirect so the fused branch never
// builds an aggregation Fragment at the lowerer boundary.
//
// Phase 6e (Task 13a Phase 6e): the non-fused branch now hands the
// AggregationPlan to renderAggregationLogicalBody — the logical-plan port
// of renderAggregationFragment's body — instead of materializing the
// Fragment and calling renderAggregationFragment. No BuildFragment call
// remains on this path.
func renderAggregationLogicalDirect(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (renderedFragment, error) {
	// Fused range+aggregation: route through the logical-plan fused helper
	// so the lowerer boundary never BuildFragment's on this branch. The
	// capability check is purely structural on the logical plan.
	if canFuseRangeAggregationLogicalDirect(n, ctx.Params) {
		if rendered, ok, err := tryRenderFusedRangeAggregationLogicalDirect(ctx.Config, n, ctx.Analysis, ctx.NativeAnalysis, ctx.Params); err != nil {
			return renderedFragment{}, err
		} else if ok {
			return rendered, nil
		}
		// Capability said fuse but the try path declined — fall through
		// to the non-fused rendering rather than returning an error so
		// the query still renders correctly via the standard path.
	}

	return renderAggregationLogicalBody(ctx, n)
}

// renderAggregationLogicalBody is the Phase-6e logical-plan port of
// renderAggregationFragment (join.go:62-130). It mirrors the Fragment-side
// body branch-for-branch in both instant and range modes:
//
//  1. Direct-aggregation-source: when the aggregation's child shape can be
//     rendered as a storage.AggregationSource (leaf selector /
//     unary-source / binary-scalar-source fragments, read off the
//     analysis-side SourceExprView on the child node), build the
//     aggregation SQL via BuildInstantAggregationQuerySQLWithBounds /
//     BuildRangeAggregationQuerySQLWithBounds.
//  2. Subquery fallback: when the child cannot feed the direct-aggregation
//     source path, recurse into the logical child via renderLogicalSubquery
//     and build the aggregation over the rendered subquery via
//     BuildInstantAggregationOverSubquerySQL /
//     BuildRangeAggregationOverSubquerySQL.
//
// Aggregation fields (Op, Grouping, Without, ParamNumber, ParamString) are
// read directly from the logical plan. EmitZeroOnEmpty is read from the
// cached aggregation info (populated during native.Analyze) because it is
// computed from a structural `sum(x) or vector(0)` pattern that has no
// standalone logical representation. The direct-source shape data comes
// from info.SourceExpr on the logical child node — no NativeFragment
// dereference at the render boundary.
//
// For selection aggregations (topk, bottomk, limitk, limit_ratio) and
// count_values the renderer forces RequireFullTags=true via RenderParams
// so the underlying selector emits the full tags array. This mirrors the
// Fragment-side clone + forceFragmentFullTags without materializing a
// Fragment at the boundary: renderAggregationSourceView already honors
// params.RequireFullTags through applyRenderParamsNarrowing.
//
// Task 13c-14d-1: the scoped CloneFragment + forceFragmentFullTags +
// renderAggregationSource(*NativeFragment) chain retires here. The
// direct-source branch now reads info.SourceExpr on the logical child
// and threads the selection-aggregation full-tag forcing through
// RenderParams.
func renderAggregationLogicalBody(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: aggregation body requires a node")
	}
	if ctx.NativeAnalysis == nil {
		return renderedFragment{}, fmt.Errorf("aggregation (logical) requires a native analysis")
	}
	aggInfo := ctx.NativeAnalysis.InfoFor(n)
	if aggInfo == nil || aggInfo.Aggregation == nil {
		return renderedFragment{}, fmt.Errorf("aggregation (logical) is missing cached aggregation metadata")
	}
	cachedAgg := aggInfo.Aggregation

	// Selection aggregations (topk, bottomk, limitk, limit_ratio) and
	// count_values synthesize output labels from the input labelset and
	// cannot lower through the empty-tags selector fast path; force the
	// underlying selector to emit the full tags array via RenderParams.
	sourceParams := ctx.Params
	if native.IsSelectionNativeAggregation(n.Op) || n.Op == parser.COUNT_VALUES {
		sourceParams.RequireFullTags = true
		sourceParams.RequiredTagLabels = nil
	}

	// Branch 1: direct-aggregation-source. Render from the child node's
	// SourceExprView, which mirrors the leaf / unary-source /
	// binary-scalar-source fragment shapes — the same shapes the
	// Fragment-side body handled.
	var childInfo *native.LoweringInfo
	if n.Child != nil {
		childInfo = ctx.NativeAnalysis.InfoFor(n.Child)
	}
	if childInfo != nil && childInfo.SourceExpr != nil {
		if source, err := renderAggregationSourceView(childInfo.SourceExpr, sourceParams); err == nil {
			switch ctx.Params.Mode {
			case native.RenderModeInstant:
				sql, queryParams, err := storage.BuildInstantAggregationQuerySQLWithBounds(ctx.Config, source, ctx.Params.EvaluationTimeMS, ctx.Params.RequiredStartMS, ctx.Params.RequiredEndMS, n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString)
				if err != nil {
					return renderedFragment{}, err
				}
				if cachedAgg.EmitZeroOnEmpty {
					return wrapZeroOnEmptyAggregationInstantSQL(trimRenderedQuerySQL(sql), queryParams, ctx.Params), nil
				}
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
			case native.RenderModeRange:
				sql, queryParams, err := storage.BuildRangeAggregationQuerySQLWithBounds(ctx.Config, source, ctx.Params.StartMS, ctx.Params.EndMS, ctx.Params.StepMS, ctx.Params.RequiredStartMS, ctx.Params.RequiredEndMS, n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString)
				if err != nil {
					return renderedFragment{}, err
				}
				if cachedAgg.EmitZeroOnEmpty {
					return wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, ctx.Params), nil
				}
				return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
			default:
				return renderedFragment{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
			}
		}
	}

	// Branch 2: subquery fallback (Phase-6e retirement of the
	// Fragment-side renderFragmentSubquery at join.go:103). Lower the
	// logical child directly so the child SQL is driven off the
	// logical tree, then wrap it in an aggregation-over-subquery shell.
	childSQL, childParams, err := renderLogicalSubquery(ctx.Config, n.Child, ctx.Analysis, ctx.NativeAnalysis, ctx.Params, "aggregation_child")
	if err != nil {
		return renderedFragment{}, err
	}
	source := storage.AggregationSource{ValueExpr: "{value}", TagsExpr: "{tags}"}
	switch ctx.Params.Mode {
	case native.RenderModeInstant:
		sql, queryParams, err := storage.BuildInstantAggregationOverSubquerySQL(source, childSQL, childParams, ctx.Params.EvaluationTimeMS, n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString)
		if err != nil {
			return renderedFragment{}, err
		}
		if cachedAgg.EmitZeroOnEmpty {
			return wrapZeroOnEmptyAggregationInstantSQL(trimRenderedQuerySQL(sql), queryParams, ctx.Params), nil
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	case native.RenderModeRange:
		sql, queryParams, err := storage.BuildRangeAggregationOverSubquerySQL(source, childSQL, childParams, n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString)
		if err != nil {
			return renderedFragment{}, err
		}
		if cachedAgg.EmitZeroOnEmpty {
			return wrapZeroOnEmptyAggregationRangeSQL(trimRenderedQuerySQL(sql), queryParams, ctx.Params), nil
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
	}
}
