package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// lowerVector delegates to Lower on the scalar child because vector()
// only flips OutputKind (a decode-time flag, not a SQL-text
// difference). VectorPlan promotes a scalar-producing child into an
// instant-vector output with a single series and no tags; the emitted
// SQL is byte-identical to whatever the child renders to. Locking that
// equivalence through recursion avoids constructing an intermediate
// NativeFragment and keeps the lowerer trivial.
//
// Hierarchical fallback: if the child isn't a direct-render surface
// yet, Lower returns errUnsupportedLowerNode and the caller falls back
// to the Fragment path for the whole query. Correctness is preserved
// because the sentinel bubbles up unchanged.
//
// Children currently direct-lowered through this recursion:
//
//   - *logicalpkg.ScalarLiteralPlan  (e.g. vector(1))
//   - *logicalpkg.ScalarBuiltinPlan  (e.g. vector(time()), vector(pi()))
//   - *logicalpkg.ScalarConvertPlan  (e.g. vector(scalar(sum(up))))
//
// Any other scalar child kind (e.g. scalar BinaryPlan) returns the
// sentinel from Lower and lands on the Fragment path.
func lowerVector(ctx LoweringCtx, n *logicalpkg.VectorPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerVector called with nil")
	}
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: vector missing child")
	}
	return Lower(ctx, n.Child)
}
