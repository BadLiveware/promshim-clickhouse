package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

func main() {
	var opts promharness.SweepPlanOptions
	flag.StringVar(&opts.RunName, "run-name", "", "Sweep run name.")
	flag.StringVar(&opts.ArtifactRoot, "artifact-root", getenv("PROM_SHIM_ARTIFACT_ROOT", "harness/artifacts"), "Artifact root relative to repo root.")
	flag.StringVar(&opts.Profile, "profile", "7d", "Profile: 7d, 30d, 1y, all.")
	flag.StringVar(&opts.ActiveSeries, "active-series", "", "Target active series count, e.g. 5000, 50k, 500k.")
	flag.StringVar(&opts.ActiveSeriesPreset, "active-series-preset", "", "Active series preset: fast, profile-50k, profile-500k, all. Default: fast.")
	flag.StringVar(&opts.ActiveSeriesPreset, "named-active-series", "", "Alias for --active-series-preset.")
	flag.StringVar(&opts.Density, "density", "", "Deprecated compatibility alias: sparse, dense, stress-50k, stress-500k, all.")
	flag.StringVar(&opts.Transport, "transport", "native", "Benchmark transport.")
	flag.StringVar(&opts.SeedPolicy, "seed-policy", "reuse", "Seed policy.")
	flag.BoolVar(&opts.SkipCompliance, "skip-compliance", false, "Compliance skipped.")
	flag.BoolVar(&opts.SkipBench, "skip-bench", false, "Benchmark skipped.")
	flag.StringVar(&opts.ShimModes, "shim-modes", "prefer,force_supported,off", "Shim modes.")
	flag.StringVar(&opts.RoutingPolicies, "routing-policies", "strict", "Routing policies.")
	flag.StringVar(&opts.WarmupRoutingPolicies, "warmup-routing-policies", "", "Warmup routing policies.")
	flag.StringVar(&opts.CostRoutingLocalFamilies, "cost-routing-local-families", "", "Cost routing local families.")
	flag.StringVar(&opts.MemoryMode, "memory-mode", "summary", "Memory mode.")
	flag.StringVar(&opts.ClickHouseProfileMode, "clickhouse-profile-mode", "off", "ClickHouse profile mode.")
	flag.StringVar(&opts.ClickHouseReferenceProfile, "clickhouse-reference-profile", "default-benchmark-compose", "ClickHouse reference profile.")
	flag.StringVar(&opts.SettingsProfile, "settings-profile", "default_safe", "promshim settings profile.")
	flag.StringVar(&opts.CorpusSet, "corpus-set", "native", "Corpus set.")
	flag.BoolVar(&opts.Estimate, "estimate", false, "Include estimates.")
	flag.Parse()
	plan, err := promharness.BuildSweepPlan(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promshim-sweep-plan: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(promharness.RenderSweepPlan(plan))
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
