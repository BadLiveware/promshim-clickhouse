package opt_test

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical/opt"
)

func TestCancelRepeatedAddDivideByTwo(t *testing.T) {
	root := mustLogical(t, `(rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_cpu_usage_seconds_total[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.RatePlan); !ok {
		t.Fatalf("expected repeated add/divide to collapse to RatePlan, got %T", out)
	}
}

func TestCancelRepeatedAddDivideByTwoRequiresMatchingOperands(t *testing.T) {
	root := mustLogical(t, `(rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_memory_usage_bytes[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected mismatched operands to remain BinaryPlan, got %T", out)
	}
}

func TestCancelRepeatedAddDivideByTwoRequiresImplicitMatching(t *testing.T) {
	root := mustLogical(t, `(rate(demo_cpu_usage_seconds_total[5m]) + on(job) rate(demo_cpu_usage_seconds_total[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected explicit vector matching to remain BinaryPlan, got %T", out)
	}
}

func TestCancelRepeatedAddDivideByTwoRequiresMetricDroppingChild(t *testing.T) {
	root := mustLogical(t, `(demo_cpu_usage_seconds_total + demo_cpu_usage_seconds_total) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected metric-preserving child to remain BinaryPlan, got %T", out)
	}
}
