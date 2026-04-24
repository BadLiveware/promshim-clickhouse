package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerRound renders round(v[, nearest]) directly. The value-transform outer
// SELECT is emitted via renderValueTransformFromSource; the value expression
// is computed from n.Decimals by buildRoundValueExpr.
//
// Hierarchical fallback: if the child kind isn't yet direct-renderable,
// Lower returns errUnsupportedLowerNode and we propagate it so the caller
// falls back to the next execution tier. An invalid toNearest (0, NaN, Inf)
// also triggers the sentinel so native-lowerability semantics match the
// analysis-time gate.
func lowerRound(ctx LoweringCtx, n *logicalpkg.RoundPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerRound called with nil")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: round missing child")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: round missing analysis")
	}
	toNearest := 1.0
	if n.Decimals != nil {
		toNearest = *n.Decimals
	}
	valueExpr, ok := buildRoundValueExpr(toNearest)
	if !ok {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	childRQ, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err // bubble errUnsupportedLowerNode
	}
	rf, err := renderValueTransformFromSource(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, valueExpr, "", true, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}
