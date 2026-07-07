package physical

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func TestChooseRangeInstantSelector(t *testing.T) {
	tests := []struct {
		name string
		in   RangeInstantSelectorInput
		want storage.RangeInstantSelectorStrategy
	}{
		{
			name: "default ASOF",
			in: RangeInstantSelectorInput{
				SelectorKind: storage.SelectorKindInstantVector,
				LookbackMS:   60_000,
				StepMS:       300_000,
			},
			want: storage.RangeInstantSelectorStrategyASOFJoin,
		},
		{
			name: "requested bucketed argMax when sparse timing is eligible",
			in: RangeInstantSelectorInput{
				RequestedStrategy: storage.RangeInstantSelectorStrategyBucketedArgMax,
				SelectorKind:      storage.SelectorKindInstantVector,
				LookbackMS:        60_000,
				StepMS:            300_000,
			},
			want: storage.RangeInstantSelectorStrategyBucketedArgMax,
		},
		{
			name: "range-vector selector cannot use bucketed argMax",
			in: RangeInstantSelectorInput{
				RequestedStrategy: storage.RangeInstantSelectorStrategyBucketedArgMax,
				SelectorKind:      storage.SelectorKindRangeVector,
				LookbackMS:        60_000,
				StepMS:            300_000,
			},
			want: storage.RangeInstantSelectorStrategyASOFJoin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ChooseRangeInstantSelector(tt.in)
			if decision.Strategy != tt.want {
				t.Fatalf("strategy = %q, want %q", decision.Strategy, tt.want)
			}
			if decision.Reason == "" {
				t.Fatalf("expected decision reason")
			}
		})
	}
}

func TestChooseRangeWindowAggregate(t *testing.T) {
	tests := []struct {
		name string
		in   RangeWindowAggregateInput
		want RangeWindowAggregateStrategy
	}{
		{
			name: "max_over_time non-overlap uses sparse direct aggregate",
			in: RangeWindowAggregateInput{
				Func:       "max_over_time",
				LookbackMS: 3_600_000,
				StepMS:     3_600_000,
			},
			want: RangeWindowAggregateStrategySparseDirectAggregate,
		},
		{
			name: "max_over_time high overlap uses existing windowed-array fallback",
			in: RangeWindowAggregateInput{
				Func:       "max_over_time",
				LookbackMS: 3_600_000,
				StepMS:     300_000,
			},
			want: RangeWindowAggregateStrategyDefault,
		},
		{
			name: "avg_over_time non-overlap uses sparse direct aggregate",
			in: RangeWindowAggregateInput{
				Func:                        "avg_over_time",
				LookbackMS:                  3_600_000,
				StepMS:                      3_600_000,
				EnableCumulativeAvgOverTime: true,
			},
			want: RangeWindowAggregateStrategySparseDirectAggregate,
		},
		{
			name: "avg_over_time high overlap uses cumulative avg when enabled",
			in: RangeWindowAggregateInput{
				Func:                        "avg_over_time",
				LookbackMS:                  3_600_000,
				StepMS:                      60_000,
				EnableCumulativeAvgOverTime: true,
			},
			want: RangeWindowAggregateStrategyCumulativeAvg,
		},
		{
			name: "explicit supported direct aggregate preference wins",
			in: RangeWindowAggregateInput{
				Func:       "avg_over_time",
				LookbackMS: 3_600_000,
				StepMS:     60_000,
				Preferences: PreferRangeWindowAggregateStrategy(PlanPreferences{},
					RangeWindowAggregateStrategyDirectAggregate),
				EnableCumulativeAvgOverTime: true,
			},
			want: RangeWindowAggregateStrategyDirectAggregate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ChooseRangeWindowAggregate(tt.in)
			if decision.Strategy != tt.want {
				t.Fatalf("strategy = %q, want %q (%s)", decision.Strategy, tt.want, decision.Reason)
			}
			if decision.Reason == "" {
				t.Fatalf("expected decision reason")
			}
		})
	}
}

func TestNativeGridAndSparseRateGuards(t *testing.T) {
	if !CanUseSparseDirectRateBuckets("rate", 3_600_000, 0, 3_600_000) {
		t.Fatalf("expected non-overlap rate window to use sparse direct rate buckets")
	}
	if CanUseSparseDirectRateBuckets("rate", 300_000, 0, 60_000) {
		t.Fatalf("expected overlapping rate window to reject sparse direct rate buckets")
	}
	// Correctness guard, not a heuristic: the sparse direct-rate SQL anchors
	// its extrapolation factor at the unshifted eval_ts (issue #36). Offset
	// windows must stay rejected until that anchor is offset-shifted.
	if CanUseSparseDirectRateBuckets("rate", 3_600_000, 60_000, 3_600_000) {
		t.Fatalf("expected offset rate window to reject sparse direct rate buckets")
	}
	if !CanUseNativeGridRangeFunction("rate", 300_000, 0) {
		t.Fatalf("expected 5m zero-offset rate to allow native-grid range function")
	}
	if CanUseNativeGridRangeFunction("rate", 30_000, 0) {
		t.Fatalf("expected short rate window to reject native-grid range function")
	}
	if CanUseNativeGridRangeFunction("rate", 300_000, 60_000) {
		t.Fatalf("expected offset rate window to reject native-grid range function")
	}
}

func TestChooseFusedRangeAggregation(t *testing.T) {
	nativeGrid := ChooseFusedRangeAggregation(FusedRangeAggregationInput{
		IsSumAggregation:          true,
		EnableNativeGridFunctions: true,
		IsMatrixSelectorLeaf:      true,
		IsIdentitySelectorInput:   true,
		Func:                      "rate",
		LookbackMS:                300_000,
		StepMS:                    60_000,
	})
	if nativeGrid.Strategy != FusedRangeAggregationStrategyNativeGridSumAggregation {
		t.Fatalf("native-grid fused strategy = %q, want %q (%s)", nativeGrid.Strategy, FusedRangeAggregationStrategyNativeGridSumAggregation, nativeGrid.Reason)
	}

	sparseRate := ChooseFusedRangeAggregation(FusedRangeAggregationInput{
		IsSumAggregation:          true,
		EnableNativeGridFunctions: true,
		IsMatrixSelectorLeaf:      true,
		IsIdentitySelectorInput:   true,
		Func:                      "rate",
		LookbackMS:                3_600_000,
		StepMS:                    3_600_000,
	})
	if sparseRate.Strategy != FusedRangeAggregationStrategyDefault {
		t.Fatalf("sparse-rate fused strategy = %q, want fallback", sparseRate.Strategy)
	}
	if sparseRate.Reason == "" {
		t.Fatalf("expected sparse-rate fallback reason")
	}
}

func TestChooseRangeFunctionRows(t *testing.T) {
	sparseRate := ChooseRangeFunctionRows(RangeFunctionRowsInput{
		EnableNativeGridFunctions: true,
		IsIdentitySelectorInput:   true,
		Func:                      "rate",
		LookbackMS:                3_600_000,
		StepMS:                    3_600_000,
	})
	if sparseRate.Strategy != RangeFunctionRowsStrategySparseDirectRateAggregation {
		t.Fatalf("range rows strategy = %q, want sparse direct rate", sparseRate.Strategy)
	}

	nativeGrid := ChooseRangeFunctionRows(RangeFunctionRowsInput{
		EnableNativeGridFunctions: true,
		IsIdentitySelectorInput:   true,
		Func:                      "rate",
		LookbackMS:                300_000,
		StepMS:                    60_000,
	})
	if nativeGrid.Strategy != RangeFunctionRowsStrategyNativeGridRows {
		t.Fatalf("range rows strategy = %q, want native-grid rows", nativeGrid.Strategy)
	}

	windowedArrays := ChooseRangeFunctionRows(RangeFunctionRowsInput{
		EnableNativeGridFunctions: true,
		Func:                      "rate",
		LookbackMS:                300_000,
		StepMS:                    60_000,
	})
	if windowedArrays.Strategy != RangeFunctionRowsStrategyWindowedArrays {
		t.Fatalf("range rows strategy = %q, want windowed arrays", windowedArrays.Strategy)
	}
}
