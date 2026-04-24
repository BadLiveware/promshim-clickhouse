package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerHistogramProjection lowers a HistogramProjectionPlan to a RenderedQuery
// via the existing Fragment renderer internals.
//
// Transitional dispatch port: the lowerer builds the Fragment on demand from
// the logical node via native.BuildFragment (which reuses the analysis
// side-map when present or rebuilds it otherwise), validates the kind, then
// delegates to renderHistogramProjectionFragment so SQL stays byte-identical
// to the Fragment path. Once renderClassicHistogramGroupsQuery and friends
// port to logical children, this lowerer can drop the Fragment materialization
// and recurse via Lower on n.Child.
//
// Hierarchical fallback: if BuildFragment rejects the node (e.g. because the
// child selector isn't lowerable yet) we return errUnsupportedLowerNode so
// the caller falls back to the Fragment rendering path wholesale.
//
// Supported functions: histogram_count, histogram_sum, histogram_avg,
// histogram_stddev, histogram_stdvar.
func lowerHistogramProjection(ctx LoweringCtx, n *logicalpkg.HistogramProjectionPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerHistogramProjection called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: histogram projection missing logical analysis")
	}
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if fragment == nil || fragment.Kind != native.FragmentKindHistogramProjection {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderHistogramProjectionFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
