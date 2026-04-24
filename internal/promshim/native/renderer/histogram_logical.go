package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

// renderHistogramProjectionLogical renders a HistogramProjectionPlan
// without constructing a top-level NativeFragment at the lowerer
// boundary. Tag-narrowing for a grouping-aggregation child is threaded
// through RenderParams using decideHistogramChildNarrowing.
//
// Phase A1 transitional: the renderer still materializes the child
// Fragment on demand via native.BuildFragment and hands it to the legacy
// renderClassicHistogramGroupsQuery helper so the emitted SQL stays
// byte-identical with the Fragment path. The materialized child's
// selector is pre-narrowed from RenderParams so the helper's internal
// narrowHistogramAggregationChildTags call is idempotent. The legacy
// helper retires in Phase C (Task 13) once renderClassicHistogramGroupsQuery
// has been ported to consume a logical child directly.
func renderHistogramProjectionLogical(cfg storage.QueryConfig, analysis *native.Analysis, n *logicalpkg.HistogramProjectionPlan, params RenderParams) (renderedFragment, error) {
	if n == nil || n.Child == nil {
		return renderedFragment{}, fmt.Errorf("renderer: histogram projection requires a child")
	}

	// Thread tag-narrowing for a grouping-aggregation child via
	// RenderParams. Non-aggregation children leave the params untouched
	// so the underlying SelectorSource keeps governing.
	childParams := params
	if aggChild, ok := n.Child.(*logicalpkg.AggregationPlan); ok {
		requireFull, labels := decideHistogramChildNarrowing(aggChild)
		childParams.RequireFullTags = requireFull
		childParams.RequiredTagLabels = labels
	}

	// Phase-A transitional: materialize the child Fragment on demand,
	// apply narrowing onto its selector so the legacy helper sees a
	// pre-narrowed shape, then reuse renderClassicHistogramGroupsQuery.
	childFragment, err := native.BuildFragment(n.Child, analysis)
	if err != nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	childFragment = applyRenderParamsNarrowing(childFragment, childParams)

	histograms, err := renderClassicHistogramGroupsQuery(cfg, childFragment, childParams, "histogram_projection_child")
	if err != nil {
		return renderedFragment{}, err
	}
	valueExpr, err := classicHistogramProjectionValueExpr(sqlb.Ident("buckets"), n.Func)
	if err != nil {
		return renderedFragment{}, err
	}
	return histogramOutputFragment(histograms, valueExpr, params.Mode, "histogram_projection_steps"), nil
}

// renderHistogramFunctionLogical renders a histogram-function plan node
// (HistogramQuantilePlan, HistogramFractionPlan, or HistogramQuantilesPlan)
// without constructing a top-level NativeFragment at the lowerer boundary.
// Tag-narrowing for a grouping-aggregation child is threaded through
// RenderParams using histogramFunctionChildAggregation +
// decideHistogramChildNarrowing, mirroring the projection approach in
// renderHistogramProjectionLogical.
//
// Phase A2 transitional: the renderer materializes the whole histogram-function
// Fragment on demand (BuildFragment on the node itself) and delegates to
// renderHistogramFunctionFragment. The child Fragment's selector is
// pre-narrowed via applyRenderParamsNarrowing so the legacy
// narrowHistogramAggregationChildTags inside renderClassicHistogramGroupsQuery
// is idempotent. Fragment materialization retires in Phase C (Task 13).
func renderHistogramFunctionLogical(cfg storage.QueryConfig, analysis *native.Analysis, n logicalpkg.Node, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: histogram function requires a node")
	}

	// Compute narrowing from the histogram-function's child if it is a
	// grouping aggregation. Non-aggregation children leave params untouched.
	childParams := params
	if aggChild := histogramFunctionChildAggregation(n); aggChild != nil {
		requireFull, labels := decideHistogramChildNarrowing(aggChild)
		childParams.RequireFullTags = requireFull
		childParams.RequiredTagLabels = labels
	}

	// Phase-A2 transitional: build the whole histogram-function Fragment,
	// then pre-narrow the child selector so the legacy helper is idempotent.
	fragment, err := native.BuildFragment(n, analysis)
	if err != nil {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindHistogramFunction {
		return renderedFragment{}, errUnsupportedLowerNode
	}
	if fragment.HistogramFunction != nil && fragment.HistogramFunction.Child != nil {
		fragment.HistogramFunction.Child = applyRenderParamsNarrowing(fragment.HistogramFunction.Child, childParams)
	}

	return renderHistogramFunctionFragment(cfg, fragment, params)
}

// histogramFunctionChildAggregation returns the AggregationPlan child of
// any histogram-function plan kind, or nil when the child is not a direct
// aggregation. The immediate child of each plan kind is the bucket expression;
// unwrapToAggregation peels one level in case it is an AggregationPlan.
// RangeFunction wrappers (e.g. rate(x[5m])) sit INSIDE the aggregation,
// not outside it, so the immediate child of histogram_quantile(q, sum by (le)
// (rate(...))) is the AggregationPlan — no additional peeling is needed.
func histogramFunctionChildAggregation(n logicalpkg.Node) *logicalpkg.AggregationPlan {
	switch h := n.(type) {
	case *logicalpkg.HistogramQuantilePlan:
		return unwrapToAggregation(h.Child)
	case *logicalpkg.HistogramFractionPlan:
		return unwrapToAggregation(h.Child)
	case *logicalpkg.HistogramQuantilesPlan:
		return unwrapToAggregation(h.Child)
	default:
		return nil
	}
}

// unwrapToAggregation returns n as an *AggregationPlan, or nil if it is not one.
func unwrapToAggregation(n logicalpkg.Node) *logicalpkg.AggregationPlan {
	if agg, ok := n.(*logicalpkg.AggregationPlan); ok {
		return agg
	}
	return nil
}

// applyRenderParamsNarrowing merges RenderParams narrowing fields onto
// a child Fragment's underlying selector. It mirrors
// narrowHistogramAggregationChildTags so the legacy
// renderClassicHistogramGroupsQuery helper, which re-applies the same
// rule internally, is idempotent against this pre-narrowing.
//
// The helper is a no-op when the parent has not expressed a narrowing
// requirement (RequireFullTags=true or empty RequiredTagLabels), or
// when the child's aggregation shape would itself reject narrowing
// (Without=true, empty grouping, missing source).
func applyRenderParamsNarrowing(fragment *native.NativeFragment, params RenderParams) *native.NativeFragment {
	if fragment == nil || params.RequireFullTags || len(params.RequiredTagLabels) == 0 {
		return fragment
	}
	if fragment.Aggregation == nil || fragment.Aggregation.Without || len(fragment.Aggregation.Grouping) == 0 || fragment.Aggregation.Source == nil {
		return fragment
	}
	cloned := native.CloneFragment(fragment)
	selector := native.BaseSelectorSource(cloned.Aggregation.Source)
	if selector == nil {
		return cloned
	}
	selector.RequireFullTags = false
	selector.RequiredTagLabels = append([]string(nil), params.RequiredTagLabels...)
	return cloned
}
