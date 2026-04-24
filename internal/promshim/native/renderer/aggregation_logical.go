package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

// renderAggregationLogical renders a *logicalpkg.AggregationPlan without
// constructing a top-level NativeFragment at the lowerer boundary. It is the
// logical-plan entry point for every aggregation op (sum, avg, count, min,
// max, stddev, stdvar, group, topk, bottomk, quantile, count_values) across
// leaf, grouping, range-fused, and scalar-binary child shapes.
//
// Phase 4 (Task 13a Phase 4): the top-level BuildFragment call has been
// moved out of this function and into renderAggregationLogicalDirect. The
// fused range+aggregation branch now routes entirely through the logical
// plan via canFuseRangeAggregationLogicalDirect +
// tryRenderFusedRangeAggregationLogicalDirect (Phase 3 helpers), so the
// fused path no longer builds a Fragment at the lowerer boundary. Only
// the non-fused branch keeps one scoped internal BuildFragment to feed
// the existing renderAggregationFragment body; Phase 6 retires that
// remaining call together with renderAggregationFragment.
//
// Hierarchical fallback: if the direct helper or its internal
// BuildFragment rejects the node (e.g. because the child selector is not
// lowerable yet) or the resulting fragment is the wrong kind, we return
// errUnsupportedLowerNode so the caller falls back to the Fragment
// rendering path wholesale.
func renderAggregationLogical(cfg storage.QueryConfig, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, n *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: aggregation requires a node")
	}
	return renderAggregationLogicalDirect(cfg, n, logicalAnalysis, analysis, params)
}

// renderAggregationLogicalDirect is the Phase-4 direct-render counterpart
// of renderAggregationFragment. It first checks the fused range+aggregation
// shape purely on the logical plan via canFuseRangeAggregationLogicalDirect;
// when the shape fuses, it delegates to
// tryRenderFusedRangeAggregationLogicalDirect so the fused branch never
// builds an aggregation Fragment at the lowerer boundary — only the
// internal BuildFragment inside the fused helper (still there pending
// Phase 6) is touched, and only after the logical-plan fusion shape has
// been confirmed.
//
// For the non-fused path we still materialize the AggregationPlan Fragment
// once internally so the dense renderAggregationFragment body keeps
// driving the SQL synthesis (instant/range, grouping, selection ops,
// count_values, scalar-binary source wrappers, EmitZeroOnEmpty wraps,
// direct-vs-subquery source fallback, etc.) byte-identically. That single
// call retires in Phase 6 when renderAggregationFragment is ported to
// consume the logical child directly.
//
// Hierarchical fallback: if the internal BuildFragment rejects the node
// or the resulting fragment is the wrong kind, we return
// errUnsupportedLowerNode so the caller falls back to the Fragment
// rendering path wholesale.
func renderAggregationLogicalDirect(cfg storage.QueryConfig, n *logicalpkg.AggregationPlan, logicalAnalysis *logicalpkg.Analysis, analysis *native.Analysis, params RenderParams) (renderedFragment, error) {
	// Fused range+aggregation: route through the logical-plan fused helper
	// so the lowerer boundary never BuildFragment's on this branch. The
	// capability check is purely structural on the logical plan and the
	// fused helper's remaining internal BuildFragment retires in Phase 6
	// together with tryRenderFusedRangeAggregationFragment.
	if canFuseRangeAggregationLogicalDirect(n, params) {
		if rendered, ok, err := tryRenderFusedRangeAggregationLogicalDirect(cfg, n, logicalAnalysis, analysis, params); err != nil {
			return renderedFragment{}, err
		} else if ok {
			return rendered, nil
		}
		// Capability said fuse but the try path declined — fall through
		// to the non-fused rendering rather than returning an error so
		// the query still renders correctly via the standard path.
	}

	// Non-fused path (transitional): materialize the aggregation Fragment
	// once so the existing renderAggregationFragment body keeps driving
	// the SQL. Phase 6 ports that body to consume the logical child
	// directly and retires this BuildFragment call. Until then, scoping
	// it here (not at the lowerer boundary) keeps Fragment access
	// localized to the one call site that still needs it.
	fragment, err := native.BuildFragment(n, analysis)
	if err != nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindAggregation {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	return renderAggregationFragment(cfg, fragment, params)
}
