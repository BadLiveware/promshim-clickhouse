package native

import (
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func isSupportedNativeVectorJoinOp(op parser.ItemType, matching *parser.VectorMatching) bool {
	if isSetOperator(op) {
		return false
	}
	normalized := normalizeVectorMatching(matching)
	if normalized.Card == parser.CardManyToMany {
		return false
	}
	switch op {
	case parser.ADD, parser.SUB, parser.MUL, parser.DIV, parser.MOD, parser.POW,
		parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func supportedNativeVectorJoinShape(matching *parser.VectorMatching) (string, bool) {
	normalized := normalizeVectorMatching(matching)
	switch normalized.Card {
	case parser.CardOneToOne:
		return JoinShapeOneToOne, true
	case parser.CardManyToOne:
		return JoinShapeManyToOne, true
	case parser.CardOneToMany:
		return JoinShapeOneToMany, true
	default:
		return "", false
	}
}

func nativeVectorJoinDropsMetricName(op parser.ItemType, returnBool bool) bool {
	return !isComparisonBinaryOperator(op) || returnBool
}

func nativeVectorJoinLabelLineage(lhs, rhs LabelLineage, matching *parser.VectorMatching, op parser.ItemType, returnBool bool) LabelLineage {
	normalized := normalizeVectorMatching(matching)
	result := passthroughLabelLineage(lhs)
	if normalized.Card == parser.CardOneToMany {
		result = passthroughLabelLineage(rhs)
	}
	if normalized.Card == parser.CardOneToOne {
		if normalized.On {
			kept := map[string]LabelLineageState{}
			for _, label := range normalized.MatchingLabels {
				if label == labels.MetricName {
					continue
				}
				if state, ok := result.Known[label]; ok {
					kept[label] = state
				}
			}
			result.Known = kept
			result.Wildcard = LabelLineageDropped
		} else {
			for _, label := range normalized.MatchingLabels {
				delete(result.Known, label)
			}
		}
	}
	for _, label := range normalized.Include {
		if label == labels.MetricName {
			continue
		}
		if state, ok := rhs.Known[label]; ok {
			result.Known[label] = state
		} else {
			result.Known[label] = LabelLineageCopied
		}
	}
	if nativeVectorJoinDropsMetricName(op, returnBool) {
		result.MetricName = LabelLineageDropped
		result.Known[labels.MetricName] = LabelLineageDropped
	}
	return result
}

func isComparisonBinaryOperator(op parser.ItemType) bool {
	switch op {
	case parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func isSetOperator(op parser.ItemType) bool {
	switch op {
	case parser.LAND, parser.LOR, parser.LUNLESS:
		return true
	default:
		return false
	}
}

func applyUnarySourceTransform(op parser.ItemType, valueExpr string, childDropsMetric bool) (string, bool, bool) {
	switch op {
	case parser.ADD:
		return valueExpr, childDropsMetric, true
	case parser.SUB:
		return "-" + wrapValueExpr(valueExpr), true, true
	default:
		return "", false, false
	}
}

func applyBinarySourceTransform(op parser.ItemType, valueExpr string, scalar float64, scalarOnLeft bool) (string, bool, bool) {
	valueExpr = wrapValueExpr(valueExpr)
	scalarExpr := storage.NativeFloatLiteral(scalar)

	switch op {
	case parser.ADD:
		if scalarOnLeft {
			return scalarExpr + " + " + valueExpr, true, true
		}
		return valueExpr + " + " + scalarExpr, true, true
	case parser.SUB:
		if scalarOnLeft {
			return scalarExpr + " - " + valueExpr, true, true
		}
		return valueExpr + " - " + scalarExpr, true, true
	case parser.MUL:
		if scalarOnLeft {
			return scalarExpr + " * " + valueExpr, true, true
		}
		return valueExpr + " * " + scalarExpr, true, true
	case parser.DIV:
		if scalarOnLeft {
			return scalarExpr + " / " + valueExpr, true, true
		}
		return valueExpr + " / " + scalarExpr, true, true
	case parser.MOD:
		if scalarOnLeft {
			return "modulo(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "modulo(" + valueExpr + ", " + scalarExpr + ")", true, true
	case parser.POW:
		if scalarOnLeft {
			return "pow(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "pow(" + valueExpr + ", " + scalarExpr + ")", true, true
	default:
		return "", false, false
	}
}

func applyComparisonFilterTransform(op parser.ItemType, returnBool bool, vectorFragment *NativeFragment, vectorLineage LabelLineage, scalar float64, scalarOnLeft bool) (*NativeFragment, LabelLineage, bool) {
	if vectorFragment == nil {
		return nil, LabelLineage{}, false
	}
	if !isComparisonBinaryOperator(op) {
		return nil, LabelLineage{}, false
	}
	if returnBool {
		return nil, LabelLineage{}, false
	}
	if vectorFragment.OutputKind != OutputKindInstantVector {
		return nil, LabelLineage{}, false
	}
	filter, ok := comparisonFilterTemplate(op, scalar, scalarOnLeft)
	if !ok {
		return nil, LabelLineage{}, false
	}
	lineage := passthroughLabelLineage(vectorLineage)
	return &NativeFragment{
		Kind:       FragmentKindValueTransform,
		OutputKind: OutputKindInstantVector,
		ValueTransform: &ValueTransformFragment{
			Child:      vectorFragment,
			ValueExpr:  "{value}",
			FilterExpr: filter,
		},
	}, lineage, true
}

func comparisonFilterTemplate(op parser.ItemType, scalar float64, scalarOnLeft bool) (string, bool) {
	opSym, ok := comparisonOperatorSymbol(op)
	if !ok {
		return "", false
	}
	scalarExpr := storage.NativeFloatLiteral(scalar)
	if scalarOnLeft {
		return scalarExpr + " " + opSym + " ({value})", true
	}
	return "({value}) " + opSym + " " + scalarExpr, true
}

func comparisonOperatorSymbol(op parser.ItemType) (string, bool) {
	switch op {
	case parser.EQLC:
		return "=", true
	case parser.NEQ:
		return "!=", true
	case parser.GTR:
		return ">", true
	case parser.LSS:
		return "<", true
	case parser.GTE:
		return ">=", true
	case parser.LTE:
		return "<=", true
	default:
		return "", false
	}
}

func buildBinaryTemplateForScalarExpr(op parser.ItemType, scalarExpr, valueExpr string, scalarOnLeft bool) (string, bool, bool) {
	left := scalarExpr
	right := "(" + valueExpr + ")"
	if !scalarOnLeft {
		left = "(" + valueExpr + ")"
		right = scalarExpr
	}
	switch op {
	case parser.ADD:
		return left + " + " + right, true, true
	case parser.SUB:
		return left + " - " + right, true, true
	case parser.MUL:
		return left + " * " + right, true, true
	case parser.DIV:
		return left + " / " + right, true, true
	case parser.MOD:
		return "modulo(" + left + ", " + right + ")", true, true
	case parser.POW:
		return "pow(" + left + ", " + right + ")", true, true
	default:
		return "", false, false
	}
}
