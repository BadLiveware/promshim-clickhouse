package promshim

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func TestEnforceEstimatedResponseLimitsRejectsBeforeMaterialization(t *testing.T) {
	routing := httpapi.RoutingInfo{
		EstimatesAvailable: true,
		Class: httpapi.QueryCostClass{
			EstimatedSeries:       3,
			EstimatedOutputPoints: 101,
		},
	}
	err := enforceEstimatedResponseLimits(routing, Options{MaxResponseSeries: 10, MaxResponsePoints: 100})
	if err == nil {
		t.Fatal("expected estimated response point limit error")
	}
	if !local.IsBadDataError(err) {
		t.Fatalf("expected bad_data error kind, got %v", err)
	}
}

func TestEnforceEstimatedResponseLimitsAllowsMissingEstimates(t *testing.T) {
	routing := httpapi.RoutingInfo{
		EstimatesAvailable: false,
		Class:              httpapi.QueryCostClass{EstimatedSeries: 1000, EstimatedOutputPoints: 1000},
	}
	if err := enforceEstimatedResponseLimits(routing, Options{MaxResponseSeries: 1, MaxResponsePoints: 1}); err != nil {
		t.Fatalf("expected missing estimates to defer to post-eval limit, got %v", err)
	}
}

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
