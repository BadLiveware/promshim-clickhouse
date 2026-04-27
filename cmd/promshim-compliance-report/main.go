package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/compliance"
)

func main() {
	action := flag.String("action", "", "Action: reconcile, classify, native-gap, summary.")
	reportPath := flag.String("report", "", "Compliance report path.")
	allowPath := flag.String("allowlist", "harness/compliance/expected-failures.json", "Expected failures allowlist.")
	mode := flag.String("mode", "", "Compliance mode for mode-tagged allowlist entries.")
	bucket := flag.String("bucket", "", "Failure bucket to dump for classify action.")
	limit := flag.Int("limit", 0, "Maximum details for classify bucket dump.")
	flag.Parse()
	if *reportPath == "" {
		fail("--report is required")
	}
	report, err := compliance.LoadTesterReport(*reportPath)
	if err != nil {
		fail("load report: %v", err)
	}
	switch *action {
	case "summary":
		fmt.Printf("%#v\n", compliance.ComplianceSummary(report))
	case "reconcile":
		allow, err := compliance.LoadExpectedFailures(*allowPath)
		if err != nil {
			fail("load allowlist: %v", err)
		}
		summary := compliance.ReconcileExpectedFailures(report, allow, *mode)
		fmt.Print(compliance.FormatReconcileText(*reportPath, *allowPath, *mode, summary))
		if !compliance.ReconcileIsClean(summary) {
			os.Exit(1)
		}
	case "classify":
		if *bucket == "" {
			printBuckets(report, *reportPath)
		} else {
			printBucketDetails(report, *bucket, *limit)
		}
	case "native-gap":
		printNativeGap(report, *reportPath)
	default:
		fail("--action must be reconcile, classify, native-gap, or summary")
	}
}

func printBuckets(report compliance.TesterReport, reportPath string) {
	fmt.Printf("report: %s\n", reportPath)
	fmt.Println()
	fmt.Println("  COUNT  BUCKET                      SAMPLE QUERIES")
	fmt.Println("  -----  ------                      --------------")
	passed, failed := 0, 0
	for _, b := range compliance.FailureBuckets(report) {
		fmt.Printf("  %-5d  %-26s  %s\n", b.Count, b.Bucket, sample(b.Samples, 0))
		for i := 1; i < len(b.Samples); i++ {
			fmt.Printf("  %-33s%s\n", "", sample(b.Samples, i))
		}
		if b.Bucket == "passed" {
			passed = b.Count
		} else {
			failed += b.Count
		}
	}
	fmt.Println()
	fmt.Printf("  total %d / passed %d / failed %d\n", report.TotalResults, passed, failed)
}

func printBucketDetails(report compliance.TesterReport, bucket string, limit int) {
	shown := 0
	for _, r := range report.Results {
		if compliance.ClassifyFailureBucket(r) != bucket {
			continue
		}
		if limit > 0 && shown >= limit {
			break
		}
		shown++
		fmt.Printf("QUERY:  %s", r.TestCase.Query)
		if r.UnexpectedFailure != "" {
			fmt.Printf("\nERROR:  %s", r.UnexpectedFailure)
		}
		if r.Diff != "" {
			d := r.Diff
			if len(d) > 2500 {
				d = d[:2500]
			}
			fmt.Printf("\nDIFF:\n%s", d)
		}
		fmt.Print("\n--------------------------------------------------------------------------------\n")
	}
}

func printNativeGap(report compliance.TesterReport, reportPath string) {
	s := compliance.NativeGapReport(report)
	fmt.Println("native-mode compliance gap report")
	fmt.Printf("report: %s\n\n", reportPath)
	pct := 0.0
	if s.Total > 0 {
		pct = float64(s.Passed) / float64(s.Total) * 100
	}
	fmt.Printf("total queries      : %d\n", s.Total)
	fmt.Printf("passing on native  : %d (%.1f%%)\n", s.Passed, pct)
	fmt.Printf("diff failures      : %d  (native lowered but returned wrong values — real bugs)\n", s.DiffFailure)
	fmt.Printf("unsupported root   : %d  (planner refuses to lower — coverage gaps)\n", s.UnsupportedRoot)
	fmt.Printf("other errors       : %d\n\n", s.UnexpectedFailureOther)
	if s.DiffFailure > 0 {
		fmt.Println("== diff failures (native-SQL correctness bugs) ==")
		for _, r := range report.Results {
			if r.Diff != "" {
				d := r.Diff
				if len(d) > 200 {
					d = d[:200]
				}
				fmt.Printf("QUERY: %s\n  DIFF: %s...\n\n", r.TestCase.Query, d)
			}
		}
	}
	if s.UnsupportedRoot > 0 {
		fmt.Println("== unsupported-root queries (native lowering not yet implemented for these shapes) ==")
		fmt.Println("Grouped by normalized shape (metrics/numbers/strings collapsed):")
		fmt.Println()
		for _, shape := range s.UnsupportedShapes {
			fmt.Printf("%7d %s\n", shape.Count, shape.Shape)
		}
		fmt.Println()
	}
	if s.UnexpectedFailureOther > 0 {
		fmt.Println("== other unexpected failures ==")
		for _, r := range report.Results {
			if r.UnexpectedFailure != "" && !strings.Contains(r.UnexpectedFailure, "requires a native_sql root plan") {
				fmt.Printf("QUERY: %s\n  ERR : %s\n\n", r.TestCase.Query, r.UnexpectedFailure)
			}
		}
	}
	fmt.Println("note: native gaps are tracked openly — they are NOT allowlisted in")
	fmt.Println("      expected-failures.json. Each number above should trend down as")
	fmt.Println("      native lowering coverage grows.")
}

func sample(samples []string, i int) string {
	if i >= len(samples) {
		return ""
	}
	s := samples[i]
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "promshim-compliance-report: "+format+"\n", args...)
	os.Exit(2)
}
