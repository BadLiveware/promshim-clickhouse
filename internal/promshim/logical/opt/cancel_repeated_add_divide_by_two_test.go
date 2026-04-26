package opt_test

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical/opt"
)

func TestCancelRepeatedAverageTwoTerms(t *testing.T) {
	root := mustLogical(t, `(rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_cpu_usage_seconds_total[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.RatePlan); !ok {
		t.Fatalf("expected repeated average to collapse to RatePlan, got %T", out)
	}
}

func TestCancelRepeatedAverageThreeTerms(t *testing.T) {
	root := mustLogical(t, `(rate(up[5m]) + rate(up[5m]) + rate(up[5m])) / 3`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.RatePlan); !ok {
		t.Fatalf("expected three-term repeated average to collapse to RatePlan, got %T", out)
	}
}

func TestCancelRepeatedAverageAssociativity(t *testing.T) {
	queries := []string{
		`((rate(up[5m]) + rate(up[5m])) + rate(up[5m])) / 3`,
		`(rate(up[5m]) + (rate(up[5m]) + rate(up[5m]))) / 3`,
		`(((rate(up[5m]) + rate(up[5m])) + rate(up[5m])) + rate(up[5m])) / 4`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			root := mustLogical(t, query)
			out, _, err := opt.Optimize(root, opt.DefaultPasses)
			if err != nil {
				t.Fatalf("Optimize: %v", err)
			}
			if _, ok := out.(*logical.RatePlan); !ok {
				t.Fatalf("expected repeated average to collapse to RatePlan, got %T", out)
			}
		})
	}
}

func TestCancelRepeatedAverageNestedInAggregation(t *testing.T) {
	root := mustLogical(t, `sum by (job) ((rate(up[5m]) + rate(up[5m]) + rate(up[5m])) / 3)`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	agg, ok := out.(*logical.AggregationPlan)
	if !ok {
		t.Fatalf("expected AggregationPlan, got %T", out)
	}
	if _, ok := agg.Child.(*logical.RatePlan); !ok {
		t.Fatalf("expected aggregation child to collapse to RatePlan, got %T", agg.Child)
	}
}

func TestCancelRepeatedAverageTraceMetadata(t *testing.T) {
	root := mustLogical(t, `(rate(up[5m]) + rate(up[5m])) / 2`)
	_, _, trace, err := opt.OptimizeWithTrace(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("OptimizeWithTrace: %v", err)
	}
	foundApplied := false
	for _, pass := range trace.Passes {
		if pass.Name == "cancel_repeated_average" {
			if pass.Metadata.RollbackConfiguration != opt.DisableOptimizedIREnv {
				t.Fatalf("unexpected rollback metadata: %+v", pass.Metadata)
			}
			foundApplied = foundApplied || pass.Applied
		}
	}
	if !foundApplied {
		t.Fatalf("applied cancel_repeated_average trace not found: %+v", trace.Passes)
	}
}

func TestCancelRepeatedAverageHonorsOptimizedIRDisableEnv(t *testing.T) {
	t.Setenv(opt.DisableOptimizedIREnv, "true")
	root := mustLogical(t, `(rate(up[5m]) + rate(up[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPassesForEnv())
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected disabled optimized IR to preserve BinaryPlan, got %T", out)
	}
}

func TestCancelRepeatedAverageRequiresMatchingOperands(t *testing.T) {
	root := mustLogical(t, `(rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_memory_usage_bytes[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected mismatched operands to remain BinaryPlan, got %T", out)
	}
}

func TestCancelRepeatedAverageRequiresExactTermCountDivisor(t *testing.T) {
	queries := []string{
		`(rate(up[5m]) + rate(up[5m])) / 3`,
		`(rate(up[5m]) + rate(up[5m]) + rate(up[5m])) / 2`,
		`(rate(up[5m]) + rate(up[5m])) / 2.5`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			root := mustLogical(t, query)
			out, _, err := opt.Optimize(root, opt.DefaultPasses)
			if err != nil {
				t.Fatalf("Optimize: %v", err)
			}
			if _, ok := out.(*logical.BinaryPlan); !ok {
				t.Fatalf("expected non-exact divisor to remain BinaryPlan, got %T", out)
			}
		})
	}
}

func TestCancelRepeatedAverageRequiresImplicitMatching(t *testing.T) {
	root := mustLogical(t, `(rate(demo_cpu_usage_seconds_total[5m]) + on(job) rate(demo_cpu_usage_seconds_total[5m])) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected explicit vector matching to remain BinaryPlan, got %T", out)
	}
}

func TestCancelRepeatedAverageRequiresMetricDroppingChild(t *testing.T) {
	root := mustLogical(t, `(demo_cpu_usage_seconds_total + demo_cpu_usage_seconds_total) / 2`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.BinaryPlan); !ok {
		t.Fatalf("expected metric-preserving child to remain BinaryPlan, got %T", out)
	}
}

func TestCancelRepeatedAverageDoesNotRewriteOtherAlgebra(t *testing.T) {
	queries := []string{
		`(rate(up[5m]) - rate(up[5m])) / 2`,
		`(rate(up[5m]) + rate(up[5m])) * 0.5`,
		`(rate(up[5m]) * 2) / 2`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			root := mustLogical(t, query)
			out, _, err := opt.Optimize(root, opt.DefaultPasses)
			if err != nil {
				t.Fatalf("Optimize: %v", err)
			}
			if _, ok := out.(*logical.BinaryPlan); !ok {
				t.Fatalf("expected unsupported algebra to remain BinaryPlan, got %T", out)
			}
		})
	}
}
