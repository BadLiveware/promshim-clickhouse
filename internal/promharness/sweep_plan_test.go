package promharness

import (
	"strings"
	"testing"
)

func TestProfileEndTimeOffsetsDenseSlots(t *testing.T) {
	got, err := ProfileEndTime("7d", "dense")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-03-14T21:45:42Z" {
		t.Fatalf("dense 7d eval = %s", got)
	}
	got, err = ProfileEndTime("30d", "stress-50k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2025-12-22T21:45:42Z" {
		t.Fatalf("stress 30d eval = %s", got)
	}
}

func TestBuildSweepPlanExpandsAxesAndCorpora(t *testing.T) {
	plan, err := BuildSweepPlan(SweepPlanOptions{RunName: "r", Profile: "all", Density: "all", Transport: "native", SeedPolicy: "reuse", ShimModes: "prefer", RoutingPolicies: "strict", MemoryMode: "summary", ClickHouseProfileMode: "off", ClickHouseReferenceProfile: "default-benchmark-compose", SettingsProfile: "default_safe", CorpusSet: "both", Estimate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Datasets) != 6 {
		t.Fatalf("datasets = %d", len(plan.Datasets))
	}
	if len(plan.Corpora) != 12 {
		t.Fatalf("corpora = %d", len(plan.Corpora))
	}
	out := RenderSweepPlan(plan)
	for _, want := range []string{"Sweep run: r", "7d  sparse eval=2026-03-22T21:45:42Z series≈130", "harness/corpus/bench-native-lowering-7d.json", "harness/corpus/bench-processing-1y.json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCorpusPathsForOptimizationIs7DOnly(t *testing.T) {
	paths, err := CorpusPathsFor("7d", "optimization")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "harness/corpus/bench-optimization-tuning-7d.json" {
		t.Fatalf("optimization paths = %#v", paths)
	}
	if _, err := CorpusPathsFor("30d", "optimization"); err == nil || !strings.Contains(err.Error(), "only --profile 7d") {
		t.Fatalf("expected 30d optimization rejection, got %v", err)
	}
}

func TestEstimateSamplesStress(t *testing.T) {
	got, err := EstimateSamples("7d", "stress-50k")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "series≈50,024") || !strings.Contains(got, "samples≈2,016,967,680") {
		t.Fatalf("estimate = %s", got)
	}
}
