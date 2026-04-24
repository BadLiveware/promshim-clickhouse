package renderer

import (
	"math"

	"ch-observability/internal/promshim/storage"
)

// buildRoundValueExpr returns the ValueTransform template for PromQL
// round(v, toNearest). Must stay in sync with applyRoundValueTransform in
// native/analysis_binary.go; any drift between the two is a latent bug in
// the direct-render path. Returns ok=false when toNearest is 0, NaN, or
// +/-Inf.
func buildRoundValueExpr(toNearest float64) (string, bool) {
	if toNearest == 0 || math.IsNaN(toNearest) || math.IsInf(toNearest, 0) {
		return "", false
	}
	m := storage.NativeFloatLiteral(toNearest)
	return "if(isNaN({value}) OR isInfinite({value}), {value}, if((({value}) / " + m + ") >= 0, floor((({value}) / " + m + ") + 0.5), ceil((({value}) / " + m + ") - 0.5)) * " + m + ")", true
}
