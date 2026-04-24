package native

import (
	"math"

	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// IsSupportedNativeVectorJoinOp is the exported form of
// isSupportedNativeVectorJoinOp, used by the renderer's direct-render
// BinaryVectorJoin path to gate on operator+matching support without
// bridging through NativeAnalysis.
func IsSupportedNativeVectorJoinOp(op parser.ItemType, matching *parser.VectorMatching) bool {
	return isSupportedNativeVectorJoinOp(op, matching)
}

// SupportedNativeVectorJoinShape is the exported form of
// supportedNativeVectorJoinShape, used by the renderer's direct-render
// BinaryVectorJoin path to derive JoinShape from the logical VectorMatching.
func SupportedNativeVectorJoinShape(matching *parser.VectorMatching) (string, bool) {
	return supportedNativeVectorJoinShape(matching)
}

func isSupportedNativeVectorJoinOp(op parser.ItemType, matching *parser.VectorMatching) bool {
	normalized := normalizeVectorMatching(matching)
	if isSetOperator(op) {
		return normalized.Card == parser.CardManyToMany
	}
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
	case parser.CardManyToMany:
		return JoinShapeManyToMany, true
	default:
		return "", false
	}
}

func nativeVectorJoinDropsMetricName(op parser.ItemType, returnBool bool) bool {
	if isSetOperator(op) {
		return false
	}
	return !isComparisonBinaryOperator(op) || returnBool
}

func nativeVectorJoinLabelLineage(lhs, rhs LabelLineage, matching *parser.VectorMatching, op parser.ItemType, returnBool bool) LabelLineage {
	normalized := normalizeVectorMatching(matching)
	if isSetOperator(op) {
		if op == parser.LOR {
			return unknownLineage()
		}
		return passthroughLabelLineage(lhs)
	}
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

// valueTransformResult captures the analysis-side outputs of an
// apply*Transform helper. Each helper populates an info.ValueTransform
// view, the DropsMetric flag, an optional RuntimeValueTransform, and
// the resulting LabelLineage so the Analyze caller can flip those onto
// the LoweringInfo.
type valueTransformResult struct {
	View       ValueTransformView
	OutputKind OutputKind
	DropsMetric bool
	Runtime    *RuntimeValueTransform
	Lineage    LabelLineage
}

func applyUnaryValueTransform(op parser.ItemType, childOutputKind OutputKind, childDropsMetric bool, childLineage LabelLineage) (valueTransformResult, bool) {
	if childOutputKind != OutputKindInstantVector && childOutputKind != OutputKindScalar {
		return valueTransformResult{}, false
	}
	valueExpr, dropsMetric, ok := applyUnarySourceTransform(op, "{value}", childDropsMetric)
	if !ok {
		return valueTransformResult{}, false
	}
	lineage := passthroughLabelLineage(childLineage)
	if dropsMetric {
		lineage = withMetricNameState(lineage, LabelLineageDropped)
	}
	return valueTransformResult{
		View: ValueTransformView{
			ValueExpr:   valueExpr,
			DropsMetric: dropsMetric,
		},
		OutputKind:  childOutputKind,
		DropsMetric: dropsMetric,
		Lineage:     lineage,
	}, true
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
			return promQLModuloExpr(scalarExpr, valueExpr), true, true
		}
		return promQLModuloExpr(valueExpr, scalarExpr), true, true
	case parser.POW:
		if scalarOnLeft {
			return "pow(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "pow(" + valueExpr + ", " + scalarExpr + ")", true, true
	default:
		return "", false, false
	}
}

func applyRoundValueTransform(childOutputKind OutputKind, childLineage LabelLineage, toNearest float64) (valueTransformResult, bool) {
	if childOutputKind != OutputKindInstantVector {
		return valueTransformResult{}, false
	}
	if toNearest == 0 || math.IsNaN(toNearest) || math.IsInf(toNearest, 0) {
		return valueTransformResult{}, false
	}
	multiplier := storage.NativeFloatLiteral(toNearest)
	valueExpr := "if(isNaN({value}) OR isInfinite({value}), {value}, if((({value}) / " + multiplier + ") >= 0, floor((({value}) / " + multiplier + ") + 0.5), ceil((({value}) / " + multiplier + ") - 0.5)) * " + multiplier + ")"
	lineage := withMetricNameState(passthroughLabelLineage(childLineage), LabelLineageDropped)
	return valueTransformResult{
		View: ValueTransformView{
			ValueExpr:   valueExpr,
			DropsMetric: true,
		},
		OutputKind:  OutputKindInstantVector,
		DropsMetric: true,
		Lineage:     lineage,
	}, true
}

func applyScalarValueTransform(op parser.ItemType, vectorOutputKind OutputKind, vectorLineage LabelLineage, scalar float64, scalarOnLeft bool) (valueTransformResult, bool) {
	if vectorOutputKind != OutputKindInstantVector && vectorOutputKind != OutputKindScalar {
		return valueTransformResult{}, false
	}
	valueExpr, dropsMetric, ok := buildBinaryTemplateForScalarExpr(op, storage.NativeFloatLiteral(scalar), "{value}", scalarOnLeft)
	if !ok {
		return valueTransformResult{}, false
	}
	var runtimeTransform *RuntimeValueTransform
	if op == parser.MOD {
		runtimeScalar := scalar
		runtimeTransform = &RuntimeValueTransform{Op: RuntimeValueTransformPromQLModulo, Scalar: &runtimeScalar, ScalarOnLeft: scalarOnLeft}
		valueExpr = "{value}"
	}
	lineage := withMetricNameState(passthroughLabelLineage(vectorLineage), boolState(dropsMetric, LabelLineageDropped, vectorLineage.MetricName))
	return valueTransformResult{
		View: ValueTransformView{
			ValueExpr:   valueExpr,
			DropsMetric: dropsMetric,
		},
		OutputKind:  vectorOutputKind,
		DropsMetric: dropsMetric,
		Runtime:     runtimeTransform,
		Lineage:     lineage,
	}, true
}

func applyComparisonBoolTransform(op parser.ItemType, returnBool bool, vectorOutputKind OutputKind, vectorLineage LabelLineage, scalar float64, scalarOnLeft bool) (valueTransformResult, bool) {
	if !returnBool || (vectorOutputKind != OutputKindInstantVector && vectorOutputKind != OutputKindScalar) || !isComparisonBinaryOperator(op) {
		return valueTransformResult{}, false
	}
	filter, ok := comparisonFilterTemplate(op, scalar, scalarOnLeft)
	if !ok {
		return valueTransformResult{}, false
	}
	lineage := unknownLineage()
	if vectorOutputKind == OutputKindInstantVector {
		lineage = withMetricNameState(passthroughLabelLineage(vectorLineage), LabelLineageDropped)
	}
	return valueTransformResult{
		View: ValueTransformView{
			ValueExpr:   "if(" + filter + ", 1.0, 0.0)",
			DropsMetric: true,
		},
		OutputKind:  vectorOutputKind,
		DropsMetric: true,
		Lineage:     lineage,
	}, true
}

func applyComparisonFilterTransform(op parser.ItemType, returnBool bool, vectorOutputKind OutputKind, vectorLineage LabelLineage, scalar float64, scalarOnLeft bool) (valueTransformResult, bool) {
	if !isComparisonBinaryOperator(op) {
		return valueTransformResult{}, false
	}
	if returnBool {
		return valueTransformResult{}, false
	}
	if vectorOutputKind != OutputKindInstantVector {
		return valueTransformResult{}, false
	}
	filter, ok := comparisonFilterTemplate(op, scalar, scalarOnLeft)
	if !ok {
		return valueTransformResult{}, false
	}
	lineage := passthroughLabelLineage(vectorLineage)
	return valueTransformResult{
		View: ValueTransformView{
			ValueExpr:  "{value}",
			FilterExpr: filter,
		},
		OutputKind:  OutputKindInstantVector,
		DropsMetric: false,
		Lineage:     lineage,
	}, true
}

func comparisonFilterTemplate(op parser.ItemType, scalar float64, scalarOnLeft bool) (string, bool) {
	return comparisonFilterExprTemplate(op, storage.NativeFloatLiteral(scalar), scalarOnLeft)
}

func comparisonFilterExprTemplate(op parser.ItemType, scalarExpr string, scalarOnLeft bool) (string, bool) {
	opSym, ok := comparisonOperatorSymbol(op)
	if !ok {
		return "", false
	}
	scalarExpr = wrapValueExpr(scalarExpr)
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

func promQLModuloExpr(left, right string) string {
	left = wrapValueExpr(left)
	right = wrapValueExpr(right)
	absRight := "abs(" + right + ")"
	positiveMod := "positiveModulo(" + left + ", " + absRight + ")"
	negativeMod := "-positiveModulo(-(" + left + "), " + absRight + ")"
	return "if(isNaN(" + left + ") OR isNaN(" + right + ") OR " + right + " = 0 OR isInfinite(" + left + "), nan, if(isInfinite(" + right + "), " + left + ", if(" + left + " < 0, " + negativeMod + ", " + positiveMod + ")))"
}

func applyScalarExprChildTransform(op parser.ItemType, returnBool bool, scalarExpr string, childOutputKind OutputKind, childLineage LabelLineage, scalarOnLeft bool) (valueTransformResult, bool) {
	if childOutputKind != OutputKindInstantVector && childOutputKind != OutputKindScalar {
		return valueTransformResult{}, false
	}
	if isComparisonBinaryOperator(op) {
		filter, ok := comparisonFilterExprTemplate(op, scalarExpr, scalarOnLeft)
		if !ok {
			return valueTransformResult{}, false
		}
		if returnBool {
			lineage := unknownLineage()
			if childOutputKind == OutputKindInstantVector {
				lineage = withMetricNameState(passthroughLabelLineage(childLineage), LabelLineageDropped)
			}
			return valueTransformResult{
				View: ValueTransformView{
					ValueExpr:   "if(" + filter + ", 1.0, 0.0)",
					DropsMetric: true,
				},
				OutputKind:  childOutputKind,
				DropsMetric: true,
				Lineage:     lineage,
			}, true
		}
		if childOutputKind != OutputKindInstantVector {
			return valueTransformResult{}, false
		}
		return valueTransformResult{
			View: ValueTransformView{
				ValueExpr:  "{value}",
				FilterExpr: filter,
			},
			OutputKind:  OutputKindInstantVector,
			DropsMetric: false,
			Lineage:     passthroughLabelLineage(childLineage),
		}, true
	}
	template, dropsMetric, ok := buildBinaryTemplateForScalarExpr(op, scalarExpr, "{value}", scalarOnLeft)
	if !ok {
		return valueTransformResult{}, false
	}
	lineage := unknownLineage()
	if childOutputKind == OutputKindInstantVector {
		lineage = withMetricNameState(passthroughLabelLineage(childLineage), boolState(dropsMetric, LabelLineageDropped, childLineage.MetricName))
	}
	return valueTransformResult{
		View: ValueTransformView{
			ValueExpr:   template,
			DropsMetric: dropsMetric,
		},
		OutputKind:  childOutputKind,
		DropsMetric: dropsMetric,
		Lineage:     lineage,
	}, true
}

func buildBinaryTemplateForScalarExpr(op parser.ItemType, scalarExpr, valueExpr string, scalarOnLeft bool) (string, bool, bool) {
	left := "(" + scalarExpr + ")"
	right := "(" + valueExpr + ")"
	if !scalarOnLeft {
		left = "(" + valueExpr + ")"
		right = "(" + scalarExpr + ")"
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
		return promQLModuloExpr(left, right), true, true
	case parser.POW:
		return "pow(" + left + ", " + right + ")", true, true
	default:
		return "", false, false
	}
}
