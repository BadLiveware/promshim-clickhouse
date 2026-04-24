package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
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
