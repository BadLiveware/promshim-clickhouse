package exec

// Tests for ApplyDeltaWithBounds, the extrapolating delta matching
// Prometheus's funcDelta (extrapolatedRate with isCounter=false,
// isRate=false). Expected values verified against the real Prometheus
// engine via promqltest.

import (
	"math"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestApplyDeltaWithBoundsExtrapolatesLikePrometheus(t *testing.T) {
	// Linear gauge (slope 2/s) sampled every 15s over [end-285, end-15]:
	// one scrape short of both boundaries of the 5m window, each gap under
	// the 16.5s extrapolation threshold. Prometheus scales the raw 540
	// difference to 600.
	end := 1_700_000_000.0
	points := make([]model.RangePoint, 0, 19)
	for ts := end - 285.0; ts <= end-15.0+1e-9; ts += 15.0 {
		points = append(points, model.RangePoint{Timestamp: ts, Value: 2.0 * ts})
	}
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: points,
	}}}
	vector, err := ApplyDeltaWithBounds(matrix, end-300.0, end)
	if err != nil {
		t.Fatalf("ApplyDeltaWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got := vector.Samples[0].Value; math.Abs(got-600.0) > 1e-6 {
		t.Errorf("expected extrapolated delta 600, got %.3f", got)
	}
}

func TestApplyDeltaWithBoundsIsNotCounterCorrected(t *testing.T) {
	// A gauge that decreases must keep the negative difference; counter
	// reset correction (which would treat the drop as a reset) must not
	// apply on the delta path.
	end := 1_700_000_000.0
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: end - 300.0, Value: 100},
			{Timestamp: end - 150.0, Value: 60},
			{Timestamp: end, Value: 20},
		},
	}}}
	vector, err := ApplyDeltaWithBounds(matrix, end-300.0, end)
	if err != nil {
		t.Fatalf("ApplyDeltaWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got := vector.Samples[0].Value; math.Abs(got-(-80.0)) > 1e-9 {
		t.Errorf("expected delta -80 (no reset correction), got %.3f", got)
	}
}

func TestApplyDeltaWithBoundsNaNBoundary(t *testing.T) {
	// A NaN first or last sample makes the result NaN, matching both
	// Prometheus and the existing non-extrapolating ApplyDelta.
	end := 1_700_000_000.0
	matrix := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "api"},
		Values: []model.RangePoint{
			{Timestamp: end - 300.0, Value: math.NaN()},
			{Timestamp: end, Value: 20},
		},
	}}}
	vector, err := ApplyDeltaWithBounds(matrix, end-300.0, end)
	if err != nil {
		t.Fatalf("ApplyDeltaWithBounds: %v", err)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if !math.IsNaN(vector.Samples[0].Value) {
		t.Errorf("expected NaN for NaN boundary sample, got %v", vector.Samples[0].Value)
	}
}
