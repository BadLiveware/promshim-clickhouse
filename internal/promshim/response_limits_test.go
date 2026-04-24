package promshim

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestEnforceResponseLimitsRejectsExcessPoints(t *testing.T) {
	value := model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"job": "clickhouse"},
		Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 2}, {Timestamp: 3, Value: 3}},
	}}}
	err := enforceResponseLimits(value, Options{MaxResponseSeries: 10, MaxResponsePoints: 2})
	if err == nil {
		t.Fatal("expected response point limit error")
	}
	if !local.IsBadDataError(err) {
		t.Fatalf("expected bad_data error kind, got %v", err)
	}
}
