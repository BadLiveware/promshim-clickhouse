package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

// renderAggregationLogical is the entry point for every aggregation op
// (sum, avg, count, min, max, stddev, stdvar, group, topk, bottomk,
// quantile, count_values) across leaf, grouping, range-fused, and
// scalar-binary child shapes.
func renderAggregationLogical(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: aggregation requires a node")
	}
	return renderAggregationLogicalDirect(ctx, n)
}

// renderAggregationLogicalDirect routes fused range+aggregation shapes
// through tryRenderFusedRangeAggregationLogicalDirect; non-fused shapes
// fall through to renderAggregationLogicalBody.
func renderAggregationLogicalDirect(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (renderedFragment, error) {
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

// renderAggregationLogicalBody renders an aggregation in both instant
// and range modes via two branches:
//
//  1. Direct-aggregation-source: when the child exposes a SourceExprView
//     (leaf selector / unary-source / binary-scalar-source), build the
//     aggregation SQL via BuildInstantAggregationQuerySQLWithBounds /
//     BuildRangeAggregationQuerySQLWithBounds.
//  2. Subquery fallback: when the child cannot feed the direct source,
//     recurse via renderLogicalSubquery and build the aggregation over
//     the rendered subquery.
//
// Op, Grouping, Without, ParamNumber, and ParamString come from the
// logical plan. EmitZeroOnEmpty is read from the cached aggregation
// info because it derives from a structural `sum(x) or vector(0)`
// pattern with no standalone logical representation.
//
// Selection aggregations (topk, bottomk, limitk, limit_ratio) and
// count_values synthesize output labels from the input labelset; the
// body forces RequireFullTags=true via RenderParams so the underlying
// selector emits the full tags array.
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

	sourceParams := aggregationChildRenderParams(n, ctx.Params)

	// Branch 1: direct-aggregation-source. Render from the child node's
	// SourceExprView (leaf / unary-source / binary-scalar-source shapes).
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

	// Branch 2: subquery fallback. Lower the logical child directly so
	// the child SQL is driven off the logical tree, then wrap it in an
	// aggregation-over-subquery shell.
	childSQL, childParams, err := renderLogicalSubquery(ctx.Config, n.Child, ctx.Analysis, ctx.NativeAnalysis, sourceParams, "aggregation_child")
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
