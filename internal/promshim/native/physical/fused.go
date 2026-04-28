package physical

type FusedRangeAggregationStrategy string

const (
	FusedRangeAggregationStrategyDefault                  FusedRangeAggregationStrategy = ""
	FusedRangeAggregationStrategyNativeGridSumAggregation FusedRangeAggregationStrategy = "native_grid_sum_aggregation"
)

type FusedRangeAggregationInput struct {
	IsSumAggregation          bool
	EnableNativeGridFunctions bool
	IsMatrixSelectorLeaf      bool
	IsIdentitySelectorInput   bool
	Func                      string
	LookbackMS                int64
	OffsetMS                  int64
	StepMS                    int64
}

type FusedRangeAggregationDecision struct {
	Strategy FusedRangeAggregationStrategy
	Reason   string
	Guards   []string
	Rejected []Alternative
}

func ChooseFusedRangeAggregation(input FusedRangeAggregationInput) FusedRangeAggregationDecision {
	if !input.IsSumAggregation {
		return fusedRangeAggregationFallback("requires sum aggregation", nil)
	}
	if !input.EnableNativeGridFunctions {
		return fusedRangeAggregationFallback("native-grid functions are disabled", nil)
	}
	if !input.IsMatrixSelectorLeaf {
		return fusedRangeAggregationFallback("requires matrix-selector leaf child", nil)
	}
	if !input.IsIdentitySelectorInput {
		return fusedRangeAggregationFallback("requires identity selector input", nil)
	}
	if CanUseSparseDirectRateBuckets(input.Func, input.LookbackMS, input.OffsetMS, input.StepMS) {
		return fusedRangeAggregationFallback("sparse direct rate aggregation is preferred for non-overlap rate windows", []Alternative{{Strategy: string(FusedRangeAggregationStrategyNativeGridSumAggregation), Reason: "sparse direct rate takes precedence for this timing"}})
	}
	if !CanUseNativeGridRangeFunction(input.Func, input.LookbackMS, input.OffsetMS) {
		return fusedRangeAggregationFallback("function or timing is not eligible for native-grid range aggregation", []Alternative{{Strategy: string(FusedRangeAggregationStrategyNativeGridSumAggregation), Reason: "native-grid range function guard failed"}})
	}
	return FusedRangeAggregationDecision{
		Strategy: FusedRangeAggregationStrategyNativeGridSumAggregation,
		Reason:   "sum aggregation over native-grid range function is eligible",
		Guards:   []string{"sum_aggregation", "native_grid_enabled", "matrix_selector_leaf", "identity_selector_input", "native_grid_range_function"},
	}
}

func fusedRangeAggregationFallback(reason string, rejected []Alternative) FusedRangeAggregationDecision {
	return FusedRangeAggregationDecision{
		Strategy: FusedRangeAggregationStrategyDefault,
		Reason:   reason,
		Guards:   []string{"row_oriented_aggregation_fallback"},
		Rejected: rejected,
	}
}

type RangeFunctionRowsStrategy string

const (
	RangeFunctionRowsStrategyWindowedArrays              RangeFunctionRowsStrategy = "windowed_arrays"
	RangeFunctionRowsStrategyNativeGridRows              RangeFunctionRowsStrategy = "native_grid_rows"
	RangeFunctionRowsStrategySparseDirectRateAggregation RangeFunctionRowsStrategy = "sparse_direct_rate_aggregation"
	RangeFunctionRowsStrategyRangeWindowAggregate        RangeFunctionRowsStrategy = "range_window_aggregate"
)

type RangeFunctionRowsInput struct {
	EnableNativeGridFunctions bool
	IsIdentitySelectorInput   bool
	Func                      string
	LookbackMS                int64
	OffsetMS                  int64
	StepMS                    int64
}

type RangeFunctionRowsDecision struct {
	Strategy RangeFunctionRowsStrategy
	Reason   string
	Guards   []string
	Rejected []Alternative
}

func ChooseRangeFunctionRows(input RangeFunctionRowsInput) RangeFunctionRowsDecision {
	if !input.IsIdentitySelectorInput {
		return RangeFunctionRowsDecision{
			Strategy: RangeFunctionRowsStrategyWindowedArrays,
			Reason:   "non-identity selector input must use windowed-array rows",
			Guards:   []string{"windowed_array_fallback"},
		}
	}
	if input.EnableNativeGridFunctions && !CanUseSparseDirectRateBuckets(input.Func, input.LookbackMS, input.OffsetMS, input.StepMS) && CanUseNativeGridRangeFunction(input.Func, input.LookbackMS, input.OffsetMS) {
		return RangeFunctionRowsDecision{
			Strategy: RangeFunctionRowsStrategyNativeGridRows,
			Reason:   "native-grid range rows are eligible",
			Guards:   []string{"identity_selector_input", "native_grid_enabled", "native_grid_range_function"},
		}
	}
	if CanUseSparseDirectRateBuckets(input.Func, input.LookbackMS, input.OffsetMS, input.StepMS) {
		return RangeFunctionRowsDecision{
			Strategy: RangeFunctionRowsStrategySparseDirectRateAggregation,
			Reason:   "non-overlap rate windows use sparse direct rate aggregation",
			Guards:   []string{"identity_selector_input", "rate", "zero_offset", "non_overlap_window"},
		}
	}
	return RangeFunctionRowsDecision{
		Strategy: RangeFunctionRowsStrategyRangeWindowAggregate,
		Reason:   "identity selector input can use range-window aggregate strategy selection",
		Guards:   []string{"identity_selector_input"},
	}
}
