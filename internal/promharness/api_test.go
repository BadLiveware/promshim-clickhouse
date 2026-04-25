package promharness

import (
	"strings"
	"testing"
)

func TestBuildQueryURLIncludesNativeLoweringModeExplainAndRoutingPolicyForInstantQueries(t *testing.T) {
	url, err := buildQueryURL("http://shim:9090", Manifest{BaseUnixSeconds: 1700000000}, QuerySpec{
		Endpoint:           "query",
		Query:              `sum by (job) (harness_up)`,
		TimeOffsetSeconds:  540,
		Explain:            true,
		NativeLoweringMode: "shadow",
		RoutingPolicy:      "cost_shadow",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"/api/v1/query?",
		"query=sum+by+%28job%29+%28harness_up%29",
		"time=2023-11-14T22%3A22%3A20Z",
		"explain=1",
		"native_lowering_mode=shadow",
		"routing_policy=cost_shadow",
	} {
		if !strings.Contains(url, fragment) {
			t.Fatalf("expected %q to contain %q", url, fragment)
		}
	}
}

func TestBuildQueryURLIncludesNativeLoweringModeForRangeQueries(t *testing.T) {
	url, err := buildQueryURL("http://shim:9090", Manifest{BaseUnixSeconds: 1700000000}, QuerySpec{
		Endpoint:           "query_range",
		Query:              `sum_over_time(harness_queue_depth[5m])`,
		StartOffsetSeconds: 300,
		EndOffsetSeconds:   540,
		StepSeconds:        60,
		NativeLoweringMode: "off",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"/api/v1/query_range?",
		"query=sum_over_time%28harness_queue_depth%5B5m%5D%29",
		"start=2023-11-14T22%3A18%3A20Z",
		"end=2023-11-14T22%3A22%3A20Z",
		"step=60s",
		"native_lowering_mode=off",
	} {
		if !strings.Contains(url, fragment) {
			t.Fatalf("expected %q to contain %q", url, fragment)
		}
	}
}
