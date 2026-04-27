package promharness

import (
	"strings"
	"testing"
)

func TestProfileEndTimeOffsetsActiveSeriesSlots(t *testing.T) {
	got, err := ProfileEndTime("7d", "fast-5k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-03-22T21:45:42Z" {
		t.Fatalf("fast 7d eval = %s", got)
	}
	got, err = ProfileEndTime("30d", "profile-50k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2026-01-22T21:45:42Z" {
		t.Fatalf("profile-50k 30d eval = %s", got)
	}
	got, err = ProfileEndTime("30d", "profile-500k")
	if err != nil {
		t.Fatal(err)
	}
	if got != "2025-12-22T21:45:42Z" {
		t.Fatalf("profile-500k 30d eval = %s", got)
	}
}

func TestBuildSweepPlanExpandsAxesAndCorpora(t *testing.T) {
	plan, err := BuildSweepPlan(SweepPlanOptions{RunName: "r", Profile: "all", ActiveSeriesPreset: "all", Transport: "native", SeedPolicy: "reuse", ShimModes: "prefer", RoutingPolicies: "strict", MemoryMode: "summary", ClickHouseProfileMode: "off", ClickHouseReferenceProfile: "default-benchmark-compose", SettingsProfile: "default_safe", CorpusSet: "both", Estimate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Datasets) != 9 {
		t.Fatalf("datasets = %d", len(plan.Datasets))
	}
	if len(plan.Corpora) != 18 {
		t.Fatalf("corpora = %d", len(plan.Corpora))
	}
	out := RenderSweepPlan(plan)
	for _, want := range []string{"Sweep run: r", "7d  fast-5k", "target_series≈5,000 actual_series≈5,018", "harness/corpus/bench-native-lowering-7d.json", "harness/corpus/bench-processing-1y.json"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestActiveSeriesSelectionAcceptsCustomAndLegacy(t *testing.T) {
	custom, err := ActiveSeriesSelections("12k", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if custom[0].Target != 12000 || custom[0].Label != "custom-12k" {
		t.Fatalf("custom selection = %#v", custom[0])
	}
	legacy, err := ActiveSeriesSelections("", "", "stress-50k")
	if err != nil {
		t.Fatal(err)
	}
	if legacy[0].Target != 50000 || legacy[0].Label != "profile-50k" {
		t.Fatalf("legacy selection = %#v", legacy[0])
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

func TestEstimateSamplesActiveSeries(t *testing.T) {
	got, err := EstimateSamples("7d", ActiveSeriesSelection{Label: "profile-50k", Target: 50000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "actual_series≈50,024") || !strings.Contains(got, "samples≈2,016,967,680") {
		t.Fatalf("estimate = %s", got)
	}
}
