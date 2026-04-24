package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

// lowerUnary renders a UnaryPlan directly from the logical tree. The
// child is lowered via Lower (bubbling errUnsupportedLowerNode if the
// child kind isn't yet directly renderable), and handed to
// renderValueTransformFromSource.
//
// Supported ops:
//   - parser.ADD: identity. Returns the child's RenderedQuery unchanged;
//     TagsExpr and ValueExpr pass through.
//   - parser.SUB: wraps child with a value-transform outer SELECT that
//     negates value and drops __name__.
//
// Scalar literal fast path: when the child is a *ScalarLiteralPlan,
// fold the op into the literal value and emit the resulting synthetic
// scalar series directly via renderScalarLiteralFragment.
//
// Double-negation: the opt.constantFoldUnaryNegation pass runs on every
// production query (local/planner.go), so -(-x) reaches Lower already
// folded to x. Tests that bypass opt may observe a double-wrap here;
// that is expected.
//
// Operator outside {ADD, SUB}: errUnsupportedLowerNode so the caller
// falls back to the next execution tier.
func lowerUnary(ctx LoweringCtx, n *logicalpkg.UnaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerUnary called with nil")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: unary missing child node")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: unary missing logical analysis")
	}
	if lit, ok := n.Child.(*logicalpkg.ScalarLiteralPlan); ok {
		folded, ok := foldUnaryScalarLiteralLogical(n.Op, lit.Value)
		if !ok {
			return RenderedQuery{}, errUnsupportedLowerNode
		}
		rf, err := renderScalarLiteralFragment(folded, ctx.Params)
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(rf)
	}
	switch n.Op {
	case parser.ADD:
		return Lower(ctx, n.Child)
	case parser.SUB:
		childRQ, err := Lower(ctx, n.Child)
		if err != nil {
			return RenderedQuery{}, err
		}
		rf, err := renderValueTransformFromSource(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "-({value})", "", true, ctx.Params)
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(rf)
	default:
		return RenderedQuery{}, errUnsupportedLowerNode
	}
}

// foldUnaryScalarLiteralLogical mirrors native.foldUnaryScalarLiteral.
// Duplicated here so the renderer's direct-render path does not depend
// on the unexported native helper; both return the same constants for
// ADD (identity) and SUB (negate), and reject every other op.
func foldUnaryScalarLiteralLogical(op parser.ItemType, value float64) (float64, bool) {
	switch op {
	case parser.ADD:
		return value, true
	case parser.SUB:
		return -value, true
	default:
		return 0, false
	}
}
