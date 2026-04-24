package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerHistogramProjection lowers a HistogramProjectionPlan to a
// RenderedQuery by direct-rendering from the logical plan via
// renderHistogramProjectionLogical.
//
// Tag-narrowing for a grouping-aggregation child is threaded through
// RenderParams inside renderHistogramProjectionLogical.
//
// Hierarchical fallback: if the child cannot be materialized (e.g. an
// unsupported subtree), renderHistogramProjectionLogical returns
// errUnsupportedLowerNode so the caller falls back to the next
// execution tier.
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
