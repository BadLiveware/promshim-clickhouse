package renderer

import (
	"fmt"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerSubquery lowers a SubqueryPlan (PromQL subquery expressions like
// `up[5m:1m]`) directly from the logical tree. The subquery's job is to
// render its child over a step-grid carved out of the outer request bounds;
// we compute that envelope from the SubqueryPlan fields (shared with the
// Fragment path via subqueryRenderEnvelopeLogical) and recurse into Lower
// with a RenderParams set to RangeMode over that envelope. The child's own
// lowerer handles everything from there.
//
// Hierarchical fallback: Lower returns errUnsupportedLowerNode when the
// child kind isn't directly renderable yet, which bubbles up so the whole
// query falls back to the Fragment path.
func lowerSubquery(ctx LoweringCtx, n *logicalpkg.SubqueryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerSubquery called with nil node")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: subquery missing child node")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: subquery missing analysis")
	}
	startMS, endMS, stepMS, err := subqueryRenderEnvelopeLogical(n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	childRequiredStartMS, childRequiredEndMS := logicalRangeRequiredBoundsForChild(n.Child, startMS, endMS)
	childCtx := LoweringCtx{
		Config:         ctx.Config,
		Analysis:       ctx.Analysis,
		NativeAnalysis: ctx.NativeAnalysis,
		Params: RenderParams{
			Mode:                native.RenderModeRange,
			StartMS:             startMS,
			EndMS:               endMS,
			StepMS:              stepMS,
			RequiredStartMS:     childRequiredStartMS,
			RequiredEndMS:       childRequiredEndMS,
			ResolveSourcePromQL: ctx.Params.ResolveSourcePromQL,
		},
	}
	return Lower(childCtx, n.Child)
}

// subqueryRenderEnvelopeLogical computes the (startMS, endMS, stepMS)
// envelope for a subquery from the logical plan fields. The logic mirrors
// subqueryRenderEnvelope's Fragment-side body — we carve out the child
// step-grid over either the current instant (RenderModeInstant) or the outer
// range envelope (RenderModeRange).
func subqueryRenderEnvelopeLogical(n *logicalpkg.SubqueryPlan, params RenderParams) (int64, int64, int64, error) {
	if n == nil {
		return 0, 0, 0, fmt.Errorf("subquery plan is missing metadata")
	}
	if n.Range <= 0 {
		return 0, 0, 0, fmt.Errorf("subquery range must be greater than zero")
	}
	step := n.Step
	if step <= 0 {
		step = time.Minute
	}
	stepMS := step.Milliseconds()
	if stepMS <= 0 {
		return 0, 0, 0, fmt.Errorf("subquery step must be greater than zero")
	}
	rangeMS := n.Range.Milliseconds()
	offsetMS := n.Offset.Milliseconds()
	switch params.Mode {
	case native.RenderModeInstant:
		endMS := params.EvaluationTimeMS
		if n.Timestamp != nil {
			endMS = *n.Timestamp
		}
		endMS -= offsetMS
		startMS := alignSubqueryStepStart(endMS-rangeMS, stepMS)
		return startMS, endMS, stepMS, nil
	case native.RenderModeRange:
		endMS := params.EndMS - offsetMS
		startMS := alignSubqueryStepStart(params.StartMS-offsetMS-rangeMS, stepMS)
		return startMS, endMS, stepMS, nil
	default:
		return 0, 0, 0, fmt.Errorf("native subquery rendering in %s mode is not implemented yet", params.Mode)
	}
}
