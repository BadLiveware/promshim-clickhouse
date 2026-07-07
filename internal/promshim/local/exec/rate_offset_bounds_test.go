package exec

// Regression tests for issue #36 at the extrapolation-math level: the
// window bounds passed to the extrapolated rate/increase/delta helpers
// must be the offset-shifted window, exactly as Prometheus's
// extrapolatedRate receives rangeStart = t-(offset+range) and
// rangeEnd = t-offset. Values are cross-checked against the real
// Prometheus engine via promqltest (constant-rate counter / linear
// gauge shapes are closed-form).

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

// offsetWindowCounterSamples returns a monotonic counter (91.3/s, 15s
// scrape) covering the *offset* window (evalTime-35m, evalTime-30m], as a
// correct fetch of `counter[5m] offset 30m` returns them.
func offsetWindowCounterSamples(evalTime float64) []model.RangePoint {
	const ratePerSec = 91.3
	windowEnd := evalTime - 1800.0
	windowStart := evalTime - 2100.0
	points := make([]model.RangePoint, 0, 21)
	for ts := windowStart; ts <= windowEnd+1e-9; ts += 15.0 {
		points = append(points, model.RangePoint{Timestamp: ts, Value: ratePerSec * ts})
	}
	return points
}

func TestExtrapolatedRateOffsetBounds(t *testing.T) {
	evalTime := 1_700_000_000.0
	samples := offsetWindowCounterSamples(evalTime)

	// Unshifted bounds (the pre-fix planner behavior for
	// rate(x[5m] offset 30m)): the whole sampled interval sits before the
	// extrapolation window, flipping the factor negative (-454.2 observed
	// in the lab / triage).
	buggy := extrapolatedValue(samples, evalTime-300.0, evalTime, true, true)
	if buggy >= 0 {
		t.Errorf("unshifted bounds should reproduce the negative-rate defect, got %.3f", buggy)
	}
	if math.Abs(buggy-(-454.217)) > 0.5 {
		t.Errorf("unshifted bounds: expected the documented -454.217 defect value, got %.3f", buggy)
	}

	// Offset-shifted bounds (Prometheus semantics): factor 1, rate 91.3.
	correct := extrapolatedValue(samples, evalTime-2100.0, evalTime-1800.0, true, true)
	if math.Abs(correct-91.3) > 1e-9 {
		t.Errorf("shifted bounds: expected 91.300, got %.3f", correct)
	}
}

func TestApplyIncreaseInstantWithBoundsOffsetWindow(t *testing.T) {
	evalTime := 1_700_000_000.0
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: offsetWindowCounterSamples(evalTime),
	}}}
	vector, err := ApplyIncreaseInstantWithBounds(matrix, evalTime-2100.0, evalTime-1800.0)
	if err != nil {
		t.Fatalf("ApplyIncreaseInstantWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	expected := 91.3 * 300.0
	if got := vector.Samples[0].Value; math.Abs(got-expected) > 1e-6 {
		t.Errorf("expected increase %.3f, got %.3f", expected, got)
	}
}

