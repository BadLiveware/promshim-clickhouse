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
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promharness"
)

func main() {
	var (
		corpusPath     = flag.String("corpus", "harness/corpus/bench-native-lowering.json", "Path to bench corpus JSON.")
		artifactDir    = flag.String("artifact-dir", "harness/artifacts", "Directory to write bench-report.json.")
		repeats        = flag.Int("repeats", 10, "Timed repeats per (query, mode).")
		warmup         = flag.Int("warmup", 2, "Warmup repeats per (query, mode), discarded.")
		baselinePath   = flag.String("baseline", "", "Optional baseline bench report for regression comparison.")
		updateBaseline = flag.Bool("update-baseline", false, "Rewrite the baseline file from this run's results.")
		promURL        = flag.String("prom-url", "http://localhost:29090", "Prometheus base URL.")
		shimURL        = flag.String("shim-url", "http://localhost:29091", "promshim base URL.")
		timeoutFlag    = flag.Duration("timeout", 30*time.Second, "Per-request HTTP timeout.")
		evalTime       = flag.String("eval-time", "2026-04-21T21:45:42Z", "Bench evaluation time (manifest base). Corpus offsets are interpreted relative to this. Match the compliance fixture's end_time.")
	)
	flag.Parse()

	base, err := time.Parse(time.RFC3339, *evalTime)
	if err != nil {
		fail("parse --eval-time %q: %v", *evalTime, err)
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
