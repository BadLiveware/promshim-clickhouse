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

func IsSupportedNativeRangeModeForDirectSelector(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindLeafSource && child.Selector != nil && child.Selector.Kind == SelectorKindRangeVector
}

func IsSupportedNativeRangeModeForAggregateOverTimeSubquery(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	if !isSupportedNativeAggregateOverTime(fragment.RangeFunction.Func) {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil
}

func IsSupportedNativeRangeModeForWindowedArraysSubquery(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	if !isSupportedNativeAggregateOverTime(fragment.RangeFunction.Func) && fragment.RangeFunction.Func != "predict_linear" && fragment.RangeFunction.Func != "quantile_over_time" && fragment.RangeFunction.Func != "resets" && fragment.RangeFunction.Func != "double_exponential_smoothing" && fragment.RangeFunction.Func != "holt_winters" {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil
}

func IsSupportedNativeRangeModeForCounterSubquery(fragment *NativeFragment) bool {
	if fragment == nil || fragment.RangeFunction == nil || fragment.RangeFunction.Child == nil {
		return false
	}
	if !isSupportedNativeCounterRangeFunction(fragment.RangeFunction.Func) {
		return false
	}
	child := fragment.RangeFunction.Child
	return child.Kind == FragmentKindSubquery && child.Subquery != nil && child.Subquery.Child != nil
}

// IsSupportedNativeRangeModeForDirectSelectorFromInfo wraps
// IsSupportedNativeRangeModeForDirectSelector so tier-3 construction
// dispatchers can gate on range-mode support without dereferencing
// info.Fragment. Returns false for nil info or nil info.Fragment.
func IsSupportedNativeRangeModeForDirectSelectorFromInfo(info *LoweringInfo) bool {
	if info == nil {
		return false
	}
	return IsSupportedNativeRangeModeForDirectSelector(info.Fragment)
}

// IsSupportedNativeRangeModeForAggregateOverTimeSubqueryFromInfo mirrors
// IsSupportedNativeRangeModeForAggregateOverTimeSubquery driven from
// LoweringInfo.
func IsSupportedNativeRangeModeForAggregateOverTimeSubqueryFromInfo(info *LoweringInfo) bool {
	if info == nil {
		return false
	}
	return IsSupportedNativeRangeModeForAggregateOverTimeSubquery(info.Fragment)
}

// IsSupportedNativeRangeModeForWindowedArraysSubqueryFromInfo mirrors
// IsSupportedNativeRangeModeForWindowedArraysSubquery driven from
// LoweringInfo.
func IsSupportedNativeRangeModeForWindowedArraysSubqueryFromInfo(info *LoweringInfo) bool {
	if info == nil {
		return false
	}
	return IsSupportedNativeRangeModeForWindowedArraysSubquery(info.Fragment)
}

// IsSupportedNativeRangeModeForCounterSubqueryFromInfo mirrors
// IsSupportedNativeRangeModeForCounterSubquery driven from LoweringInfo.
func IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info *LoweringInfo) bool {
	if info == nil {
		return false
	}
	return IsSupportedNativeRangeModeForCounterSubquery(info.Fragment)
}

// HasRangeFunctionFragmentFromInfo reports whether info carries a
// RangeFunction fragment — the precondition that the range-mode
// support predicates assume. Replaces the explicit
// `info.Fragment == nil || info.Fragment.RangeFunction == nil` guard
// at tier-3 range-like plan construction sites.
func HasRangeFunctionFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.Fragment != nil && info.Fragment.RangeFunction != nil
}

// HasAbsentFragmentFromInfo reports whether info carries an Absent
// fragment — the precondition tier-3 absent plan construction assumes.
func HasAbsentFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.Fragment != nil && info.Fragment.Absent != nil
}

// HasSubqueryFragmentFromInfo reports whether info carries a Subquery
// fragment with a populated Subquery payload — the precondition tier-3
// subquery plan construction assumes.
func HasSubqueryFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.Fragment != nil && info.Fragment.Subquery != nil
}

// HasHistogramFunctionFragmentFromInfo reports whether info carries a
// HistogramFunction fragment with a populated HistogramFunction payload.
func HasHistogramFunctionFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.Fragment != nil && info.Fragment.HistogramFunction != nil
}

// HistogramFunctionNameFromInfo returns the histogram function name
// carried by info's HistogramFunction fragment, or empty string when
// the fragment is missing.
func HistogramFunctionNameFromInfo(info *LoweringInfo) string {
	if !HasHistogramFunctionFragmentFromInfo(info) {
		return ""
	}
	return info.Fragment.HistogramFunction.Func
}

// HasHistogramProjectionFragmentFromInfo reports whether info carries a
// HistogramProjection fragment with a populated HistogramProjection
// payload.
func HasHistogramProjectionFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.Fragment != nil && info.Fragment.HistogramProjection != nil
}

// HasAggregationFragmentFromInfo reports whether info carries an
// Aggregation fragment with a populated Aggregation payload.
func HasAggregationFragmentFromInfo(info *LoweringInfo) bool {
	return info != nil && info.Fragment != nil && info.Fragment.Aggregation != nil
}

func isSupportedNativeRangeChildFragment(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindSubquery:
		return true
	default:
		return false
	}
}

func isSupportedNativeSubqueryChildFragment(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr, FragmentKindAggregation, FragmentKindBinaryVectorJoin, FragmentKindRangeFunction, FragmentKindSortTransform, FragmentKindLabelTransform, FragmentKindClampTransform, FragmentKindValueTransform:
		return true
	default:
		return false
	}
}

func isSupportedAggregationSourceFragment(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	switch fragment.Kind {
	case FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr, FragmentKindAggregation:
		return true
	default:
		return false
	}
}
