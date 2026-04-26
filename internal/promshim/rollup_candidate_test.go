package promshim

import (
	"testing"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
)

func TestMatchesDenseRateRollupQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		step  time.Duration
		want  bool
	}{
		{
			name:  "exact supported shape",
			query: `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`,
			step:  time.Minute,
			want:  true,
		},
		{
			name:  "wrong step",
			query: `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`,
			step:  5 * time.Minute,
			want:  false,
		},
		{
			name:  "wrong grouping",
			query: `sum by (instance) (rate(demo_cpu_usage_seconds_total[5m]))`,
			step:  time.Minute,
			want:  false,
		},
		{
			name:  "wrong window",
			query: `sum by (job) (rate(demo_cpu_usage_seconds_total[1m]))`,
			step:  time.Minute,
			want:  false,
		},
		{
			name:  "different bare metric represented by contract",
			query: `sum by (job) (rate(other_counter_total[5m]))`,
			step:  time.Minute,
			want:  true,
		},
		{
			name:  "extra matcher not represented by rollup",
			query: `sum by (job) (rate(demo_cpu_usage_seconds_total{instance="a"}[5m]))`,
			step:  time.Minute,
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesDenseRateRollupQuery(tt.query, tt.step); got != tt.want {
				t.Fatalf("matchesDenseRateRollupQuery() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDenseRateRollupMetricName(t *testing.T) {
	metricName, ok := denseRateRollupMetricName(`sum by (job) (rate(other_counter_total[5m]))`, time.Minute)
	if !ok || metricName != "other_counter_total" {
		t.Fatalf("denseRateRollupMetricName() = %q, %v", metricName, ok)
	}
	if metricName, ok := denseRateRollupMetricName(`sum by (job) (rate(other_counter_total{instance="a"}[5m]))`, time.Minute); ok || metricName != "" {
		t.Fatalf("extra matcher should reject, got %q, %v", metricName, ok)
	}
}

func TestAttachDenseRateRollupCandidateReportsGateDisabled(t *testing.T) {
	service := &queryService{denseRateRollup: denseRateRollupDiscoveryForTest(true), opts: Options{DenseRateRollups: "off"}}
	info := httpapi.RoutingInfo{Class: httpapi.QueryCostClass{Family: "aggregation"}}
	service.attachDenseRateRollupCandidate(&info, `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`, time.Minute)
	if len(info.Candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", info.Candidates)
	}
	candidate := info.Candidates[0]
	if candidate.ID != denseRateRollupCandidateID {
		t.Fatalf("candidate ID = %q", candidate.ID)
	}
	if !candidate.Supported || !candidate.KnownCorrect {
		t.Fatalf("expected supported known-correct diagnostic candidate: %#v", candidate)
	}
	if candidate.Eligible || candidate.Selected || candidate.Served {
		t.Fatalf("rollup candidate must remain non-serving: %#v", candidate)
	}
	if len(candidate.RejectReasons) != 1 || candidate.RejectReasons[0] != "gate_disabled" {
		t.Fatalf("unexpected reject reasons: %#v", candidate.RejectReasons)
	}
}

func TestAttachDenseRateRollupCandidateWaitsForCoverageWhenGateEnabled(t *testing.T) {
	service := &queryService{denseRateRollup: denseRateRollupDiscoveryForTest(true), opts: Options{DenseRateRollups: "prefer"}}
	info := httpapi.RoutingInfo{Class: httpapi.QueryCostClass{Family: "aggregation"}}
	service.attachDenseRateRollupCandidate(&info, `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`, time.Minute)
	candidate := info.Candidates[0]
	if !candidate.Supported || !candidate.KnownCorrect {
		t.Fatalf("expected supported known-correct rollup candidate when gate is enabled: %#v", candidate)
	}
	if candidate.Eligible || candidate.Selected || candidate.Served {
		t.Fatalf("candidate must not be served before coverage check: %#v", candidate)
	}
	if len(candidate.RejectReasons) != 1 || candidate.RejectReasons[0] != "coverage_unverified" {
		t.Fatalf("unexpected reject reasons: %#v", candidate.RejectReasons)
	}
}

func TestAttachDenseRateRollupCandidateReportsAbsentRollup(t *testing.T) {
	service := &queryService{denseRateRollup: denseRateRollupDiscoveryForTest(false)}
	info := httpapi.RoutingInfo{Class: httpapi.QueryCostClass{Family: "aggregation"}}
	service.attachDenseRateRollupCandidate(&info, `sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`, time.Minute)
	candidate := info.Candidates[0]
	if candidate.Supported || candidate.KnownCorrect || candidate.Eligible {
		t.Fatalf("absent rollup must not be supported: %#v", candidate)
	}
	if len(candidate.RejectReasons) != 1 || candidate.RejectReasons[0] != "rollup_not_detected" {
		t.Fatalf("unexpected reject reasons: %#v", candidate.RejectReasons)
	}
}
