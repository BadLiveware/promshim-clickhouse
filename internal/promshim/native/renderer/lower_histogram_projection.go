package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerHistogramProjection lowers a HistogramProjectionPlan to a RenderedQuery
// via the existing Fragment renderer internals.
//
// Surface 9 uses the "Approach A" dispatch port: rather than rewriting the
// renderer body, the lowerer reads the pre-computed Fragment from
// ctx.NativeAnalysis.InfoFor(n).Fragment, validates the kind, then delegates
// to renderHistogramProjectionFragment so SQL stays byte-identical to the
// Fragment path. The render body retires with the final cleanup commit once
// all surfaces have ported.
//
// Hierarchical fallback: if native.Analyze didn't mark this node as
// native-lowerable (e.g. because the child selector isn't lowerable yet),
// nativeInfo.Fragment will be nil and we return errUnsupportedLowerNode so
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
	if ctx.NativeAnalysis == nil {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	nativeInfo := ctx.NativeAnalysis.InfoFor(n)
	if nativeInfo == nil || nativeInfo.Fragment == nil || nativeInfo.Fragment.Kind != native.FragmentKindHistogramProjection {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	rendered, err := renderHistogramProjectionFragment(ctx.Config, nativeInfo.Fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
