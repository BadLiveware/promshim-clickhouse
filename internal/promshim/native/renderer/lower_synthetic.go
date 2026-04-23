package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
)

// lowerPointwiseFunction lowers a PointwiseFunctionPlan whose Func is a
// zero-arg synthetic date/time function (minute, hour, day_of_week,
// day_of_month, day_of_year, days_in_month, month, year) to a synthetic
// SELECT. Non-synthetic Funcs (including synthetic names with a child
// expression, e.g. minute(vec)) return errUnsupportedLowerNode so the
// caller falls back to Fragment rendering for the whole query.
//
// SQL output is byte-identical to the FragmentKindSyntheticSeries path
// in renderSyntheticFragment — the lowerer constructs the same
// SyntheticSeriesFragment shape and reuses the existing renderer so
// whitespace, column order, and parameter naming stay in lockstep.
func lowerPointwiseFunction(ctx LoweringCtx, n *logicalpkg.PointwiseFunctionPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerPointwiseFunction called with nil node")
	}
	// Synthetic lowering only applies to zero-arg synthetic date
	// functions. If the plan carries a child or param children, it's a
	// unary source-expression shape (e.g. abs(foo)) or a clamp/round
	// variant — not part of this surface. Fall back to Fragment.
	if n.Child != nil || !isSupportedNativeSyntheticDateFuncName(n.Func) {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if len(n.ParamChildren) != 0 {
		return RenderedQuery{}, fmt.Errorf("renderer: synthetic pointwise %q must not carry param children", n.Func)
	}
	fragment := &native.NativeFragment{
		Kind:        native.FragmentKindSyntheticSeries,
		OutputKind:  native.OutputKindInstantVector,
		DropsMetric: true,
		Synthetic:   &native.SyntheticSeriesFragment{Func: n.Func},
	}
	rf, err := renderSyntheticFragment(fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// isSupportedNativeSyntheticDateFuncName mirrors
// native.isSupportedNativeSyntheticDateFunction; duplicated here so the
// renderer package does not depend on the unexported helper. Keep in
// sync with analysis_support.go until the native package is retired.
func isSupportedNativeSyntheticDateFuncName(name string) bool {
	switch name {
	case "minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return true
	default:
		return false
	}
}
