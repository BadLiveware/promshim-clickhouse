package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerHistogramProjection lowers a HistogramProjectionPlan to a
// RenderedQuery by direct-rendering from the logical plan via
// renderHistogramProjectionLogical.
//
// Phase A1 (Task 8) removed the top-level Fragment construction from
// this lowerer. Tag-narrowing for a grouping-aggregation child is
// threaded through RenderParams inside renderHistogramProjectionLogical.
// The renderer still builds the child Fragment on demand transitionally
// so the legacy renderClassicHistogramGroupsQuery helper can emit
// byte-identical SQL; that materialization retires with the helper in
// Phase C (Task 13).
//
// Hierarchical fallback: if the child cannot be materialized (e.g. an
// unsupported subtree), renderHistogramProjectionLogical returns
// errUnsupportedLowerNode so the caller falls back to the Fragment
// rendering path wholesale.
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
	rendered, err := renderHistogramProjectionLogical(ctx.Config, ctx.Analysis, ctx.NativeAnalysis, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
