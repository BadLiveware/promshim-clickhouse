package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerSortTransform renders sort/sort_desc/sort_by_label/sort_by_label_desc
// directly. Range mode is a passthrough (sort is a no-op over matrix data);
// instant mode wraps the Lower-rendered child in the SORT outer SELECT,
// shared with the Fragment path via renderSortTransformInstantFromSource.
//
// Hierarchical fallback: if the child kind isn't yet direct-renderable,
// Lower returns errUnsupportedLowerNode and we propagate it so the caller
// falls back to the Fragment rendering path wholesale.
func lowerSortTransform(ctx LoweringCtx, n *logicalpkg.SortPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerSortTransform called with nil")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: sort_transform missing child")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: sort_transform missing analysis")
	}
	// Range mode: sort is a no-op, passthrough.
	if ctx.Params.Mode == native.RenderModeRange {
		return Lower(ctx, n.Child)
	}
	// Instant mode: wrap the Lower-rendered child in the SORT outer SELECT.
	childRQ, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err // bubble errUnsupportedLowerNode
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "sort_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	rf, err := renderSortTransformInstantFromSource(childSQL, childParams, n.Func, n.Labels)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}
