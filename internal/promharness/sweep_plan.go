package promharness

import (
	"fmt"
	"strings"
	"time"
)

type SweepPlanOptions struct {
	RunName                    string
	ArtifactRoot               string
	Profile                    string
	Density                    string
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
	ClickHouseReferenceProfile string
	SettingsProfile            string
	CorpusSet                  string
	Estimate                   bool
}

type SweepDatasetPlan struct {
	Profile  string
	Density  string
	EvalTime string
	Estimate string
}
type SweepCorpusPlan struct {
	Profile string
	Density string
	Path    string
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
func DensitiesFor(density string) ([]string, error) {
	switch density {
	case "all":
		return []string{"sparse", "dense"}, nil
	case "sparse", "dense", "stress-50k", "stress-500k":
		return []string{density}, nil
	default:
		return nil, fmt.Errorf("--density must be sparse|dense|stress-50k|stress-500k|all (got: %s)", density)
	}
}

func ProfileEndTime(profile, density string) (string, error) {
	base := map[string]string{"7d": "2026-03-22T21:45:42Z", "30d": "2026-02-22T21:45:42Z", "1y": "2025-03-22T21:45:42Z"}
	durations := map[string]time.Duration{"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "1y": 365 * 24 * time.Hour}
	slots := map[string]int{"sparse": 0, "dense": 1, "stress-50k": 2, "stress-500k": 3}
	b, ok := base[profile]
	if !ok {
		return "", fmt.Errorf("unknown profile %s", profile)
	}
	slot, ok := slots[density]
	if !ok {
		return "", fmt.Errorf("unknown density %s", density)
	}
	t, err := time.Parse(time.RFC3339, b)
	if err != nil {
		return "", err
	}
	if slot > 0 {
		t = t.Add(-time.Duration(slot) * durations[profile]).Add(-time.Duration(slot) * 24 * time.Hour)
	}
	return t.UTC().Format(time.RFC3339), nil
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

func EstimateSamples(profile, density string) (string, error) {
	prof := map[string][2]int{"7d": {7 * 24 * 3600, 15}, "30d": {30 * 24 * 3600, 60}, "1y": {365 * 24 * 3600, 300}}
	p, ok := prof[profile]
	if !ok {
		return "", fmt.Errorf("unknown profile %s", profile)
	}
	points := p[0] / p[1]
	instances := 0
	switch density {
	case "sparse":
		instances = 5
	case "dense":
		if profile == "1y" {
			instances = 50
		} else {
			instances = 100
		}
	case "stress-50k":
		instances = 1924
	case "stress-500k":
		instances = 19231
	default:
		return "", fmt.Errorf("unknown density %s", density)
	}
	series := 2 * instances * 13
	samples := series * points
	return fmt.Sprintf("series≈%s points/series≈%s samples≈%s disk≈%.1fGiB-headroom", comma(series), comma(points), comma(samples), float64(samples)*60/1024/1024/1024), nil
}

func BuildSweepPlan(opts SweepPlanOptions) (SweepPlan, error) {
	profiles, err := ProfilesFor(opts.Profile)
	if err != nil {
		return SweepPlan{}, err
	}
	densities, err := DensitiesFor(opts.Density)
	if err != nil {
		return SweepPlan{}, err
	}
	artifactRoot := strings.Trim(strings.TrimSpace(opts.ArtifactRoot), "/")
	if artifactRoot == "" {
		artifactRoot = "harness/artifacts"
	}
	plan := SweepPlan{Options: opts, ArtifactDir: artifactRoot + "/bench/sweeps/" + opts.RunName}
	for _, p := range profiles {
		for _, d := range densities {
			eval, err := ProfileEndTime(p, d)
			if err != nil {
				return plan, err
			}
			ds := SweepDatasetPlan{Profile: p, Density: d, EvalTime: eval}
			if opts.Estimate {
				est, err := EstimateSamples(p, d)
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
					plan.Corpora = append(plan.Corpora, SweepCorpusPlan{Profile: p, Density: d, Path: path})
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
	fmt.Fprintf(&b, "ClickHouse reference profile: %s\n", o.ClickHouseReferenceProfile)
	fmt.Fprintf(&b, "promshim settings profile: %s\n\n", o.SettingsProfile)
	b.WriteString("Datasets:\n")
	for _, d := range plan.Datasets {
		fmt.Fprintf(&b, "  %-3s %-6s eval=%s", d.Profile, d.Density, d.EvalTime)
		if d.Estimate != "" {
			fmt.Fprintf(&b, " %s", d.Estimate)
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if !o.SkipBench {
		b.WriteString("Benchmark corpora:\n")
		for _, c := range plan.Corpora {
			fmt.Fprintf(&b, "  %-3s %-6s %s\n", c.Profile, c.Density, c.Path)
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
