package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

func main() {
	var (
		action       = flag.String("action", "", "Action: memory-summary, memory-detail-manifest, or clickhouse-profile.")
		mode         = flag.String("mode", "summary", "ClickHouse profile mode: summary, auto, or processors.")
		chURL        = flag.String("ch-url", "http://localhost:28123", "ClickHouse HTTP URL.")
		chUser       = flag.String("ch-user", "default", "ClickHouse user.")
		chPassword   = flag.String("ch-password", "otel", "ClickHouse password.")
		shimURL      = flag.String("shim-url", "http://localhost:29091", "promshim URL.")
		reportPath   = flag.String("report", "", "Source v2 benchmark report path.")
		outputPath   = flag.String("output", "", "Output JSON path.")
		markdownPath = flag.String("markdown", "", "Output Markdown path for clickhouse-profile.")
		profilesDir  = flag.String("profiles-dir", "", "Per-query profile artifact directory.")
		detailDir    = flag.String("detail-dir", "", "Memory detail directory for memory-detail-manifest.")
		timeout      = flag.Duration("timeout", 30*time.Second, "HTTP timeout.")
	)
	flag.Parse()

	var err error
	switch *action {
	case "memory-summary":
		require("--report", *reportPath)
		require("--output", *outputPath)
		err = promharness.CaptureMemorySummary(promharness.CHMemoryCaptureOptions{
			ClickHouseURL: *chURL,
			User:          *chUser,
			Password:      *chPassword,
			PromshimURL:   *shimURL,
			ReportPath:    *reportPath,
			OutputPath:    *outputPath,
			Timeout:       *timeout,
		})
		if err == nil {
			fmt.Printf("Wrote %s\n", *outputPath)
		}
	case "memory-detail-manifest":
		require("--detail-dir", *detailDir)
		require("--report", *reportPath)
		err = promharness.WriteMemoryDetailManifest(*detailDir, *reportPath, *shimURL)
	case "clickhouse-profile":
		require("--report", *reportPath)
		require("--output", *outputPath)
		err = promharness.CaptureClickHouseProfile(promharness.CHProfileCaptureOptions{
			Mode:          *mode,
			ClickHouseURL: *chURL,
			User:          *chUser,
			Password:      *chPassword,
			PromshimURL:   *shimURL,
			ReportPath:    *reportPath,
			OutputPath:    *outputPath,
			MarkdownPath:  *markdownPath,
			ProfilesDir:   *profilesDir,
			Timeout:       *timeout,
		})
		if err == nil {
			fmt.Printf("Wrote %s\n", *outputPath)
			if *markdownPath != "" {
				fmt.Printf("Wrote %s\n", *markdownPath)
			}
			printProfileHighlights(*outputPath)
		}
	default:
		err = fmt.Errorf("--action must be memory-summary, memory-detail-manifest, or clickhouse-profile")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "promshim-ch-profile: %v\n", err)
		os.Exit(1)
	}
}

func printProfileHighlights(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var summary promharness.CHProfileSummary
	if err := json.Unmarshal(content, &summary); err != nil || len(summary.Rows) == 0 {
		return
	}
	rows := append([]promharness.CHProfileRow(nil), summary.Rows...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].QueryDurationP50MS == rows[j].QueryDurationP50MS {
			return rows[i].MemoryP95Bytes > rows[j].MemoryP95Bytes
		}
		return rows[i].QueryDurationP50MS > rows[j].QueryDurationP50MS
	})
	if len(rows) > 5 {
		rows = rows[:5]
	}
	fmt.Println("ClickHouse profile highlights:")
	for _, row := range rows {
		fmt.Printf("  %s mode=%s ch_p50=%gms mem_p95=%.0fB read_p50=%.0f join_p50=%.0f filter_p50=%.0f\n", row.QueryName, row.Mode, row.QueryDurationP50MS, row.MemoryP95Bytes, row.ReadRowsP50, row.JoinResultRowCountP50, row.FilterTransformPassedRowsP50)
	}
}

func require(name, value string) {
	if value == "" {
		fmt.Fprintf(os.Stderr, "promshim-ch-profile: %s is required\n", name)
		os.Exit(2)
	}
}
