package exec

// Branch coverage for extrapolatedValue (rate.go), the shared
// extrapolatedRate port. Two branches are otherwise unexercised:
//   - the threshold-limited start clamp (durationToStart >= 1.1*avg):
//     when the first sample sits far inside the window, Prometheus caps the
//     start-side extrapolation at half the average scrape interval.
//   - the counter zero-crossing clamp (isCounter && delta > 0 &&
//     first.Value >= 0): the start extrapolation is further capped at the
//     implied zero crossing, and only for counters — the gauge (delta) path
//     must not apply it.
// Expected values are closed-form from the extrapolatedRate formula.

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestExtrapolationStartThresholdLimited(t *testing.T) {
	// Samples 500/510/520 in the window [0,1000]: avg interval 10s, so the
	// 1.1x threshold is 11s. Both durationToStart (500) and durationToEnd
	// (480) exceed it, so each clamps to avg/2 = 5s. Raw delta is 40 over a
	// 20s sampled interval; factor = (20+5+5)/20 = 1.5, giving 60.
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 500, Value: 1000},
			{Timestamp: 510, Value: 1020},
			{Timestamp: 520, Value: 1040},
		},
	}}}
	vector, err := ApplyDeltaWithBounds(matrix, 0, 1000)
	if err != nil {
		t.Fatalf("ApplyDeltaWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got := vector.Samples[0].Value; math.Abs(got-60.0) > 1e-9 {
		t.Errorf("expected threshold-limited delta 60, got %.6f", got)
	}
}

func TestExtrapolationCounterZeroCrossingClamp(t *testing.T) {
	// Counter 2/12/22 in the window [90,130]: avg interval 10s (threshold
	// 11s), so durationToStart (10s) is NOT threshold-limited. delta is 20,
	// so durationToZero = 20 * (2/20) = 2s < 10s and the start extrapolation
	// clamps to the zero crossing. durationToEnd (10s) stays. factor =
	// (20+2+10)/20 = 1.6, giving increase 32.
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 100, Value: 2},
			{Timestamp: 110, Value: 12},
			{Timestamp: 120, Value: 22},
		},
	}}}
	vector, err := ApplyIncreaseInstantWithBounds(matrix, 90, 130)
	if err != nil {
		t.Fatalf("ApplyIncreaseInstantWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got := vector.Samples[0].Value; math.Abs(got-32.0) > 1e-9 {
		t.Errorf("expected zero-crossing-clamped increase 32, got %.6f", got)
	}
}

func TestExtrapolationGaugeSkipsZeroCrossingClamp(t *testing.T) {
	// The same series on the delta (gauge) path: isCounter is false so the
	// zero-crossing clamp must NOT engage. durationToStart stays 10s (below
	// threshold), durationToEnd 10s, factor = (20+10+10)/20 = 2.0, delta 20
	// => 40. Contrast with 32 on the counter path above.
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: 100, Value: 2},
			{Timestamp: 110, Value: 12},
			{Timestamp: 120, Value: 22},
		},
	}}}
	vector, err := ApplyDeltaWithBounds(matrix, 90, 130)
	if err != nil {
		t.Fatalf("ApplyDeltaWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got := vector.Samples[0].Value; math.Abs(got-40.0) > 1e-9 {
		t.Errorf("expected gauge delta 40 (no zero-crossing clamp), got %.6f", got)
	}
}
