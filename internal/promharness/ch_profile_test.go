package promharness

import (
	"strings"
	"testing"
)

func TestBuildCHProfileCommentsMatchesBenchLogComments(t *testing.T) {
	report := BenchReportV2{
		RunLabels: map[string]string{"run": "dense run/1"},
		Rows: []BenchRowV2{
			{
				Name:     "sum by(job) rate",
				Query:    "sum(rate(http_requests_total[5m])) by (job)",
				Endpoint: "query_range",
				Prom:     &BenchTiming{P50MS: 5},
				Shim: map[string]BenchShimModeResult{
					"prefer@strict": {BenchTiming: BenchTiming{P50MS: 20}, NativeLoweringMode: "prefer", RoutingPolicy: "strict", Strategy: "native_sql"},
					"off":           {BenchTiming: BenchTiming{P50MS: 10}, Strategy: "local"},
				},
			},
		},
	}
	comments := BuildCHProfileComments(report)
	if len(comments) != 2 {
		t.Fatalf("comments = %#v", comments)
	}
	if comments[0].LogComment != "promshim-bench run=dense_run_1 query=sum_by_job_rate mode=off" {
		t.Fatalf("unexpected first comment: %#v", comments[0])
	}
	if comments[1].LogComment != "promshim-bench run=dense_run_1 query=sum_by_job_rate mode=prefer policy=strict" {
		t.Fatalf("unexpected second comment: %#v", comments[1])
	}
	if comments[1].ShimPromRatio == nil || *comments[1].ShimPromRatio != 4 {
		t.Fatalf("ratio = %#v", comments[1].ShimPromRatio)
	}
}

func TestCHProfileUsesP50MetricsForMarkdown(t *testing.T) {
	rows := []CHProfileRow{
		{
			QueryName:                      "dense_avg",
			Mode:                           "prefer",
			QueryDurationP50MS:             750,
			MemoryP95Bytes:                 1536,
			ReadRowsTotal:                  9_000_000,
			ReadRowsP50:                    300_000,
			JoinResultRowCountTotal:        5_000_000_000,
			JoinResultRowCountP50:          4_000,
			FilterTransformPassedRowsTotal: 2_000_000_000,
			FilterTransformPassedRowsP50:   2_000,
			NativeSQLPath:                  "profiles/dense_avg__prefer__strict/native.sql",
		},
	}
	markdown := RenderCHProfileMarkdown("bench-report.json", rows, nil)
	if !strings.Contains(markdown, "| `dense_avg` | `prefer` | 750ms | 1.5KiB | 300.0K | 4.0K | 2.0K |") {
		t.Fatalf("markdown used totals or wrong formatting:\n%s", markdown)
	}
	if strings.Contains(markdown, "5.0B") || strings.Contains(markdown, "2.0B") {
		t.Fatalf("markdown should not display total counters as per-query metrics:\n%s", markdown)
	}
}

func TestCHProfileNeedsProcessors(t *testing.T) {
	ratio := 3.0
	cases := []struct {
		name string
		mode string
		row  CHProfileRow
		want bool
	}{
		{name: "summary never", mode: "summary", row: CHProfileRow{QueryDurationP50MS: 1000}, want: false},
		{name: "processors always", mode: "processors", row: CHProfileRow{}, want: true},
		{name: "auto duration", mode: "auto", row: CHProfileRow{QueryDurationP50MS: 500}, want: true},
		{name: "auto memory", mode: "auto", row: CHProfileRow{MemoryP95Bytes: 1_000_000_000}, want: true},
		{name: "auto fanout", mode: "auto", row: CHProfileRow{JoinResultRowCountP50: 100_000_000}, want: true},
		{name: "auto ratio", mode: "auto", row: CHProfileRow{QueryDurationP50MS: 100, ShimPromRatio: &ratio}, want: true},
		{name: "auto small", mode: "auto", row: CHProfileRow{QueryDurationP50MS: 99, ShimPromRatio: &ratio}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CHProfileNeedsProcessors(tc.mode, tc.row); got != tc.want {
				t.Fatalf("CHProfileNeedsProcessors() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCHProfileDirectoryNameNormalizesParts(t *testing.T) {
	got := CHProfileDirectoryName("rate by(job)", "prefer", "cost shadow")
	want := "rate_by_job___prefer__cost_shadow"
	if got != want {
		t.Fatalf("directory name = %q, want %q", got, want)
	}
}
