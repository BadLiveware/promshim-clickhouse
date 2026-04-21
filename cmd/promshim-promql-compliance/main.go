package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/BadLiveware/promshim-ch/internal/promshim"
	"go.yaml.in/yaml/v2"
)

type suiteConfig struct {
	TestCases []suiteTestCase `yaml:"test_cases"`
}

type suiteTestCase struct {
	Query       string   `yaml:"query"`
	VariantArgs []string `yaml:"variant_args,omitempty"`
	ShouldFail  bool     `yaml:"should_fail,omitempty"`
}

type reportRow struct {
	Query                string   `json:"query"`
	ShouldFail           bool     `json:"shouldFail,omitempty"`
	Path2Status          string   `json:"path2Status"`
	ImplementationBucket string   `json:"implementationBucket,omitempty"`
	PlanSupported        bool     `json:"planSupported"`
	PlanDifficulty       string   `json:"planDifficulty,omitempty"`
	PlanReason           string   `json:"planReason,omitempty"`
	NativeLowerable      bool     `json:"nativeLowerable"`
	NativeReason         string   `json:"nativeReason,omitempty"`
	FragmentKind         string   `json:"fragmentKind,omitempty"`
	AggregationEligible  bool     `json:"aggregationEligible,omitempty"`
	OutputKind           string   `json:"outputKind,omitempty"`
	Notes                []string `json:"notes,omitempty"`
}

type statusSummary struct {
	Path2Status string `json:"path2Status"`
	Count       int    `json:"count"`
}

type bucketSummary struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

type complianceReport struct {
	SourcePath               string          `json:"sourcePath"`
	ExpandedQueries          int             `json:"expandedQueries"`
	ShouldFailQueries        int             `json:"shouldFailQueries"`
	Path2StatusSummary       []statusSummary `json:"path2StatusSummary"`
	UnsupportedBucketSummary []bucketSummary `json:"unsupportedBucketSummary,omitempty"`
	PartialBucketSummary     []bucketSummary `json:"partialBucketSummary,omitempty"`
	UnsupportedExamples      []reportRow     `json:"unsupportedExamples,omitempty"`
	PartialExamples          []reportRow     `json:"partialExamples,omitempty"`
}

var variantArgs = map[string][]string{
	"range":                {"1s", "15s", "1m", "5m", "15m", "1h"},
	"offset":               {"1m", "5m", "10m"},
	"simpleAggrOp":         {"sum", "avg", "max", "min", "count", "stddev", "stdvar"},
	"simpleTimeAggrOp":     {"sum", "avg", "max", "min", "count", "stddev", "stdvar", "absent", "last"},
	"topBottomOp":          {"topk", "bottomk"},
	"quantile":             {"-0.5", "0.1", "0.5", "0.75", "0.95", "0.90", "0.99", "1", "1.5"},
	"arithBinOp":           {"+", "-", "*", "/", "%", "^"},
	"compBinOp":            {"==", "!=", "<", ">", "<=", ">="},
	"binOp":                {"+", "-", "*", "/", "%", "^", "==", "!=", "<", ">", "<=", ">="},
	"simpleMathFunc":       {"abs", "ceil", "floor", "exp", "sqrt", "ln", "log2", "log10", "round"},
	"extrapolatedRateFunc": {"delta", "rate", "increase"},
	"clampFunc":            {"clamp_min", "clamp_max"},
	"instantRateFunc":      {"idelta", "irate"},
	"dateFunc":             {"day_of_month", "day_of_week", "days_in_month", "hour", "minute", "month", "year"},
	"smoothingFactor":      {"0.1", "0.5", "0.8"},
	"trendFactor":          {"0.1", "0.5", "0.8"},
}

func main() {
	source := flag.String("source", filepath.FromSlash("harness/compliance/prom-compliance/promql/promql-test-queries.yml"), "Prometheus compliance query YAML")
	jsonOut := flag.String("json-out", filepath.FromSlash(".pi/path2-promql-compliance-alignment.json"), "JSON output path")
	mdOut := flag.String("md-out", filepath.FromSlash(".pi/path2-promql-compliance-alignment.md"), "Markdown output path")
	maxExamples := flag.Int("max-examples", 40, "Max partial/unsupported example rows per section")
	flag.Parse()

	report, err := buildReport(*source, *maxExamples)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build report: %v\n", err)
		os.Exit(1)
	}
	if err := writeJSON(*jsonOut, report); err != nil {
		fmt.Fprintf(os.Stderr, "write json: %v\n", err)
		os.Exit(1)
	}
	if err := writeMarkdown(*mdOut, report); err != nil {
		fmt.Fprintf(os.Stderr, "write markdown: %v\n", err)
		os.Exit(1)
	}
}

func buildReport(source string, maxExamples int) (complianceReport, error) {
	payload, err := os.ReadFile(source)
	if err != nil {
		return complianceReport{}, err
	}
	var cfg suiteConfig
	if err := yaml.Unmarshal(payload, &cfg); err != nil {
		return complianceReport{}, err
	}

	queries := expandQueries(cfg.TestCases)
	report := complianceReport{SourcePath: source, ExpandedQueries: len(queries)}
	statusCounts := map[string]int{}
	unsupportedBucketCounts := map[string]int{}
	partialBucketCounts := map[string]int{}
	unsupported := make([]reportRow, 0)
	partial := make([]reportRow, 0)

	for _, tc := range queries {
		if tc.ShouldFail {
			report.ShouldFailQueries++
		}
		snapshot, err := promshim.MeasureNativeSupport(tc.Query)
		if err != nil {
			row := reportRow{Query: tc.Query, ShouldFail: tc.ShouldFail, Path2Status: "no", ImplementationBucket: promshim.ClassifyImplementationBucket(tc.Query, promshim.NativeMeasurementSnapshot{}, true), Notes: []string{err.Error()}}
			statusCounts[row.Path2Status]++
			unsupportedBucketCounts[row.ImplementationBucket]++
			if len(unsupported) < maxExamples {
				unsupported = append(unsupported, row)
			}
			continue
		}
		row := reportRow{
			Query:               tc.Query,
			ShouldFail:          tc.ShouldFail,
			PlanSupported:       snapshot.PlanSupported,
			PlanDifficulty:      snapshot.PlanDifficulty,
			PlanReason:          snapshot.PlanReason,
			NativeLowerable:     snapshot.NativeLowerable,
			NativeReason:        snapshot.NativeReason,
			FragmentKind:        snapshot.FragmentKind,
			AggregationEligible: snapshot.AggregationEligible,
			OutputKind:          snapshot.OutputKind,
		}
		row.Path2Status = promshim.ClassifyPath2Status(snapshot, tc.Query)
		row.ImplementationBucket = promshim.ClassifyImplementationBucket(tc.Query, snapshot, tc.ShouldFail)
		statusCounts[row.Path2Status]++
		switch row.Path2Status {
		case "no":
			unsupportedBucketCounts[row.ImplementationBucket]++
			if len(unsupported) < maxExamples {
				unsupported = append(unsupported, row)
			}
		case "partial":
			partialBucketCounts[row.ImplementationBucket]++
			if len(partial) < maxExamples {
				partial = append(partial, row)
			}
		}
	}

	keys := make([]string, 0, len(statusCounts))
	for k := range statusCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		report.Path2StatusSummary = append(report.Path2StatusSummary, statusSummary{Path2Status: k, Count: statusCounts[k]})
	}
	report.UnsupportedBucketSummary = summarizeBuckets(unsupportedBucketCounts)
	report.PartialBucketSummary = summarizeBuckets(partialBucketCounts)
	report.UnsupportedExamples = unsupported
	report.PartialExamples = partial
	return report, nil
}

func summarizeBuckets(counts map[string]int) []bucketSummary {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] == counts[keys[j]] {
			return keys[i] < keys[j]
		}
		return counts[keys[i]] > counts[keys[j]]
	})
	out := make([]bucketSummary, 0, len(keys))
	for _, key := range keys {
		out = append(out, bucketSummary{Bucket: key, Count: counts[key]})
	}
	return out
}

type expandedCase struct {
	Query      string
	ShouldFail bool
}

func expandQueries(cases []suiteTestCase) []expandedCase {
	results := make([]expandedCase, 0)
	for _, tc := range cases {
		for _, query := range expandQuery(tc.Query, tc.VariantArgs, map[string]string{}) {
			results = append(results, expandedCase{Query: query, ShouldFail: tc.ShouldFail})
		}
	}
	return results
}

func expandQuery(query string, remaining []string, args map[string]string) []string {
	if len(remaining) == 0 {
		return []string{renderTemplate(query, args)}
	}
	name := remaining[0]
	values := variantArgs[name]
	if len(values) == 0 {
		panic(fmt.Errorf("unknown variant arg %q", name))
	}
	filtered := make([]string, 0, len(remaining)-1)
	for _, item := range remaining[1:] {
		filtered = append(filtered, item)
	}
	out := make([]string, 0)
	for _, value := range values {
		next := make(map[string]string, len(args)+1)
		for k, v := range args {
			next[k] = v
		}
		next[name] = value
		out = append(out, expandQuery(query, filtered, next)...)
	}
	return out
}

func renderTemplate(raw string, data map[string]string) string {
	t := template.Must(template.New("query").Parse(raw))
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		panic(err)
	}
	return b.String()
}

func writeJSON(path string, report complianceReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func writeMarkdown(path string, report complianceReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Path 2 PromQL compliance alignment\n\n")
	b.WriteString("This file is generated from the read-only Prometheus compliance suite at `harness/compliance/prom-compliance/promql/promql-test-queries.yml`.\n\n")
	b.WriteString(fmt.Sprintf("- Source: `%s`\n", report.SourcePath))
	b.WriteString(fmt.Sprintf("- Expanded queries: `%d`\n", report.ExpandedQueries))
	b.WriteString(fmt.Sprintf("- `should_fail` queries: `%d`\n\n", report.ShouldFailQueries))
	b.WriteString("## Summary\n\n")
	b.WriteString("| Path 2 status | Count |\n")
	b.WriteString("|---|---:|\n")
	for _, item := range report.Path2StatusSummary {
		b.WriteString(fmt.Sprintf("| `%s` | %d |\n", item.Path2Status, item.Count))
	}
	b.WriteString("\n")
	writeBucketSummary(&b, "Unsupported implementation buckets", report.UnsupportedBucketSummary)
	writeBucketSummary(&b, "Partial implementation buckets", report.PartialBucketSummary)
	writeExamples(&b, "Unsupported examples", report.UnsupportedExamples)
	writeExamples(&b, "Partial examples", report.PartialExamples)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func writeBucketSummary(b *strings.Builder, title string, rows []bucketSummary) {
	if len(rows) == 0 {
		return
	}
	b.WriteString("## " + title + "\n\n")
	b.WriteString("| Bucket | Count |\n")
	b.WriteString("|---|---:|\n")
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("| `%s` | %d |\n", row.Bucket, row.Count))
	}
	b.WriteString("\n")
}

func writeExamples(b *strings.Builder, title string, rows []reportRow) {
	if len(rows) == 0 {
		return
	}
	b.WriteString("## " + title + "\n\n")
	b.WriteString("| Bucket | Query | Should fail | Plan reason | Native reason |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%t` | %s | %s |\n", escapeTable(row.ImplementationBucket), escapeTable(row.Query), row.ShouldFail, escapeTable(row.PlanReason), escapeTable(row.NativeReason)))
	}
	b.WriteString("\n")
}

func escapeTable(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
