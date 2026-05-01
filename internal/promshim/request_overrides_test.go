package promshim

import (
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
)

func TestRequestRoutingOverridesDisabledByDefault(t *testing.T) {
	svc := &queryService{opts: Options{NativeLoweringMode: local.NativeLoweringModePrefer, RoutingPolicy: RoutingPolicyStrict}}
	if _, apiErr := svc.nativeLoweringModeForRequest("off"); apiErr == nil || !strings.Contains(apiErr.Error, "disabled") {
		t.Fatalf("native override error = %#v, want disabled", apiErr)
	}
	if _, apiErr := svc.routingPolicyForRequest("cost_prefer"); apiErr == nil || !strings.Contains(apiErr.Error, "disabled") {
		t.Fatalf("routing override error = %#v, want disabled", apiErr)
	}
}

func TestRequestRoutingOverridesCanBeEnabled(t *testing.T) {
	svc := &queryService{opts: Options{NativeLoweringMode: local.NativeLoweringModePrefer, RoutingPolicy: RoutingPolicyStrict, AllowRequestRoutingOverrides: true}}
	mode, apiErr := svc.nativeLoweringModeForRequest("off")
	if apiErr != nil {
		t.Fatalf("native override error = %#v", apiErr)
	}
	if mode != local.NativeLoweringModeOff {
		t.Fatalf("mode = %q, want off", mode)
	}
	policy, apiErr := svc.routingPolicyForRequest("cost_prefer")
	if apiErr != nil {
		t.Fatalf("routing override error = %#v", apiErr)
	}
	if policy != RoutingPolicyCostPrefer {
		t.Fatalf("policy = %q, want cost_prefer", policy)
	}
}
