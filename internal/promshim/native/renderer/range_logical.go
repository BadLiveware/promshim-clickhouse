package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

// renderRangeFunctionLogical renders any of the seven logical node kinds that
// produce FragmentKindRangeFunction without constructing a top-level
// NativeFragment at the lowerer boundary.
//
// Phase A3 transitional: the renderer materializes the whole range-function
// Fragment on demand via native.BuildFragment and delegates to
// renderRangeFunctionFragment so the emitted SQL stays byte-identical with
// the Fragment path. Fragment materialization retires in Phase C (Task 13)
// together with renderRangeFunctionFragment and its structural fast paths.
//
// Hierarchical fallback: if BuildFragment rejects the node (e.g. because the
// child selector is not lowerable yet) or the resulting fragment is the wrong
// kind, we return errUnsupportedLowerNode so the caller falls back to the
// Fragment rendering path wholesale.
//
// The seven plan kinds covered here are RangeFunctionPlan, RatePlan,
// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan, and QuantileOverTimePlan.
func renderRangeFunctionLogical(cfg storage.QueryConfig, analysis *native.Analysis, n logicalpkg.Node, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: range function requires a node")
	}

	fragment, err := native.BuildFragment(n, analysis)
	if err != nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindRangeFunction {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	return renderRangeFunctionFragment(cfg, fragment, params)
}

// canFuseRangeAggregationLogical reports whether a logical AggregationPlan
// can be fused with its range-function child into a single SQL pass. It is
// the logical-plan analog of canFuseRangeAggregationFragment and will be
// consumed by the upcoming AggregationPlan lowerer port (Task 11).
//
// Phase A3 transitional: the helper materializes the AggregationPlan
// Fragment on demand via native.BuildFragment(agg, nil) and delegates to
// canFuseRangeAggregationFragment. A nil analysis is intentional — the
// capability check does not need tag side-information, only the structural
// shape of the aggregation + its range-function child. On any BuildFragment
// error or fragment shape mismatch, we conservatively return false.
func canFuseRangeAggregationLogical(agg *logicalpkg.AggregationPlan, params RenderParams) bool {
	if agg == nil {
		return false
	}
	fragment, err := native.BuildFragment(agg, nil)
	if err != nil {
		return false
	}
	if fragment == nil || fragment.Kind != native.FragmentKindAggregation {
		return false
	}
	return canFuseRangeAggregationFragment(fragment, params)
}

// tryRenderFusedRangeAggregationLogical renders the fused range+aggregation
// SQL directly from a logical AggregationPlan without constructing a
// top-level NativeFragment at the lowerer boundary. It is the logical-plan
// analog of tryRenderFusedRangeAggregationFragment and will be consumed by
// the upcoming AggregationPlan lowerer port (Task 11).
//
// Phase A3 transitional: the helper materializes the AggregationPlan
// Fragment on demand via native.BuildFragment and delegates to
// tryRenderFusedRangeAggregationFragment. On any BuildFragment error or
// fragment shape mismatch we return (renderedFragment{}, false, nil) so the
// caller falls through to the non-fused aggregation rendering path.
func tryRenderFusedRangeAggregationLogical(cfg storage.QueryConfig, analysis *native.Analysis, agg *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, bool, error) {
	if agg == nil {
		return renderedFragment{}, false, nil
	}
	fragment, err := native.BuildFragment(agg, analysis)
	if err != nil {
		return renderedFragment{}, false, nil
	}
	if fragment == nil || fragment.Kind != native.FragmentKindAggregation {
		return renderedFragment{}, false, nil
	}
	return tryRenderFusedRangeAggregationFragment(cfg, fragment, params)
}
