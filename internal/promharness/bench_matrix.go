package promharness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type BenchMatrixOptions struct {
	RepoRoot  string
	SweepPath string
	Profiles  []BenchMatrixProfileInput
	SortBy    string
	PerQuery  bool
}

type BenchMatrixProfileInput struct {
	Profile string
	Path    string
}

type benchMatrixSweepRow struct {
	Category          string
	Query             string
	Profile           string
	Density           string
	Transport         string
	Corpus            string
	Mode              string
	RoutingPolicy     string
	Strategy          string
	StrictCandidate   string
	SelectedCandidate string
	ServedCandidate   string
	CandidateFlip     bool
	PromP50MS         *float64
	ShimP50MS         *float64
	Ratio             *float64
	PromBand          string
}

type legacyMatrixRow struct {
	Category            string
	Name                string
	Profile             string
	PromP50MS           float64
	NativeP50MS         float64
	NativePromRatio     float64
	FallbackNativeRatio float64
}

func RenderBenchMatrix(opts BenchMatrixOptions) (string, error) {
	if opts.SortBy == "" {
		opts.SortBy = "np"
	}
	switch opts.SortBy {
	case "np", "fn", "cat", "category":
	default:
		return "", fmt.Errorf("unknown --sort-by: %s", opts.SortBy)
	}
	if opts.SweepPath != "" {
		return renderSweepBenchMatrix(opts)
	}
	return renderLegacyBenchMatrix(opts)
}

func renderSweepBenchMatrix(opts BenchMatrixOptions) (string, error) {
	root := opts.RepoRoot
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	content, err := os.ReadFile(opts.SweepPath)
	if err != nil {
		return "", fmt.Errorf("read sweep manifest: %w", err)
	}
	var manifest SweepManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", fmt.Errorf("decode sweep manifest: %w", err)
	}
	rows := []benchMatrixSweepRow{}
	for _, meta := range manifest.Bench.Reports {
		reportPath := filepath.Join(root, filepath.FromSlash(meta.Path))
		report, err := loadBenchReportV2(reportPath)
		if err != nil {
			continue
		}
		profile := firstNonEmpty(meta.Profile, report.RunLabels["profile"], "unknown")
		density := firstNonEmpty(meta.Density, report.RunLabels["density"], "unknown")
		transport := firstNonEmpty(meta.Transport, report.RunLabels["transport"], "unknown")
		corpus := strings.TrimSuffix(filepath.Base(firstNonEmpty(report.CorpusPath, reportPath)), filepath.Ext(firstNonEmpty(report.CorpusPath, reportPath)))
		for _, row := range report.Rows {
			var prom *float64
			if row.Prom != nil {
				v := row.Prom.P50MS
				prom = &v
			}
			modes := make([]string, 0, len(row.Shim))
			for mode := range row.Shim {
				modes = append(modes, mode)
			}
			sort.Strings(modes)
			for _, mode := range modes {
				result := row.Shim[mode]
				shim := result.P50MS
				var ratio *float64
				if prom != nil && *prom != 0 {
					v := shim / *prom
					ratio = &v
				}
				strict := result.StrictCandidate
				selected := result.SelectedCandidate
				rows = append(rows, benchMatrixSweepRow{Category: firstNonEmpty(row.Category, "uncategorized"), Query: row.Name, Profile: profile, Density: density, Transport: transport, Corpus: corpus, Mode: mode, RoutingPolicy: result.RoutingPolicy, Strategy: result.Strategy, StrictCandidate: strict, SelectedCandidate: selected, ServedCandidate: result.ServedCandidate, CandidateFlip: strict != "" && selected != "" && strict != selected, PromP50MS: prom, ShimP50MS: &shim, Ratio: ratio, PromBand: firstNonEmpty(row.PromBand, "n/a")})
			}
		}
	}
	var b strings.Builder
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "## Sweep benchmark matrix: %s\n\n", firstNonEmpty(manifest.RunName, filepath.Base(filepath.Dir(opts.SweepPath))))
	fmt.Fprintf(&b, "Manifest: `%s`\n\n", opts.SweepPath)
	if opts.PerQuery {
		b.WriteString("| Category | Query | Profile | Density | Transport | Corpus | Mode | Routing policy | Strategy | Strict candidate | Selected candidate | Served candidate | Candidate flip | Prom band | Prom p50 | Shim p50 | S/P |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---:|---:|---:|\n")
		sort.Slice(rows, func(i, j int) bool { return sweepPerQueryKey(rows[i]) < sweepPerQueryKey(rows[j]) })
		for _, row := range rows {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", row.Category, row.Query, row.Profile, row.Density, row.Transport, row.Corpus, row.Mode, valueOr(row.RoutingPolicy, "n/a"), row.Strategy, valueOr(row.StrictCandidate, "n/a"), valueOr(row.SelectedCandidate, "n/a"), valueOr(row.ServedCandidate, "n/a"), yesNo(row.CandidateFlip), row.PromBand, formatOptional(row.PromP50MS), formatOptional(row.ShimP50MS), formatRatio(row.Ratio))
		}
	} else {
		b.WriteString("| Category | Profile | Density | Transport | Mode | Routing policy | Count | Strategies | Candidate flips | Prom p50 med | Shim p50 med | S/P med | Target bands |\n")
		b.WriteString("|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|\n")
		buckets := map[string][]benchMatrixSweepRow{}
		keys := []string{}
		for _, row := range rows {
			key := strings.Join([]string{row.Category, row.Profile, row.Density, row.Transport, row.Mode, row.RoutingPolicy}, "\x00")
			if _, ok := buckets[key]; !ok {
				keys = append(keys, key)
			}
			buckets[key] = append(buckets[key], row)
		}
		sort.Strings(keys)
		for _, key := range keys {
			vals := buckets[key]
			parts := strings.Split(key, "\x00")
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %d | %s | %d | %s | %s | %s | %s |\n", parts[0], parts[1], parts[2], parts[3], parts[4], valueOr(parts[5], "n/a"), len(vals), strategyCounts(vals), countFlips(vals), formatOptional(medianPtr(extract(vals, func(r benchMatrixSweepRow) *float64 { return r.PromP50MS }))), formatOptional(medianPtr(extract(vals, func(r benchMatrixSweepRow) *float64 { return r.ShimP50MS }))), formatRatio(medianPtr(extract(vals, func(r benchMatrixSweepRow) *float64 { return r.Ratio }))), bandCounts(vals))
		}
	}
	b.WriteString("\n")
	return b.String(), nil
}

func renderLegacyBenchMatrix(opts BenchMatrixOptions) (string, error) {
	profiles := opts.Profiles
	if len(profiles) == 0 {
		profiles = []BenchMatrixProfileInput{{"7d", "harness/artifacts/bench/standalone/latest/bench-report-7d.json"}, {"30d", "harness/artifacts/bench/standalone/latest/bench-report-30d.json"}, {"1y", "harness/artifacts/bench/standalone/latest/bench-report-1y.json"}}
	}
	rows := []legacyMatrixRow{}
	for _, input := range profiles {
		report, err := readBenchReport(input.Path)
		if err != nil {
			return "", err
		}
		for _, row := range report.Rows {
			rows = append(rows, legacyMatrixRow{Category: firstNonEmpty(row.Category, "uncategorized"), Name: row.Name, Profile: input.Profile, PromP50MS: row.PromP50MS, NativeP50MS: row.NativeP50MS, NativePromRatio: row.NativePromRatio, FallbackNativeRatio: row.FallbackNativeRatio})
		}
	}
	groups := map[string][]legacyMatrixRow{}
	keys := []string{}
	for _, row := range rows {
		key := row.Category
		if opts.PerQuery {
			key += "\x00" + row.Name
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], row)
	}
	sort.Slice(keys, func(i, j int) bool {
		return legacySortLess(opts.SortBy, groups[keys[i]], groups[keys[j]], keys[i], keys[j])
	})
	var b strings.Builder
	fmt.Fprintln(&b)
	if opts.PerQuery {
		b.WriteString("## Cross-profile native-SQL vs Prometheus matrix (per-query)\n\n")
	} else {
		b.WriteString("## Cross-profile native-SQL vs Prometheus matrix (by category)\n\n")
	}
	profileNames := make([]string, len(profiles))
	for i, p := range profiles {
		profileNames[i] = p.Profile
	}
	fmt.Fprintf(&b, "Profiles: %s. N/P = native_p50 / prom_p50 (< 1 means\n", strings.Join(profileNames, " "))
	b.WriteString("native beat Prom). F/N = fallback_p50 / native_p50 (< 1 means the\nlocal-evaluator fallback is faster than lowering — a \"don't lower\nthis\" signal). Millisecond values are p50 medians across repeats;\n")
	if !opts.PerQuery {
		b.WriteString("when a (category, profile) bucket holds multiple queries, the cell\nshows the median of that bucket (so cells are comparable even when\nprofiles expose different queries in the same category).\n")
	}
	b.WriteString("\n| Category |")
	if opts.PerQuery {
		b.WriteString(" Query |")
	}
	for _, p := range profiles {
		fmt.Fprintf(&b, " Prom p50 (%s) | Native p50 (%s) | N/P (%s) | F/N (%s) |", p.Profile, p.Profile, p.Profile, p.Profile)
	}
	b.WriteString("\n|---|")
	if opts.PerQuery {
		b.WriteString("---|")
	}
	for range profiles {
		b.WriteString("---:|---:|---:|---:|")
	}
	b.WriteByte('\n')
	for _, key := range keys {
		vals := groups[key]
		parts := strings.Split(key, "\x00")
		fmt.Fprintf(&b, "| %s |", parts[0])
		if opts.PerQuery {
			fmt.Fprintf(&b, " %s |", parts[1])
		}
		for _, p := range profiles {
			bucket := filterLegacy(vals, p.Profile)
			fmt.Fprintf(&b, " %s | %s | %s | %s |", formatFloat(medianFloat(bucket, func(r legacyMatrixRow) float64 { return r.PromP50MS })), formatFloat(medianFloat(bucket, func(r legacyMatrixRow) float64 { return r.NativeP50MS })), formatRatioFloat(medianFloat(bucket, func(r legacyMatrixRow) float64 { return r.NativePromRatio })), formatRatioFloat(medianFloat(bucket, func(r legacyMatrixRow) float64 { return r.FallbackNativeRatio })))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return b.String(), nil
}

func readBenchReport(path string) (BenchReport, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return BenchReport{}, fmt.Errorf("read report %s: %w", path, err)
	}
	var r BenchReport
	if err := json.Unmarshal(content, &r); err != nil {
		return BenchReport{}, err
	}
	return r, nil
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func sweepPerQueryKey(r benchMatrixSweepRow) string {
	return strings.Join([]string{r.Category, r.Query, r.Profile, r.Density, r.Mode, r.RoutingPolicy}, "|")
}
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
func formatOptional(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f", *v)
}
func formatFloat(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
}
func formatRatioFloat(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f×", v)
}
func extract(rows []benchMatrixSweepRow, f func(benchMatrixSweepRow) *float64) []*float64 {
	out := []*float64{}
	for _, r := range rows {
		if v := f(r); v != nil {
			out = append(out, v)
		}
	}
	return out
}
func medianPtr(vals []*float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	fs := make([]float64, len(vals))
	for i, v := range vals {
		fs[i] = *v
	}
	v := median(fs)
	return &v
}
func medianFloat(rows []legacyMatrixRow, f func(legacyMatrixRow) float64) float64 {
	vals := []float64{}
	for _, r := range rows {
		if v := f(r); v != 0 {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return 0
	}
	return median(vals)
}
func median(vals []float64) float64 {
	sort.Float64s(vals)
	n := len(vals)
	if n%2 == 1 {
		return vals[n/2]
	}
	return (vals[n/2-1] + vals[n/2]) / 2
}
func strategyCounts(rows []benchMatrixSweepRow) string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Strategy]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}
func bandCounts(rows []benchMatrixSweepRow) string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.PromBand]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}
func countFlips(rows []benchMatrixSweepRow) int {
	n := 0
	for _, r := range rows {
		if r.CandidateFlip {
			n++
		}
	}
	return n
}
func filterLegacy(rows []legacyMatrixRow, profile string) []legacyMatrixRow {
	out := []legacyMatrixRow{}
	for _, r := range rows {
		if r.Profile == profile {
			out = append(out, r)
		}
	}
	return out
}
func legacySortLess(sortBy string, a, b []legacyMatrixRow, ka, kb string) bool {
	if sortBy == "cat" || sortBy == "category" {
		return ka < kb
	}
	ma, mb := 0.0, 0.0
	for _, r := range a {
		v := r.NativePromRatio
		if sortBy == "fn" {
			v = r.FallbackNativeRatio
		}
		if v > ma {
			ma = v
		}
	}
	for _, r := range b {
		v := r.NativePromRatio
		if sortBy == "fn" {
			v = r.FallbackNativeRatio
		}
		if v > mb {
			mb = v
		}
	}
	if ma == mb {
		return ka < kb
	}
	return ma > mb
}
