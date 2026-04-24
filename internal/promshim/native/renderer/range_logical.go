package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// renderRangeFunctionLogical renders any of the seven logical node kinds that
// produce FragmentKindRangeFunction without constructing a top-level
// NativeFragment at the lowerer boundary. It is the public entry point for
// lower_range_function.go and keeps its signature stable for
// aggregation_logical.go (Phase 4) to continue consuming unchanged.
//
// Phase 3 (Task 13a Phase 3): the top-level BuildFragment call has been
// moved out of this function and into renderRangeFunctionLogicalDirect,
// which reads the child and per-function metadata (func name, scalars)
// from the logical plan via rangeFunctionChildNode. Fragment
// materialization is now scoped to a single internal BuildFragment call
// inside the direct helper, which still feeds the existing
// renderRangeFunctionFragment body. That Fragment machinery retires in
// Phase 6.
//
// Hierarchical fallback: if the direct helper or its internal
// BuildFragment rejects the node (e.g. because the child selector is not
// lowerable yet) or the resulting fragment is the wrong kind, we return
// errUnsupportedLowerNode so the caller falls back to the Fragment
// rendering path wholesale.
//
// The seven plan kinds covered here are RangeFunctionPlan, RatePlan,
// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan, and QuantileOverTimePlan.
func renderRangeFunctionLogical(cfg storage.QueryConfig, analysis *native.Analysis, n logicalpkg.Node, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: range function requires a node")
	}
	return renderRangeFunctionLogicalDirect(cfg, n, analysis, params)
}

// renderRangeFunctionLogicalDirect is the Phase-3 direct-render counterpart
// of renderRangeFunctionFragment. It reads the range-function child and
// function identity from the logical plan via rangeFunctionChildNode and
// then materializes the range-function Fragment once internally so the
// existing dense 900-LoC renderRangeFunctionFragment body can stay
// byte-identical for Phase 6. This mirrors the Phase 1/2 transitional
// pattern in the histogram ports: BuildFragment is no longer called at
// the lowerer boundary; the one remaining call is scoped to this helper
// and documented as the Phase-6 retirement target.
//
// Structural predicates that would otherwise require descending into the
// Fragment tree are now available via the logical-plan helpers
// rangeFunctionChildNode (child + func name) and the existing
// RangeFunctionChildIsLeafSelector / Shape inspection on InfoFor(n).
// Consumers that want to peer into the subtree without BuildFragment can
// reach for those helpers directly.
func renderRangeFunctionLogicalDirect(cfg storage.QueryConfig, n logicalpkg.Node, analysis *native.Analysis, params RenderParams) (renderedFragment, error) {
	childNode, _, ok := rangeFunctionChildNode(n)
	if !ok || childNode == nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}

	// Transitional: materialize the range-function Fragment once so the
	// existing renderRangeFunctionFragment body keeps driving the SQL.
	// Phase 6 ports that body to consume the logical child directly and
	// retires this BuildFragment call. Until then, scoping it here (not
	// at the lowerer boundary) keeps Fragment access localized to the
	// one call site that still needs it.
	fragment, err := native.BuildFragment(n, analysis)
	if err != nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindRangeFunction {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	return renderRangeFunctionFragment(cfg, fragment, params)
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

// tryRenderFusedRangeAggregationLogical renders the fused range+aggregation
// SQL directly from a logical AggregationPlan without constructing a
// top-level NativeFragment at the lowerer boundary. It is the logical-plan
// analog of tryRenderFusedRangeAggregationFragment and is consumed by the
// AggregationPlan lowerer (Phase 4).
//
// Phase 3 (Task 13a Phase 3): the top-level BuildFragment call has been
// moved out of this function and into
// tryRenderFusedRangeAggregationLogicalDirect. The capability check is now
// purely logical (canFuseRangeAggregationLogicalDirect). Fragment
// materialization is scoped to a single internal BuildFragment inside the
// direct helper, which still feeds the existing
// tryRenderFusedRangeAggregationFragment body. That Fragment machinery
// retires in Phase 6.
func tryRenderFusedRangeAggregationLogical(cfg storage.QueryConfig, analysis *native.Analysis, agg *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, bool, error) {
	return tryRenderFusedRangeAggregationLogicalDirect(cfg, agg, analysis, params)
}

// tryRenderFusedRangeAggregationLogicalDirect is the Phase-3 direct-render
// counterpart of tryRenderFusedRangeAggregationFragment. It guards the
// downstream Fragment rendering with a pure-logical fusion shape check
// (canFuseRangeAggregationLogicalDirect) so no Fragment is built for
// non-fusable aggregations. When the shape does fuse, it materializes the
// AggregationPlan Fragment once internally and delegates to
// tryRenderFusedRangeAggregationFragment for byte-identical SQL. The
// internal BuildFragment retires in Phase 6 together with
// tryRenderFusedRangeAggregationFragment.
func tryRenderFusedRangeAggregationLogicalDirect(cfg storage.QueryConfig, agg *logicalpkg.AggregationPlan, analysis *native.Analysis, params RenderParams) (renderedFragment, bool, error) {
	if !canFuseRangeAggregationLogicalDirect(agg, params) {
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
