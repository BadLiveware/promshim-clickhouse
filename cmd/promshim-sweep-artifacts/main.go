package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

func main() {
	var opts promharness.SweepArtifactOptions
	flag.StringVar(&opts.RepoRoot, "repo-root", ".", "Repository root.")
	flag.StringVar(&opts.ArtifactDir, "artifact-dir", "", "Sweep artifact directory, relative to repo root.")
	flag.StringVar(&opts.RunName, "run-name", "", "Sweep run name.")
	flag.StringVar(&opts.Profile, "profile", "", "Selected profile axis.")
	flag.StringVar(&opts.Density, "density", "", "Selected density axis.")
	flag.StringVar(&opts.Transport, "transport", "", "Selected benchmark transport.")
	flag.StringVar(&opts.SeedPolicy, "seed-policy", "", "Selected seed policy.")
	flag.StringVar(&opts.ShimModes, "shim-modes", "", "Comma-separated shim modes.")
	flag.StringVar(&opts.RoutingPolicies, "routing-policies", "", "Comma-separated routing policies.")
	flag.StringVar(&opts.WarmupRoutingPolicies, "warmup-routing-policies", "", "Comma-separated warmup routing policies.")
	flag.StringVar(&opts.CostRoutingLocalFamilies, "cost-routing-local-families", "", "Comma-separated local families enabled for cost routing.")
	flag.StringVar(&opts.IncludeProm, "include-prom", "", "Whether Prometheus timings were included.")
	flag.StringVar(&opts.CorpusSet, "corpus-set", "", "Selected benchmark corpus set.")
	flag.StringVar(&opts.ComplianceStatus, "compliance-status", "skipped", "Compliance status label.")
	flag.StringVar(&opts.BenchStatus, "bench-status", "skipped", "Benchmark status label.")
	flag.StringVar(&opts.PromURL, "prom-url", "", "Prometheus endpoint recorded in manifest.")
	flag.StringVar(&opts.ShimURL, "shim-url", "", "promshim endpoint recorded in manifest.")
	flag.StringVar(&opts.ClickHouseURL, "ch-url", "", "ClickHouse endpoint recorded in manifest.")
	flag.StringVar(&opts.MemoryMode, "memory-mode", "", "Memory capture mode.")
	flag.StringVar(&opts.ClickHouseProfileMode, "clickhouse-profile-mode", "", "ClickHouse profile capture mode.")
	flag.StringVar(&opts.ClickHouseReferenceProfile, "clickhouse-reference-profile", "", "ClickHouse reference profile label.")
	flag.StringVar(&opts.SettingsProfile, "settings-profile", "", "promshim ClickHouse settings profile label.")
	flag.Parse()

	if err := promharness.BuildSweepArtifacts(opts); err != nil {
		fmt.Fprintf(os.Stderr, "promshim-sweep-artifacts: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s/manifest.json\n", opts.ArtifactDir)
	fmt.Printf("Wrote %s/summary.md\n", opts.ArtifactDir)
}
