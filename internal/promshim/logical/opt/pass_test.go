package opt_test

import (
	"errors"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical/opt"
)

type noopPass struct{}

type describedNoopPass struct{ noopPass }

func (noopPass) Name() string { return "noop" }
func (noopPass) Apply(n logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	return n, false, nil
}

func (describedNoopPass) Name() string { return "described_noop" }
func (describedNoopPass) Metadata() opt.PassMetadata {
	return opt.PassMetadata{Name: "described_noop", Families: []string{"selector"}, ExpectedSignals: []string{"no_result_change"}}
}

type alwaysErrPass struct{}

func (alwaysErrPass) Name() string { return "alwaysErr" }
func (alwaysErrPass) Apply(_ logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	return nil, false, errors.New("boom")
}

type churnPass struct{}

func (churnPass) Name() string { return "churn" }
func (churnPass) Apply(n logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	if leaf, ok := n.(*logical.LeafExprPlan); ok {
		return &logical.LeafExprPlan{Expr: leaf.Expr}, true, nil
	}
	return n, false, nil
}

func mustLogical(t *testing.T, q string) logical.Node {
	t.Helper()
	expr, err := logical.ParseExpression(q)
	if err != nil {
		t.Fatalf("ParseExpression: %v", err)
	}
	node, err := logical.ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	return node
}

func TestOptimizeNoChangeReachesFixpoint(t *testing.T) {
	root := mustLogical(t, `up`)
	out, _, err := opt.Optimize(root, []opt.Pass{noopPass{}})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if out != root {
		t.Error("noop pass must return same root pointer")
	}
}

func TestOptimizePropagatesError(t *testing.T) {
	root := mustLogical(t, `up`)
	_, _, err := opt.Optimize(root, []opt.Pass{alwaysErrPass{}})
	if err == nil {
		t.Error("expected error propagation")
	}
}

func TestOptimizeCapsIterations(t *testing.T) {
	root := mustLogical(t, `up`)
	_, _, err := opt.Optimize(root, []opt.Pass{churnPass{}})
	if err == nil {
		t.Error("expected fixpoint-cap error")
	}
}

func TestOptimizeWithTraceRecordsMetadataAndSkipReasons(t *testing.T) {
	root := mustLogical(t, `up`)
	out, analysis, trace, err := opt.OptimizeWithTrace(root, []opt.Pass{describedNoopPass{}})
	if err != nil {
		t.Fatalf("OptimizeWithTrace: %v", err)
	}
	if out != root || analysis == nil || trace == nil {
		t.Fatalf("unexpected optimize result: out=%T analysis=%v trace=%v", out, analysis, trace)
	}
	if len(trace.Passes) != 1 {
		t.Fatalf("trace pass count = %d, want 1", len(trace.Passes))
	}
	got := trace.Passes[0]
	if got.Name != "described_noop" || got.Applied || len(got.SkipReasons) != 1 || got.SkipReasons[0] != "no_matching_subtree" {
		t.Fatalf("unexpected pass result: %+v", got)
	}
	if got.InspectedNodes <= 0 {
		t.Fatalf("inspected nodes not recorded: %+v", got)
	}
	if got.OptimizerTimeMicros <= 0 {
		t.Fatalf("optimizer time must be positive: %+v", got)
	}
	if got.BeforeFingerprint == "" || got.AfterFingerprint == "" || got.BeforeFingerprint != got.AfterFingerprint {
		t.Fatalf("unexpected fingerprints for no-op pass: %+v", got)
	}
	if len(got.Metadata.Families) != 1 || got.Metadata.Families[0] != "selector" {
		t.Fatalf("metadata not preserved: %+v", got.Metadata)
	}
}

func TestOptimizeWithTraceHonorsDisableEnv(t *testing.T) {
	t.Setenv(opt.DisableOptimizedIREnv, "true")
	root := mustLogical(t, `-(-up)`)
	out, _, trace, err := opt.OptimizeWithTrace(root, opt.DefaultPassesForEnv())
	if err != nil {
		t.Fatalf("OptimizeWithTrace: %v", err)
	}
	if out != root {
		t.Fatal("disabled optimized IR should return the original root")
	}
	if trace == nil || !trace.Disabled || trace.EnvGate != opt.DisableOptimizedIREnv {
		t.Fatalf("unexpected disabled trace: %+v", trace)
	}
}
