package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerInfoJoin renders an InfoPlan directly from the logical tree. The
// child is lowered via Lower (bubbling errUnsupportedLowerNode if the child
// kind isn't yet directly renderable), namespaced under "info_lhs", and
// handed to assembleInfoJoinSQL — the same shared helper the Fragment path
// calls — so SQL stays byte-identical.
//
// Info-series metadata (metric name fast path, selector matchers, label
// copies, DropUnmatched) is derived directly from InfoPlan.SelectorMatchers
// via the native.NativeInfoSelector / InfoJoinCopyLabelNames /
// InfoJoinDropUnmatched helpers.
//
// Hierarchical fallback: if logical analysis is missing or the child's
// recursive Lower returns errUnsupportedLowerNode, that sentinel bubbles
// up and the caller falls back to the Fragment rendering path for the
// whole query.
func lowerInfoJoin(ctx LoweringCtx, n *logicalpkg.InfoPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerInfoJoin called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: info missing logical analysis")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: info missing child node")
	}
	childRQ, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "info_lhs")
	if err != nil {
		return RenderedQuery{}, err
	}
	infoMetricName, selectorMatchers := native.NativeInfoSelector(n.SelectorMatchers)
	copyLabelNames := native.InfoJoinCopyLabelNames(n.SelectorMatchers)
	dropUnmatched := native.InfoJoinDropUnmatched(n.SelectorMatchers)
	rf, err := assembleInfoJoinSQL(ctx.Config, childSQL, childParams, infoMetricName, selectorMatchers, copyLabelNames, dropUnmatched, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}
