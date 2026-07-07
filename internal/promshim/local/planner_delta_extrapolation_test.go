package local

// Regression tests for the tier-4 delta gap found alongside issue #36:
// localDeltaPlan applied a plain last-first difference, but Prometheus's
// delta extrapolates to the window boundaries exactly like rate/increase
// (extrapolatedRate with isCounter=false). Expected values verified
// against the real Prometheus engine via promqltest.

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

// gaugeShrunkWindowMatrix returns a linear gauge (slope 2/s, 15s scrape)
// whose samples cover [windowEnd-285s, windowEnd-15s]: one scrape short of
// both window boundaries, so Prometheus's delta extrapolation scales the
// raw 540 difference back to the full 5m window (600).
func gaugeShrunkWindowMatrix(windowEnd float64) model.MatrixValue {
	points := make([]model.RangePoint, 0, 19)
	for ts := windowEnd - 285.0; ts <= windowEnd-15.0+1e-9; ts += 15.0 {
		points = append(points, model.RangePoint{Timestamp: ts, Value: 2.0 * ts})
	}
	return model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "test_gauge", "job": "api"},
		Values: points,
	}}}
}

func TestLocalDeltaPlanExtrapolatesLikePrometheus(t *testing.T) {
	// delta(test_gauge[5m]): samples span 270s of the 300s window with one
	// scrape (15s < threshold 16.5s) missing at each boundary, so
	// Prometheus extrapolates the raw 540 difference to 600.
	child := &staticMatrixPlan{matrix: gaugeShrunkWindowMatrix(anchorTestEvalSec)}
	vector := executeInstantPlanForAnchorTest(t, `delta(test_gauge[5m])`, child)
	expected := 600.0
	if got := vector.Samples[0].Value; math.Abs(got-expected) > 1e-6 {
		t.Errorf("delta: expected extrapolated %.3f, got %.3f", expected, got)
	}
}

func TestLocalDeltaPlanOffsetShiftsExtrapolationAnchor(t *testing.T) {
	child := &staticMatrixPlan{matrix: gaugeShrunkWindowMatrix(anchorTestEvalSec - 1800.0)}
	vector := executeInstantPlanForAnchorTest(t, `delta(test_gauge[5m] offset 30m)`, child)
	expected := 600.0
	if got := vector.Samples[0].Value; math.Abs(got-expected) > 1e-6 {
		t.Errorf("delta with offset 30m: expected extrapolated %.3f, got %.3f", expected, got)
	}
}

func TestLocalIDeltaPlanStaysUnextrapolated(t *testing.T) {
	// Prometheus's idelta is the raw difference of the last two samples;
	// it must not pick up the delta extrapolation.
	child := &staticMatrixPlan{matrix: gaugeShrunkWindowMatrix(anchorTestEvalSec)}
	vector := executeInstantPlanForAnchorTest(t, `idelta(test_gauge[5m])`, child)
	expected := 30.0 // slope 2/s over one 15s scrape interval
	if got := vector.Samples[0].Value; math.Abs(got-expected) > 1e-9 {
		t.Errorf("idelta: expected raw %.3f, got %.3f", expected, got)
	}
}
