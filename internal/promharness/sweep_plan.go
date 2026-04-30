package promharness

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultActiveSeriesPreset = "fast"
	seriesPerInstance         = 13
	defaultJobs               = 2
)

type SweepPlanOptions struct {
	RunName                    string
	ArtifactRoot               string
	Profile                    string
	Density                    string
	ActiveSeries               string
	ActiveSeriesPreset         string
	Transport                  string
	SeedPolicy                 string
	SkipCompliance             bool
	SkipBench                  bool
	ShimModes                  string
	RoutingPolicies            string
	WarmupRoutingPolicies      string
	CostRoutingLocalFamilies   string
	MemoryMode                 string
	ClickHouseProfileMode      string
	PrometheusProfileMode      string
	ClickHouseReferenceProfile string
	SettingsProfile            string
	CorpusSet                  string
	Estimate                   bool
}

type ActiveSeriesSelection struct {
	Label  string
	Target int
	Preset string
	Legacy string
}

type SweepDatasetPlan struct {
	Profile            string
	ActiveSeriesLabel  string
	ActiveSeriesTarget int
	ActiveSeriesActual int
	EvalTime           string
	Estimate           string
}
type SweepCorpusPlan struct {
	Profile           string
	ActiveSeriesLabel string
	Path              string
}

type SweepPlan struct {
	Options     SweepPlanOptions
	ArtifactDir string
	Datasets    []SweepDatasetPlan
	Corpora     []SweepCorpusPlan
}

func ProfilesFor(profile string) ([]string, error) {
	switch profile {
	case "all":
		return []string{"7d", "30d", "1y"}, nil
	case "7d", "30d", "1y":
		return []string{profile}, nil
	default:
		return nil, fmt.Errorf("--profile must be 7d|30d|1y|all (got: %s)", profile)
	}
}

func ActiveSeriesSelections(activeSeries, preset, legacyDensity string) ([]ActiveSeriesSelection, error) {
	activeSeries = strings.TrimSpace(activeSeries)
	preset = strings.TrimSpace(preset)
	legacyDensity = strings.TrimSpace(legacyDensity)
	if activeSeries != "" && (preset != "" || legacyDensity != "") {
		return nil, fmt.Errorf("--active-series cannot be combined with --active-series-preset/--named-active-series or --density")
	}
	if preset != "" && legacyDensity != "" {
		return nil, fmt.Errorf("--active-series-preset/--named-active-series cannot be combined with --density")
	}
	if activeSeries != "" {
		target, err := parseActiveSeries(activeSeries)
		if err != nil {
			return nil, err
		}
		return []ActiveSeriesSelection{{Label: fmt.Sprintf("custom-%s", formatCompactNumber(target)), Target: target}}, nil
	}
	if preset != "" {
		return activeSeriesSelectionsForPreset(preset)
	}
	if legacyDensity != "" {
		return activeSeriesSelectionsForLegacyDensity(legacyDensity)
	}
	return activeSeriesSelectionsForPreset(DefaultActiveSeriesPreset)
}

func activeSeriesSelectionsForPreset(preset string) ([]ActiveSeriesSelection, error) {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "all":
		return []ActiveSeriesSelection{
			{Label: "fast-5k", Target: 5000, Preset: "fast"},
			{Label: "profile-50k", Target: 50000, Preset: "profile-50k"},
			{Label: "profile-500k", Target: 500000, Preset: "profile-500k"},
		}, nil
	case "fast", "5k", "fast-5k":
		return []ActiveSeriesSelection{{Label: "fast-5k", Target: 5000, Preset: "fast"}}, nil
	case "profile-50k", "50k", "low-realistic", "low-realistic-50k", "dense-50k":
		return []ActiveSeriesSelection{{Label: "profile-50k", Target: 50000, Preset: "profile-50k"}}, nil
	case "profile-500k", "500k", "medium-realistic", "medium-realistic-500k":
		return []ActiveSeriesSelection{{Label: "profile-500k", Target: 500000, Preset: "profile-500k"}}, nil
	case "dashboard-50k", "realistic-50k":
		return []ActiveSeriesSelection{{Label: "dashboard-50k", Target: 50000, Preset: "dashboard-50k"}}, nil
	case "envoy-heavy-50k", "envoy-50k":
		return []ActiveSeriesSelection{{Label: "envoy-heavy-50k", Target: 50000, Preset: "envoy-heavy-50k"}}, nil
	case "churn-50k":
		return []ActiveSeriesSelection{{Label: "churn-50k", Target: 50000, Preset: "churn-50k"}}, nil
	default:
		return nil, fmt.Errorf("--active-series-preset must be fast|profile-50k|profile-500k|dashboard-50k|envoy-heavy-50k|churn-50k|all (got: %s)", preset)
	}
}

func activeSeriesSelectionsForLegacyDensity(density string) ([]ActiveSeriesSelection, error) {
	switch density {
	case "all":
		return []ActiveSeriesSelection{
			{Label: "sparse", Target: 130, Legacy: "sparse"},
			{Label: "dense", Target: 2600, Legacy: "dense"},
		}, nil
	case "sparse":
		return []ActiveSeriesSelection{{Label: "sparse", Target: 130, Legacy: "sparse"}}, nil
	case "dense":
		return []ActiveSeriesSelection{{Label: "dense", Target: 2600, Legacy: "dense"}}, nil
	case "stress-50k":
		return []ActiveSeriesSelection{{Label: "profile-50k", Target: 50000, Preset: "profile-50k", Legacy: density}}, nil
	case "stress-500k":
		return []ActiveSeriesSelection{{Label: "profile-500k", Target: 500000, Preset: "profile-500k", Legacy: density}}, nil
	default:
		return nil, fmt.Errorf("--density is deprecated; use --active-series or --active-series-preset. Legacy values: sparse|dense|stress-50k|stress-500k|all (got: %s)", density)
	}
}

func parseActiveSeries(raw string) (int, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	multiplier := 1.0
	if strings.HasSuffix(value, "k") {
		multiplier = 1000
		value = strings.TrimSuffix(value, "k")
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("--active-series must be a positive integer or k-suffixed value (got: %s)", raw)
	}
	return int(math.Ceil(parsed * multiplier)), nil
}

func InstancesPerJobForActiveSeries(target int) int {
	if target <= 0 {
		return 1
	}
	return int(math.Ceil(float64(target) / float64(defaultJobs*seriesPerInstance)))
}

func ActualActiveSeries(target int) int {
	return defaultJobs * InstancesPerJobForActiveSeries(target) * seriesPerInstance
}

func formatCompactNumber(n int) string {
	if n%1000 == 0 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func ProfileEndTime(profile, activeSeriesLabel string) (string, error) {
	base := map[string]string{"7d": "2026-03-22T21:45:42Z", "30d": "2026-02-22T21:45:42Z", "1y": "2025-03-22T21:45:42Z"}
	durations := map[string]time.Duration{"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "1y": 365 * 24 * time.Hour}
	b, ok := base[profile]
	if !ok {
		return "", fmt.Errorf("unknown profile %s", profile)
	}
	t, err := time.Parse(time.RFC3339, b)
	if err != nil {
		return "", err
	}
	slot := activeSeriesSlot(activeSeriesLabel)
	if slot > 0 {
		t = t.Add(-time.Duration(slot) * durations[profile]).Add(-time.Duration(slot) * 24 * time.Hour)
	}
	return t.UTC().Format(time.RFC3339), nil
}

func activeSeriesSlot(label string) int {
	switch label {
	case "sparse", "fast-5k":
		return 0
	case "dense", "profile-50k":
		return 1
	case "profile-500k":
		return 2
	case "dashboard-50k":
		return 3
	case "envoy-heavy-50k":
		return 4
	case "churn-50k":
		return 5
	default:
		return 6
	}
}

func CorpusPathsFor(profile, set string) ([]string, error) {
	if profile != "7d" && profile != "30d" && profile != "1y" {
		return nil, fmt.Errorf("unsupported bench profile for corpus: %s", profile)
	}
	suffix := "-" + profile
	out := []string{}
	switch set {
	case "native":
		out = append(out, "harness/corpus/bench-native-lowering"+suffix+".json")
	case "processing":
		out = append(out, "harness/corpus/bench-processing"+suffix+".json")
	case "both":
		out = append(out, "harness/corpus/bench-native-lowering"+suffix+".json", "harness/corpus/bench-processing"+suffix+".json")
	case "optimization":
		if profile != "7d" {
			return nil, fmt.Errorf("--corpus-set optimization currently supports only --profile 7d (got: %s)", profile)
		}
		out = append(out, "harness/corpus/bench-optimization-tuning"+suffix+".json")
	default:
		return nil, fmt.Errorf("--corpus-set must be native|processing|optimization|both (got: %s)", set)
	}
	return out, nil
}

func EstimateSamples(profile string, selection ActiveSeriesSelection) (string, error) {
	prof := map[string]int{"7d": 7 * 24 * 3600, "30d": 30 * 24 * 3600, "1y": 365 * 24 * 3600}
	duration, ok := prof[profile]
	if !ok {
		return "", fmt.Errorf("unknown profile %s", profile)
	}
	if workloadProfileForActiveLabel(selection.Label) != "legacy" {
		return estimateRealisticSamples(profile, duration, selection), nil
	}
	legacyStep := map[string]int{"7d": 15, "30d": 60, "1y": 300}[profile]
	points := duration / legacyStep
	actual := ActualActiveSeries(selection.Target)
	samples := actual * points
	return fmt.Sprintf("target_series≈%s actual_series≈%s instances/job=%s points/series≈%s samples≈%s disk≈%.1fGiB-headroom ch_compressed≈%.1fGiB-observed", comma(selection.Target), comma(actual), comma(InstancesPerJobForActiveSeries(selection.Target)), comma(points), comma(samples), float64(samples)*60/1024/1024/1024, float64(samples)*2.9/1024/1024/1024), nil
}

func workloadProfileForActiveLabel(label string) string {
	switch label {
	case "dashboard-50k":
		return "dashboard"
	case "envoy-heavy-50k":
		return "envoy-heavy"
	case "churn-50k":
		return "churn"
	default:
		return "legacy"
	}
}

func estimateRealisticSamples(profile string, duration int, selection ActiveSeriesSelection) string {
	if profile == "1y" {
		return fmt.Sprintf("target_series≈%s workload=%s samples≈n/a 1y-realistic=non-routine-use-legacy-stress-only", comma(selection.Target), workloadProfileForActiveLabel(selection.Label))
	}
	type family struct {
		fraction       float64
		intervalSecond int
		activeFraction float64
	}
	families := []family{}
	switch workloadProfileForActiveLabel(selection.Label) {
	case "dashboard":
		families = []family{{0.65, 60, 0.70}, {0.12, 15, 1.0}, {0.18, 60, 0.75}, {0.05, 300, 0.25}}
	case "envoy-heavy":
		families = []family{{0.82, 15, 1.0}, {0.10, 60, 0.85}, {0.05, 15, 1.0}, {0.03, 300, 0.25}}
	case "churn":
		families = []family{{0.50, 15, 0.40}, {0.15, 15, 0.55}, {0.25, 60, 0.45}, {0.10, 300, 0.25}}
	}
	var samples float64
	for _, f := range families {
		samples += float64(selection.Target) * f.fraction * (float64(duration) / float64(f.intervalSecond)) * f.activeFraction
	}
	return fmt.Sprintf("target_series≈%s workload=%s mixed_intervals=15s/60s/300s samples≈%s disk≈%.1fGiB-headroom ch_compressed≈%.1fGiB-observed", comma(selection.Target), workloadProfileForActiveLabel(selection.Label), comma(int(samples)), samples*60/1024/1024/1024, samples*2.9/1024/1024/1024)
}

func BuildSweepPlan(opts SweepPlanOptions) (SweepPlan, error) {
	profiles, err := ProfilesFor(opts.Profile)
	if err != nil {
		return SweepPlan{}, err
	}
	selections, err := ActiveSeriesSelections(opts.ActiveSeries, opts.ActiveSeriesPreset, opts.Density)
	if err != nil {
		return SweepPlan{}, err
	}
	artifactRoot := strings.Trim(strings.TrimSpace(opts.ArtifactRoot), "/")
	if artifactRoot == "" {
		artifactRoot = "harness/artifacts"
	}
	plan := SweepPlan{Options: opts, ArtifactDir: artifactRoot + "/bench/sweeps/" + opts.RunName}
	for _, p := range profiles {
		for _, selection := range selections {
			eval, err := ProfileEndTime(p, selection.Label)
			if err != nil {
				return plan, err
			}
			ds := SweepDatasetPlan{Profile: p, ActiveSeriesLabel: selection.Label, ActiveSeriesTarget: selection.Target, ActiveSeriesActual: ActualActiveSeries(selection.Target), EvalTime: eval}
			if opts.Estimate {
				est, err := EstimateSamples(p, selection)
				if err != nil {
					return plan, err
				}
				ds.Estimate = est
			}
			plan.Datasets = append(plan.Datasets, ds)
			if !opts.SkipBench {
				paths, err := CorpusPathsFor(p, opts.CorpusSet)
				if err != nil {
					return plan, err
				}
				for _, path := range paths {
					plan.Corpora = append(plan.Corpora, SweepCorpusPlan{Profile: p, ActiveSeriesLabel: selection.Label, Path: path})
				}
			}
		}
	}
	return plan, nil
}

func RenderSweepPlan(plan SweepPlan) string {
	o := plan.Options
	var b strings.Builder
	fmt.Fprintf(&b, "Sweep run: %s\n", o.RunName)
	fmt.Fprintf(&b, "Artifacts: %s\n", plan.ArtifactDir)
	fmt.Fprintf(&b, "Transport: %s\n", o.Transport)
	fmt.Fprintf(&b, "Seed policy: %s\n", o.SeedPolicy)
	fmt.Fprintf(&b, "Compliance: %s\n", enabled(!o.SkipCompliance))
	fmt.Fprintf(&b, "Benchmark: %s\n", enabled(!o.SkipBench))
	fmt.Fprintf(&b, "Benchmark modes: %s\n", o.ShimModes)
	fmt.Fprintf(&b, "Routing policies: %s\n", o.RoutingPolicies)
	fmt.Fprintf(&b, "Warmup routing policies: %s\n", valueOr(o.WarmupRoutingPolicies, "none"))
	fmt.Fprintf(&b, "Cost routing local families: %s\n", valueOr(o.CostRoutingLocalFamilies, "none"))
	fmt.Fprintf(&b, "Memory mode: %s\n", o.MemoryMode)
	fmt.Fprintf(&b, "ClickHouse profile mode: %s\n", o.ClickHouseProfileMode)
	fmt.Fprintf(&b, "Prometheus profile mode: %s\n", o.PrometheusProfileMode)
	fmt.Fprintf(&b, "ClickHouse reference profile: %s\n", o.ClickHouseReferenceProfile)
	fmt.Fprintf(&b, "promshim settings profile: %s\n\n", o.SettingsProfile)
	b.WriteString("Datasets:\n")
	for _, d := range plan.Datasets {
		fmt.Fprintf(&b, "  %-3s %-24s eval=%s", d.Profile, d.ActiveSeriesLabel, d.EvalTime)
		if d.Estimate != "" {
			fmt.Fprintf(&b, " %s", d.Estimate)
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if !o.SkipBench {
		b.WriteString("Benchmark corpora:\n")
		for _, c := range plan.Corpora {
			fmt.Fprintf(&b, "  %-3s %-24s %s\n", c.Profile, c.ActiveSeriesLabel, c.Path)
		}
	}
	return b.String()
}
func enabled(v bool) string {
	if v {
		return "enabled"
	}
	return "skipped"
}
func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	rem := len(s) % 3
	if rem == 0 {
		rem = 3
	}
	out = append(out, s[:rem]...)
	for i := rem; i < len(s); i += 3 {
		out = append(out, ',')
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}
