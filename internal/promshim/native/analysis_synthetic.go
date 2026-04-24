package native

import (
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

// isSyntheticScalarInfo reports whether info is a synthetic scalar
// series for a supported builtin ("time" / "pi"). It drives BinaryPlan
// Analyze's synthetic-scalar/vector fast paths.
func isSyntheticScalarInfo(info *LoweringInfo) bool {
	if info == nil || info.SyntheticSeries == nil {
		return false
	}
	if info.SubtreeShape != SubtreeShapeSyntheticSeries {
		return false
	}
	if info.OutputKind != OutputKindScalar {
		return false
	}
	return isSupportedNativeSyntheticScalarBuiltin(info.SyntheticSeries.Func)
}

func syntheticScalarValueTemplate(name string) (string, bool) {
	switch name {
	case "time":
		return "toFloat64(toUnixTimestamp64Milli({timestamp})) / 1000.0", true
	case "pi":
		return "toFloat64(3.141592653589793)", true
	default:
		return "", false
	}
}

func applySyntheticScalarChildTransform(op parser.ItemType, returnBool bool, syntheticFunc string, childOutputKind OutputKind, childLineage LabelLineage, scalarOnLeft bool) (valueTransformResult, bool) {
	scalarExpr, ok := syntheticScalarValueTemplate(syntheticFunc)
	if !ok {
		return valueTransformResult{}, false
	}
	return applyScalarExprChildTransform(op, returnBool, scalarExpr, childOutputKind, childLineage, scalarOnLeft)
}

func tagsExprForMetricDrop(dropMetric bool) string {
	if !dropMetric {
		return "{tags}"
	}
	return "arrayFilter(tag -> tag.1 != '__name__', {tags})"
}

func wrapValueExpr(expr string) string {
	return "(" + expr + ")"
}

func composePointwiseSourceTemplate(template, childValueExpr string) string {
	if childValueExpr == "" || childValueExpr == "{value}" {
		return template
	}
	return strings.NewReplacer("{value}", wrapValueExpr(childValueExpr)).Replace(template)
}
