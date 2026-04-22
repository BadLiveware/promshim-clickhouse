package native

import (
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

func isSyntheticScalarFragment(fragment *NativeFragment) bool {
	if fragment == nil || fragment.Synthetic == nil {
		return false
	}
	if fragment.Kind != FragmentKindSyntheticSeries {
		return false
	}
	return fragment.OutputKind == OutputKindScalar && isSupportedNativeSyntheticScalarBuiltin(fragment.Synthetic.Func)
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

func applySyntheticScalarChildTransform(op parser.ItemType, returnBool bool, syntheticFunc string, childFragment *NativeFragment, childLineage LabelLineage, scalarOnLeft bool) (*NativeFragment, LabelLineage, bool) {
	if childFragment == nil {
		return nil, LabelLineage{}, false
	}
	scalarExpr, ok := syntheticScalarValueTemplate(syntheticFunc)
	if !ok {
		return nil, LabelLineage{}, false
	}
	return applyScalarExprChildTransform(op, returnBool, scalarExpr, childFragment, childLineage, scalarOnLeft)
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
