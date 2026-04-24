package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// lowerBinaryScalarInvolving lowers a BinaryPlan where at least one side
// is DomainScalar (scalar-vector, vector-scalar, or scalar-scalar). It
// materializes the NativeFragment transitionally and delegates to
// RenderFragment, matching the Phase-A lowerer shape (histogram, range,
// aggregation).
//
// This is the Phase-B transitional extraction: the scalar-involving
// branch of lowerBinary moves out of lower.go into its own file so that
// lower.go itself contains zero native.BuildFragment calls. The
// internal BuildFragment call here retires in Phase C (Task 13) along
// with the four Phase-A logical renderers.
func lowerBinaryScalarInvolving(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryScalarInvolving called with nil")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary missing logical analysis")
	}
	fragment, err := native.BuildFragment(n, ctx.NativeAnalysis)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}
