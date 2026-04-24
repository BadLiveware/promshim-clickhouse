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
// Phase A4 transitional; BuildFragment retires in Phase C. The renderer
// materializes the whole aggregation Fragment on demand via
// native.BuildFragment and delegates to renderAggregationFragment so the
// emitted SQL stays byte-identical with the Fragment path. The fused
// range+aggregation shape is handled transparently — renderAggregationFragment
// internally calls tryRenderFusedRangeAggregationFragment when the
// aggregation's source is FragmentKindRangeFunction. Fragment materialization
// retires in Phase C (Task 13) together with renderAggregationFragment and the
// fused-range aggregation helpers.
//
// Hierarchical fallback: if BuildFragment rejects the node (e.g. because the
// child selector is not lowerable yet) or the resulting fragment is the wrong
// kind, we return errUnsupportedLowerNode so the caller falls back to the
// Fragment rendering path wholesale.
func renderAggregationLogical(cfg storage.QueryConfig, analysis *native.Analysis, n *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: aggregation requires a node")
	}

	fragment, err := native.BuildFragment(n, analysis)
	if err != nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindAggregation {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	return renderAggregationFragment(cfg, fragment, params)
}
