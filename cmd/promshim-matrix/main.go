package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/compliance"
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

type matrixRow struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	Name                 string   `json:"name"`
	Query                string   `json:"query,omitempty"`
	Path2Status          string   `json:"path2Status"`
	ImplementationBucket string   `json:"implementationBucket,omitempty"`
	PrometheusVersion    string   `json:"prometheusVersion"`
	PlanSupported        bool     `json:"planSupported"`
	PlanDifficulty       string   `json:"planDifficulty,omitempty"`
	PlanReason           string   `json:"planReason,omitempty"`
	NativeLowerable      bool     `json:"nativeLowerable"`
	NativeReason         string   `json:"nativeReason,omitempty"`
	FragmentKind         string   `json:"fragmentKind,omitempty"`
	AggregationEligible  bool     `json:"aggregationEligible,omitempty"`
	Notes                []string `json:"notes,omitempty"`
}

type bucketSummary struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

type matrixDocument struct {
	PrometheusVersion        string          `json:"prometheusVersion"`
	UnsupportedBucketSummary []bucketSummary `json:"unsupportedBucketSummary,omitempty"`
	PartialBucketSummary     []bucketSummary `json:"partialBucketSummary,omitempty"`
	Rows                     []matrixRow     `json:"rows"`
}

type featureSpec struct {
	Kind  string
	Name  string
	Query string
	Notes []string
}

const prometheusVersion = "v0.311.2"

var partialOverrides = map[string][]string{}

func main() {
	jsonOut := flag.String("json-out", filepath.FromSlash(".pi/path2-compliance-matrix.json"), "JSON output path")
	mdOut := flag.String("md-out", filepath.FromSlash(".pi/path2-compliance-matrix.md"), "Markdown output path")
	flag.Parse()

	rows, err := buildRows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build matrix: %v\n", err)
		os.Exit(1)
	}
	doc := matrixDocument{
		PrometheusVersion:        prometheusVersion,
		UnsupportedBucketSummary: summarizeBuckets(rows, "no"),
		PartialBucketSummary:     summarizeBuckets(rows, "partial"),
		Rows:                     rows,
	}

	if err := writeJSON(*jsonOut, doc); err != nil {
		fmt.Fprintf(os.Stderr, "write json: %v\n", err)
		os.Exit(1)
	}
	if err := writeMarkdown(*mdOut, doc); err != nil {
		fmt.Fprintf(os.Stderr, "write markdown: %v\n", err)
		os.Exit(1)
	}
}

func buildRows() ([]matrixRow, error) {
	rows := make([]matrixRow, 0)

	functionNames := make([]string, 0, len(parser.Functions))
	for name := range parser.Functions {
		functionNames = append(functionNames, name)
	}
	sort.Strings(functionNames)
	for _, name := range functionNames {
		query, notes, ok := canonicalFunctionQuery(name)
		if !ok {
			continue
		}
		row, err := buildRow(featureSpec{Kind: "function", Name: name, Query: query, Notes: notes})
		if err != nil {
			return nil, fmt.Errorf("function %s: %w", name, err)
		}
		rows = append(rows, row)
	}

	for _, spec := range additionalFeatureSpecs() {
		row, err := buildRow(spec)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", spec.Kind, spec.Name, err)
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

func buildRow(spec featureSpec) (matrixRow, error) {
	snapshot, err := compliance.MeasureNativeSupport(spec.Query)
	if err != nil {
		return matrixRow{}, err
	}
	status := compliance.ClassifyPath2Status(snapshot, spec.Query)
	if notes, ok := partialOverrides[spec.Name]; ok && status == "partial" && snapshot.PlanSupported {
		spec.Notes = append(spec.Notes, notes...)
	}
	row := matrixRow{
		ID:                   fmt.Sprintf("%s:%s", spec.Kind, spec.Name),
		Kind:                 spec.Kind,
		Name:                 spec.Name,
		Query:                spec.Query,
		Path2Status:          status,
		ImplementationBucket: compliance.ClassifyImplementationBucket(spec.Query, snapshot, false),
		PrometheusVersion:    prometheusVersion,
		PlanSupported:        snapshot.PlanSupported,
		PlanDifficulty:       snapshot.PlanDifficulty,
		PlanReason:           snapshot.PlanReason,
		NativeLowerable:      snapshot.NativeLowerable,
		NativeReason:         snapshot.NativeReason,
		FragmentKind:         snapshot.FragmentKind,
		AggregationEligible:  snapshot.AggregationEligible,
		Notes:                append([]string(nil), spec.Notes...),
	}
	return row, nil
}

func writeJSON(path string, doc matrixDocument) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func writeMarkdown(path string, doc matrixDocument) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Path 2 compliance matrix\n\n")
	b.WriteString("This file is generated by `go run ./cmd/promshim-matrix`.\n\n")
	b.WriteString(fmt.Sprintf("Prometheus parser version: `%s`\n\n", doc.PrometheusVersion))
	writeBucketSummary(&b, "Unsupported implementation buckets", doc.UnsupportedBucketSummary)
	writeBucketSummary(&b, "Partial implementation buckets", doc.PartialBucketSummary)
	for _, kind := range []string{"selector", "modifier", "aggregation", "binary", "vector_matching", "function"} {
		kindRows := rowsForKind(doc.Rows, kind)
		if len(kindRows) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("## %s\n\n", strings.ReplaceAll(kind, "_", " ")))
		b.WriteString("| Name | Path 2 | Bucket | Canonical query | Notes |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, row := range kindRows {
			b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | `%s` | %s |\n", row.Name, row.Path2Status, row.ImplementationBucket, escapeTable(row.Query), escapeTable(strings.Join(row.Notes, "; "))))
		}
		b.WriteString("\n")
	}
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

func summarizeBuckets(rows []matrixRow, status string) []bucketSummary {
	counts := map[string]int{}
	for _, row := range rows {
		if row.Path2Status != status || row.ImplementationBucket == "" {
			continue
		}
		counts[row.ImplementationBucket]++
	}
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

func rowsForKind(rows []matrixRow, kind string) []matrixRow {
	filtered := make([]matrixRow, 0)
	for _, row := range rows {
		if row.Kind == kind {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func escapeTable(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func canonicalFunctionQuery(name string) (string, []string, bool) {
	switch name {
	case "absent":
		return `absent(nonexistent_metric{job="api"})`, nil, true
	case "absent_over_time":
		return `absent_over_time(nonexistent_metric{job="api"}[5m])`, nil, true
	case "clamp":
		return `clamp(harness_up, 0, 1)`, nil, true
	case "clamp_min":
		return `clamp_min(harness_up, 0)`, nil, true
	case "clamp_max":
		return `clamp_max(harness_up, 1)`, nil, true
	case "double_exponential_smoothing", "holt_winters":
		return `double_exponential_smoothing(harness_queue_depth[5m], 0.5, 0.3)`, nil, true
	case "histogram_quantile":
		return `histogram_quantile(0.9, sum by (le) (rate(harness_request_duration_seconds_bucket[5m])))`, nil, true
	case "histogram_fraction":
		return `histogram_fraction(0, 1, sum by (le) (rate(harness_request_duration_seconds_bucket[5m])))`, nil, true
	case "histogram_count":
		return `histogram_count(sum by (le) (rate(harness_request_duration_seconds_bucket[5m])))`, nil, true
	case "histogram_sum":
		return `histogram_sum(sum by (le) (rate(harness_request_duration_seconds_bucket[5m])))`, nil, true
	case "histogram_avg":
		return `histogram_avg(sum by (le) (rate(harness_request_duration_seconds_bucket[5m])))`, nil, true
	case "info":
		return `info(harness_up)`, nil, true
	case "label_join":
		return `label_join(harness_up, "joined", "/", "job", "instance")`, nil, true
	case "label_replace":
		return `label_replace(harness_up, "job_copy", "$1", "job", "(.*)")`, nil, true
	case "predict_linear":
		return `predict_linear(harness_queue_depth[5m], 60)`, nil, true
	case "quantile_over_time":
		return `quantile_over_time(0.5, harness_queue_depth[5m])`, nil, true
	case "vector":
		return `vector(1)`, nil, true
	case "sort_by_label":
		return `sort_by_label(harness_up, "job", "instance")`, []string{"PromQL experimental sort-by-label function"}, true
	case "sort_by_label_desc":
		return `sort_by_label_desc(harness_up, "job", "instance")`, []string{"PromQL experimental sort-by-label function"}, true
	}

	fn := parser.Functions[name]
	if fn == nil {
		return "", nil, false
	}
	args := make([]string, 0, len(fn.ArgTypes))
	for _, argType := range fn.ArgTypes {
		args = append(args, genericArgument(name, argType))
	}
	return fmt.Sprintf("%s(%s)", name, strings.Join(args, ", ")), nil, true
}

func genericArgument(name string, valueType parser.ValueType) string {
	switch valueType {
	case parser.ValueTypeVector:
		if name == "timestamp" {
			return "harness_up"
		}
		return "harness_up"
	case parser.ValueTypeMatrix:
		switch name {
		case "rate", "irate", "increase", "delta", "idelta", "changes", "deriv", "resets":
			return "harness_requests_total[5m]"
		default:
			return "harness_queue_depth[5m]"
		}
	case parser.ValueTypeScalar:
		return "1"
	case parser.ValueTypeString:
		return `"job"`
	default:
		return "1"
	}
}

func additionalFeatureSpecs() []featureSpec {
	return []featureSpec{
		{Kind: "selector", Name: "instant_selector", Query: `harness_up`},
		{Kind: "selector", Name: "matcher_equality", Query: `harness_up{job="api"}`},
		{Kind: "selector", Name: "matcher_inequality", Query: `harness_up{job!="api"}`},
		{Kind: "selector", Name: "matcher_regex", Query: `harness_up{job=~"api|worker"}`},
		{Kind: "selector", Name: "matcher_negative_regex", Query: `harness_up{job!~"worker"}`},
		{Kind: "modifier", Name: "offset", Query: `harness_up offset 60s`},
		{Kind: "modifier", Name: "at_literal", Query: `harness_up @ 1710000000`},
		{Kind: "modifier", Name: "at_start", Query: `harness_up @ start()`},
		{Kind: "modifier", Name: "at_end", Query: `harness_up @ end()`},
		{Kind: "modifier", Name: "subquery_selector", Query: `harness_requests_total[5m:60s]`},
		{Kind: "aggregation", Name: "sum", Query: `sum by (job) (harness_up)`},
		{Kind: "aggregation", Name: "count", Query: `count by (job) (harness_up)`},
		{Kind: "aggregation", Name: "min", Query: `min by (job) (harness_queue_depth)`},
		{Kind: "aggregation", Name: "max", Query: `max by (job) (harness_queue_depth)`},
		{Kind: "aggregation", Name: "avg", Query: `avg by (job) (harness_queue_depth)`},
		{Kind: "aggregation", Name: "stddev", Query: `stddev by (job) (harness_queue_depth)`},
		{Kind: "aggregation", Name: "stdvar", Query: `stdvar by (job) (harness_queue_depth)`},
		{Kind: "aggregation", Name: "quantile", Query: `quantile by (job) (0.5, harness_queue_depth)`},
		{Kind: "aggregation", Name: "group", Query: `group by (job) (harness_up)`},
		{Kind: "aggregation", Name: "topk", Query: `topk(3, harness_up)`},
		{Kind: "aggregation", Name: "bottomk", Query: `bottomk(3, harness_up)`},
		{Kind: "aggregation", Name: "count_values", Query: `count_values("sample_value", harness_up)`},
		{Kind: "aggregation", Name: "limitk", Query: `limitk(2, harness_up)`, Notes: []string{"PromQL experimental aggregation"}},
		{Kind: "aggregation", Name: "limit_ratio", Query: `limit_ratio(0.5, harness_up)`, Notes: []string{"PromQL experimental aggregation"}},
		{Kind: "binary", Name: "add", Query: `harness_up + harness_up`},
		{Kind: "binary", Name: "sub", Query: `harness_up - harness_up`},
		{Kind: "binary", Name: "mul", Query: `harness_up * harness_up`},
		{Kind: "binary", Name: "div", Query: `harness_up / harness_up`},
		{Kind: "binary", Name: "mod", Query: `harness_up % harness_up`},
		{Kind: "binary", Name: "pow", Query: `harness_up ^ harness_up`},
		{Kind: "binary", Name: "eq", Query: `harness_up == harness_up`},
		{Kind: "binary", Name: "eq_bool", Query: `harness_up == bool harness_up`},
		{Kind: "binary", Name: "neq", Query: `harness_up != harness_up`},
		{Kind: "binary", Name: "gt", Query: `harness_up > harness_up`},
		{Kind: "binary", Name: "lt", Query: `harness_up < harness_up`},
		{Kind: "binary", Name: "gte", Query: `harness_up >= harness_up`},
		{Kind: "binary", Name: "lte", Query: `harness_up <= harness_up`},
		{Kind: "binary", Name: "and", Query: `harness_up and on(job) harness_up`},
		{Kind: "binary", Name: "or", Query: `harness_up or on(job) harness_up`},
		{Kind: "binary", Name: "unless", Query: `harness_up unless on(job) harness_up`},
		{Kind: "vector_matching", Name: "on", Query: `harness_up * on(job) harness_up`},
		{Kind: "vector_matching", Name: "ignoring", Query: `harness_up * ignoring(instance) harness_up`},
		{Kind: "vector_matching", Name: "group_left", Query: `harness_up * on(job) group_left sum by (job) (harness_up)`},
		{Kind: "vector_matching", Name: "group_right", Query: `sum by (job) (harness_up) * on(job) group_right harness_up`},
		{Kind: "vector_matching", Name: "fill", Query: `harness_up + on(job,instance) fill(0) harness_up`, Notes: []string{"single-row fill modifier coverage; left/right-only variants still need explicit matrix rows if they become parser-surface queries"}},
	}
}

func init() {
	// Sanity check a couple of canonical expressions while the command starts so
	// generator failures surface as explicit command errors rather than partially
	// written files.
	for _, query := range []string{
		`harness_up`,
		`sum by (job) (harness_up)`,
		`harness_up + on(job,instance) fill(0) harness_up`,
	} {
		if _, err := logicalpkg.ParseExpression(query); err != nil {
			panic(err)
		}
	}
}
