// promshim-bench runs the native-SQL lowering tripwire benchmark.
//
//	./promshim-bench \
//	  --corpus harness/corpus/bench-native-lowering.json \
//	  --baseline harness/bench/baseline.json \
//	  --eval-time 2026-04-21T21:45:42Z
//
// The binary compares Prometheus vs. shim latency on each corpus query,
// reads X-Promshim-* response headers to pin strategy and ClickHouse
// round-trip count, and exits non-zero if strategy, CH round-trips, or
// native latency regressed against the committed baseline. See the plan
// at ~/.claude/plans/how-can-we-do-refactored-lerdorf.md for the full
// design.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

type runLabelFlags map[string]string

func (f runLabelFlags) String() string {
	if len(f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(f))
	for k, v := range f {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ",")
}

func (f runLabelFlags) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return fmt.Errorf("run label must be KEY=VALUE")
	}
	f[key] = val
	return nil
}

func main() {
	var (
		corpusPath     = flag.String("corpus", "harness/corpus/bench-native-lowering.json", "Path to bench corpus JSON.")
		artifactDir    = flag.String("artifact-dir", "harness/artifacts", "Directory to write bench report artifacts.")
		artifactName   = flag.String("artifact-name", "bench-report.json", "Artifact file name for v2 reports (default bench-report.json when v2 is enabled).")
		repeats        = flag.Int("repeats", 10, "Timed repeats per (query, mode).")
		warmup         = flag.Int("warmup", 2, "Warmup repeats per (query, mode), discarded.")
		baselinePath   = flag.String("baseline", "", "Optional baseline bench report for regression comparison.")
		updateBaseline = flag.Bool("update-baseline", false, "Rewrite the baseline file from this run's results.")
		promURL        = flag.String("prom-url", "http://localhost:29090", "Prometheus base URL.")
		shimURL        = flag.String("shim-url", "http://localhost:29091", "promshim base URL.")
		timeoutFlag    = flag.Duration("timeout", 30*time.Second, "Per-request HTTP timeout.")
		evalTime       = flag.String("eval-time", "2026-04-21T21:45:42Z", "Bench evaluation time (manifest base). Corpus offsets are interpreted relative to this. Match the compliance fixture's end_time.")
		shimModes      = flag.String("shim-modes", "", "Comma-separated shim native_lowering_mode values for v2 reports, e.g. prefer,force_supported,off.")
		includeProm    = flag.Bool("include-prom", true, "Include Prometheus baseline timing in v2 reports.")
		memoryMode     = flag.String("memory", "off", "Memory capture mode placeholder: off|summary|detailed. Detailed capture lands in a later sweep phase.")
		legacyReport   = flag.Bool("legacy-report", false, "Force legacy v1 bench report output even when v2 flags are present.")
	)
	runLabels := runLabelFlags{}
	flag.Var(runLabels, "run-label", "Run label for v2 reports, repeated as KEY=VALUE.")
	flag.Parse()

	includePromSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "include-prom" {
			includePromSet = true
		}
	})

	base, err := time.Parse(time.RFC3339, *evalTime)
	if err != nil {
		fail("parse --eval-time %q: %v", *evalTime, err)
	}

	modeList := splitCSV(*shimModes)
	useV2 := len(modeList) > 0 || includePromSet || len(runLabels) > 0 || *artifactName != "bench-report.json" || *memoryMode != "off"
	if *legacyReport {
		useV2 = false
	}
	if useV2 {
		report, err := promharness.RunBenchV2(promharness.BenchConfig{
			PromURL:        *promURL,
			ShimURL:        *shimURL,
			CorpusPath:     *corpusPath,
			ArtifactDir:    *artifactDir,
			ArtifactName:   *artifactName,
			Manifest:       promharness.Manifest{BaseUnixSeconds: base.Unix()},
			Repeats:        *repeats,
			WarmupRepeats:  *warmup,
			Timeout:        *timeoutFlag,
			ShimModes:      modeList,
			IncludeProm:    *includeProm,
			IncludePromSet: includePromSet,
			RunLabels:      map[string]string(runLabels),
			MemoryMode:     *memoryMode,
		})
		if err != nil {
			fail("RunBenchV2: %v", err)
		}
		printTableV2(report)
		fmt.Println("\nv2 report; skipping legacy baseline gate")
		return
	}

	report, err := promharness.RunBench(promharness.BenchConfig{
		PromURL:        *promURL,
		ShimURL:        *shimURL,
		CorpusPath:     *corpusPath,
		ArtifactDir:    *artifactDir,
		Manifest:       promharness.Manifest{BaseUnixSeconds: base.Unix()},
		Repeats:        *repeats,
		WarmupRepeats:  *warmup,
		BaselinePath:   *baselinePath,
		UpdateBaseline: *updateBaseline,
		Timeout:        *timeoutFlag,
	})
	if err != nil {
		fail("RunBench: %v", err)
	}

	printTable(report)

	if *updateBaseline && *baselinePath != "" {
		fmt.Printf("\nbaseline written to %s\n", *baselinePath)
		return
	}

	if *baselinePath == "" {
		fmt.Println("\nno --baseline provided; skipping regression gate")
		return
	}

	baseline, err := promharness.ReadBenchReport(*baselinePath)
	if err != nil {
		fmt.Printf("\nno baseline found at %s (%v); skipping regression gate\n", *baselinePath, err)
		return
	}
	regressions := promharness.CompareBaseline(report, baseline)
	if len(regressions) == 0 {
		fmt.Printf("\nregressions: 0 (vs %s)\n", *baselinePath)
		return
	}
	fmt.Printf("\nregressions: %d (vs %s)\n", len(regressions), *baselinePath)
	for _, r := range regressions {
		fmt.Printf("  [%s] %s: %s -> %s (%s)\n", r.Kind, r.Query, r.Before, r.After, r.Detail)
	}
	os.Exit(1)
}

func printTable(report promharness.BenchReport) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "QUERY\tENDPOINT\tSTRATEGY\tCH_RT\tCH_MS\tPROM_P50\tNATIVE_P50\tFALLBACK_P50\tN/P\tF/N\tNOTE")
	for _, row := range report.Rows {
		note := row.FallbackReason
		if row.StrategyFlap {
			note = "flap! " + note
		}
		if row.Error != "" && row.Strategy == "" {
			note = "err: " + row.Error
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%s\n",
			truncate(row.Name, 44),
			row.Endpoint,
			row.Strategy,
			row.CHRoundtrips,
			row.CHMillis,
			row.PromP50MS,
			row.NativeP50MS,
			row.FallbackP50MS,
			row.NativePromRatio,
			row.FallbackNativeRatio,
			truncate(note, 48),
		)
	}
	_ = w.Flush()

	fmt.Printf("\nquery count: %d", report.Summary.QueryCount)
	if len(report.Summary.StrategyHistogram) > 0 {
		parts := make([]string, 0, len(report.Summary.StrategyHistogram))
		for k, v := range report.Summary.StrategyHistogram {
			parts = append(parts, fmt.Sprintf("%s:%d", k, v))
		}
		fmt.Printf("  strategies: {%s}", strings.Join(parts, ", "))
	}
	fmt.Println()
}

func printTableV2(report promharness.BenchReportV2) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "QUERY\tENDPOINT\tMODE\tSTRATEGY\tCH_RT\tCH_MS\tPROM_P50\tPROM_BAND\tSHIM_P50\tS/P\tNOTE")
	for _, row := range report.Rows {
		promP50 := 0.0
		if row.Prom != nil {
			promP50 = row.Prom.P50MS
		}
		modes := make([]string, 0, len(row.Shim))
		for mode := range row.Shim {
			modes = append(modes, mode)
		}
		sort.Strings(modes)
		for _, mode := range modes {
			result := row.Shim[mode]
			note := result.FallbackReason
			if result.StrategyFlap {
				note = "flap! " + note
			}
			if result.Error != "" {
				note = "err: " + result.Error
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%.2f\t%s\t%.2f\t%.2f\t%s\n",
				truncate(row.Name, 44),
				row.Endpoint,
				mode,
				result.Strategy,
				result.CHRoundtrips,
				result.CHMillis,
				promP50,
				row.PromBand,
				result.P50MS,
				safePrintRatio(result.P50MS, promP50),
				truncate(note, 48),
			)
		}
	}
	_ = w.Flush()
	fmt.Printf("\nquery count: %d", report.Summary.QueryCount)
	if len(report.Summary.StrategyHistogram) > 0 {
		parts := make([]string, 0, len(report.Summary.StrategyHistogram))
		for k, v := range report.Summary.StrategyHistogram {
			parts = append(parts, fmt.Sprintf("%s:%d", k, v))
		}
		fmt.Printf("  strategies: {%s}", strings.Join(parts, ", "))
	}
	fmt.Println()
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func safePrintRatio(num, den float64) float64 {
	if den <= 0 {
		return 0
	}
	return num / den
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "promshim-bench: "+format+"\n", args...)
	os.Exit(2)
}
