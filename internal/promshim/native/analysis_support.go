package native

import (
	"github.com/prometheus/prometheus/promql/parser"
)

func isSupportedNativeSyntheticScalarBuiltin(name string) bool {
	switch name {
	case "pi", "time":
		return true
	default:
		return false
	}
}

func isSupportedNativeSyntheticDateFunction(name string) bool {
	switch name {
	case "minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return true
	default:
		return false
	}
}

func isSupportedNativeAggregateOverTime(name string) bool {
	switch name {
	case "last_over_time", "first_over_time", "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "count_over_time",
		"stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time", "ts_of_first_over_time", "ts_of_last_over_time", "ts_of_max_over_time", "ts_of_min_over_time":
		return true
	default:
		return false
	}
}

// RangeFunctionPreservesMetricName reports whether the range function returns a
// sample from the original series (preserving __name__) rather than a derived
// aggregation. last_over_time and first_over_time behave like selectors in this
// respect; everything else (rate/increase/delta/*_over_time/deriv/...) drops
// the metric name because the returned value is a new quantity.
func RangeFunctionPreservesMetricName(name string) bool {
	return name == "last_over_time" || name == "first_over_time"
}

func isSupportedNativeCounterRangeFunction(name string) bool {
	switch name {
	case "rate", "irate", "increase", "delta", "idelta", "changes", "deriv":
		return true
	default:
		return false
	}
}

func IsSelectionNativeAggregation(op parser.ItemType) bool {
	switch op {
	case parser.TOPK, parser.BOTTOMK, parser.LIMITK, parser.LIMIT_RATIO:
		return true
	default:
		return false
	}
}

func isSupportedNativeAggregation(op parser.ItemType) bool {
	switch op {
	case parser.SUM, parser.COUNT, parser.MIN, parser.MAX, parser.AVG, parser.STDDEV, parser.STDVAR, parser.QUANTILE, parser.GROUP, parser.TOPK, parser.BOTTOMK, parser.COUNT_VALUES, parser.LIMITK, parser.LIMIT_RATIO:
		return true
	default:
		return false
	}
}

// IsSupportedNativeRangeModeForDirectSelectorFromInfo reports whether a
// RangeFunction subtree's child is a direct leaf selector over a range
// vector — the precondition for the direct-selector range-mode native
// rendering path.
func IsSupportedNativeRangeModeForDirectSelectorFromInfo(info *LoweringInfo) bool {
	if info == nil || info.SubtreeShape != SubtreeShapeRangeFunction || len(info.Children) == 0 {
		return false
	}
	child := info.Children[0]
	if child == nil || child.SubtreeShape != SubtreeShapeLeafSource {
		return false
	}
	if child.LeafSelector == nil {
		return false
	}
	return child.LeafSelector.Kind == SelectorKindRangeVector
}

// IsSupportedNativeRangeModeForAggregateOverTimeSubqueryFromInfo reports
// whether a RangeFunction subtree's child is a subquery feeding an
// aggregate-over-time range function.
func IsSupportedNativeRangeModeForAggregateOverTimeSubqueryFromInfo(info *LoweringInfo) bool {
	if !HasRangeFunctionFragmentFromInfo(info) {
		return false
	}
	child := info.Children[0]
	if child == nil || child.SubtreeShape != SubtreeShapeSubquery {
		return false
	}
	// The range function name has to be an aggregate-over-time variant.
	// We derive the function name from info.Expr's leading token by
	// falling back to a prefix match against the supported list. Callers
	// of this helper always pass RangeFunction-shaped info whose
	// nodeType is the func name, so we probe NodeType directly.
	return isSupportedNativeAggregateOverTime(info.NodeType)
}

// IsSupportedNativeRangeModeForWindowedArraysSubqueryFromInfo reports
// whether a RangeFunction subtree's child is a subquery feeding the
// windowed-arrays source (aggregate-over-time plus predict_linear /
// resets / quantile_over_time / double_exponential_smoothing /
// holt_winters).
func IsSupportedNativeRangeModeForWindowedArraysSubqueryFromInfo(info *LoweringInfo) bool {
	if !HasRangeFunctionFragmentFromInfo(info) {
		return false
	}
	child := info.Children[0]
	if child == nil || child.SubtreeShape != SubtreeShapeSubquery {
		return false
	}
	name := info.NodeType
	if isSupportedNativeAggregateOverTime(name) {
		return true
	}
	switch name {
	case "predict_linear", "quantile_over_time", "resets", "double_exponential_smoothing", "holt_winters":
		return true
	default:
		return false
	}
}

// IsSupportedNativeRangeModeForCounterSubqueryFromInfo reports whether
// a RangeFunction subtree's child is a subquery feeding a counter-style
// range function.
func IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info *LoweringInfo) bool {
	if !HasRangeFunctionFragmentFromInfo(info) {
		return false
	}
	child := info.Children[0]
	if child == nil || child.SubtreeShape != SubtreeShapeSubquery {
		return false
	}
	return isSupportedNativeCounterRangeFunction(info.NodeType)
}

// HasRangeFunctionFragmentFromInfo reports whether info carries a
// range-function shape — the precondition that the range-mode
// support predicates assume.
func HasRangeFunctionFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.SubtreeShape == SubtreeShapeRangeFunction
}

// HasAbsentFragmentFromInfo reports whether info carries an Absent
// shape — the precondition tier-3 absent plan construction assumes.
func HasAbsentFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.SubtreeShape == SubtreeShapeAbsent
}

// HasSubqueryFragmentFromInfo reports whether info carries a Subquery
// shape — the precondition tier-3 subquery plan construction assumes.
func HasSubqueryFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.SubtreeShape == SubtreeShapeSubquery
}

// HasHistogramFunctionFragmentFromInfo reports whether info carries a
// HistogramFunction shape.
func HasHistogramFunctionFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.SubtreeShape == SubtreeShapeHistogramFunction
}

// HistogramFunctionNameFromInfo returns the histogram function name
// carried by info's HistogramFunction shape, or empty string when the
// shape is missing.
func HistogramFunctionNameFromInfo(info *LoweringInfo) string {
	if !HasHistogramFunctionFragmentFromInfo(info) {
		return ""
	}
	return info.HistogramFunc
}

// HasHistogramProjectionFragmentFromInfo reports whether info carries a
// HistogramProjection shape.
func HasHistogramProjectionFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.SubtreeShape == SubtreeShapeHistogramProjection
}

// HasAggregationFragmentFromInfo reports whether info carries an
// Aggregation shape.
func HasAggregationFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.SubtreeShape == SubtreeShapeAggregation
}
