package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }
func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

type sweepManifest struct {
	RunName     string `json:"runName"`
	ArtifactDir string `json:"artifactDir"`
	Axes        struct {
		Profile                    string   `json:"profile"`
		Density                    string   `json:"density"`
		Transport                  string   `json:"transport"`
		ShimModes                  []string `json:"shimModes"`
		MemoryMode                 string   `json:"memoryMode"`
		ClickHouseReferenceProfile string   `json:"clickHouseReferenceProfile"`
		PromshimSettingsProfile    string   `json:"promshimSettingsProfile"`
		CorpusSet                  string   `json:"corpusSet"`
	} `json:"axes"`
	Endpoints  map[string]string `json:"endpoints"`
	Compliance struct {
		Status string `json:"status"`
	} `json:"compliance"`
	Bench struct {
		Reports []struct {
			Path      string `json:"path"`
			Profile   string `json:"profile"`
			Density   string `json:"density"`
			Transport string `json:"transport"`
		} `json:"reports"`
		MemoryReports []string `json:"memoryReports"`
	} `json:"bench"`
}

type benchReportV2 struct {
	SchemaVersion int               `json:"schemaVersion"`
	CorpusPath    string            `json:"corpusPath"`
	RunLabels     map[string]string `json:"runLabels"`
	Rows          []benchRowV2      `json:"rows"`
}

type benchRowV2 struct {
	Name     string                     `json:"name"`
	Query    string                     `json:"query"`
	Endpoint string                     `json:"endpoint"`
	Category string                     `json:"category"`
	Prom     *benchTiming               `json:"prom"`
	Shim     map[string]benchShimResult `json:"shim"`
}

type benchTiming struct {
	P50MS float64 `json:"p50Ms"`
	P95MS float64 `json:"p95Ms"`
}

type benchShimResult struct {
	P50MS              float64 `json:"p50Ms"`
	P95MS              float64 `json:"p95Ms"`
	NativeLoweringMode string  `json:"nativeLoweringMode"`
	RoutingPolicy      string  `json:"routingPolicy"`
	RoutingDecision    string  `json:"routingDecision"`
	RoutingReason      string  `json:"routingReason"`
	StrictStrategy     string  `json:"strictStrategy"`
	SelectedStrategy   string  `json:"selectedStrategy"`
	StrictCandidate    string  `json:"strictCandidate"`
	SelectedCandidate  string  `json:"selectedCandidate"`
	ServedCandidate    string  `json:"servedCandidate"`
	CostFamily         string  `json:"costFamily"`
	Strategy           string  `json:"strategy"`
	CHRoundtrips       int     `json:"chRoundtrips"`
	CHMillis           int     `json:"chMillis"`
	SettingsProfile    string  `json:"settingsProfile"`
	StrategyFlap       bool    `json:"strategyFlap"`
	Error              string  `json:"error"`
}

type memorySummary struct {
	SourceReport       string                `json:"sourceReport"`
	ClickHouseQueryLog []memoryQueryLogEntry `json:"clickHouseQueryLog"`
	MissingLogComments []string              `json:"missingLogComments"`
	Errors             []string              `json:"errors"`
}

type memoryQueryLogEntry struct {
	LogComment          string  `json:"logComment"`
	QueryDurationP50MS  float64 `json:"queryDurationP50Ms"`
	MemoryP95Bytes      float64 `json:"memoryP95Bytes"`
	ReadRows            float64 `json:"readRows"`
	ReadBytes           float64 `json:"readBytes"`
	SelectedRows        float64 `json:"selectedRows"`
	ReadCompressedBytes float64 `json:"readCompressedBytes"`
	FunctionExecute     float64 `json:"functionExecute"`
	MemoryTrackerUsage  float64 `json:"memoryTrackerUsage"`
}

type calibrationOutput struct {
	SchemaVersion int                 `json:"schemaVersion"`
	GeneratedAt   string              `json:"generatedAt"`
	Sources       []calibrationSource `json:"sources"`
	Classes       []calibrationClass  `json:"classes"`
	Warnings      []string            `json:"warnings,omitempty"`
}

type calibrationSource struct {
	SweepName                  string            `json:"sweepName,omitempty"`
	ManifestPath               string            `json:"manifestPath,omitempty"`
	BenchReports               []string          `json:"benchReports"`
	MemoryReports              []string          `json:"memoryReports,omitempty"`
	ComplianceStatus           string            `json:"complianceStatus,omitempty"`
	ClickHouseReferenceProfile string            `json:"clickHouseReferenceProfile,omitempty"`
	PromshimSettingsProfile    string            `json:"promshimSettingsProfile,omitempty"`
	Endpoints                  map[string]string `json:"endpoints,omitempty"`
}

type calibrationClass struct {
	Family                     string          `json:"family"`
	Profile                    string          `json:"profile,omitempty"`
	Density                    string          `json:"density,omitempty"`
	Transport                  string          `json:"transport,omitempty"`
	ClickHouseReferenceProfile string          `json:"clickHouseReferenceProfile,omitempty"`
	PromshimSettingsProfile    string          `json:"promshimSettingsProfile,omitempty"`
	CorpusPath                 string          `json:"corpusPath,omitempty"`
	Rows                       int             `json:"rows"`
	NativeP50MedianMS          float64         `json:"nativeP50MedianMs,omitempty"`
	LocalP50MedianMS           float64         `json:"localP50MedianMs,omitempty"`
	CostPreferP50MedianMS      float64         `json:"costPreferP50MedianMs,omitempty"`
	PromP50MedianMS            float64         `json:"promP50MedianMs,omitempty"`
	LocalNativeRatioMedian     float64         `json:"localNativeRatioMedian,omitempty"`
	StrictCandidate            string          `json:"strictCandidate,omitempty"`
	SelectedCandidate          string          `json:"selectedCandidate,omitempty"`
	ServedCandidate            string          `json:"servedCandidate,omitempty"`
	CandidateFlipRows          int             `json:"candidateFlipRows,omitempty"`
	Recommendation             string          `json:"recommendation"`
	Confidence                 string          `json:"confidence"`
	CoverageNotes              []string        `json:"coverageNotes,omitempty"`
	Reasons                    []string        `json:"reasons"`
	Memory                     *memoryCounters `json:"memory,omitempty"`
	StrategyFlips              int             `json:"strategyFlips,omitempty"`
	MissingMemoryComments      int             `json:"missingMemoryComments,omitempty"`
}

type memoryCounters struct {
	SelectedRowsMedian        float64 `json:"selectedRowsMedian,omitempty"`
	ReadCompressedBytesMedian float64 `json:"readCompressedBytesMedian,omitempty"`
	FunctionExecuteMedian     float64 `json:"functionExecuteMedian,omitempty"`
	MemoryP95BytesMedian      float64 `json:"memoryP95BytesMedian,omitempty"`
}

type sample struct {
	name, family, profile, density, transport, corpusPath string
	clickHouseReferenceProfile, promshimSettingsProfile   string
	strictCandidate, selectedCandidate, servedCandidate   string
	promP50, nativeP50, localP50, costPreferP50           float64
	strategyFlap, candidateFlip                           bool
	memory                                                *memoryQueryLogEntry
	missingMemory                                         bool
}

type groupKey struct{ family, profile, density, transport, clickHouseReferenceProfile, promshimSettingsProfile, corpusPath string }

func main() {
	var sweeps, legacyBench stringList
	outJSON := flag.String("out-json", "", "Path to write calibration JSON.")
	outMD := flag.String("out-md", "", "Path to write calibration Markdown.")
	flag.Var(&sweeps, "sweep", "Sweep manifest path. May be repeated.")
	flag.Var(&legacyBench, "bench", "Legacy/direct v2 bench report path for debugging. May be repeated.")
	flag.Parse()

	out, err := buildCalibration(sweeps, legacyBench)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promshim-routing-calibrate: %v\n", err)
		os.Exit(1)
	}
	if *outJSON == "" && *outMD == "" {
		payload, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(payload))
		return
	}
	if *outJSON != "" {
		if err := writeJSON(*outJSON, out); err != nil {
			fmt.Fprintf(os.Stderr, "write json: %v\n", err)
			os.Exit(1)
		}
	}
	if *outMD != "" {
		if err := writeText(*outMD, renderMarkdown(out)); err != nil {
			fmt.Fprintf(os.Stderr, "write markdown: %v\n", err)
			os.Exit(1)
		}
	}
}

func buildCalibration(sweeps, legacyBench []string) (calibrationOutput, error) {
	if len(sweeps) == 0 && len(legacyBench) == 0 {
		return calibrationOutput{}, errors.New("provide at least one --sweep manifest or --bench report")
	}
	var all []sample
	out := calibrationOutput{SchemaVersion: 2, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	var warnings []string
	for _, path := range sweeps {
		samples, source, sourceWarnings, err := readSweep(path)
		if err != nil {
			return calibrationOutput{}, err
		}
		all = append(all, samples...)
		out.Sources = append(out.Sources, source)
		warnings = append(warnings, sourceWarnings...)
	}
	for _, path := range legacyBench {
		samples, err := readBenchReport(path, calibrationSource{BenchReports: []string{path}})
		if err != nil {
			return calibrationOutput{}, err
		}
		all = append(all, samples...)
		out.Sources = append(out.Sources, calibrationSource{BenchReports: []string{path}})
	}
	out.Classes = summarizeSamples(all)
	out.Warnings = warnings
	return out, nil
}

func readSweep(path string) ([]sample, calibrationSource, []string, error) {
	var manifest sweepManifest
	if err := readJSON(path, &manifest); err != nil {
		return nil, calibrationSource{}, nil, fmt.Errorf("read sweep manifest %q: %w", path, err)
	}
	source := calibrationSource{SweepName: manifest.RunName, ManifestPath: path, ComplianceStatus: manifest.Compliance.Status, ClickHouseReferenceProfile: manifest.Axes.ClickHouseReferenceProfile, PromshimSettingsProfile: manifest.Axes.PromshimSettingsProfile, Endpoints: manifest.Endpoints}
	memoryByReport, missingByReport, warnings := readSweepMemory(path, manifest.Bench.MemoryReports)
	var samples []sample
	root := repoRootFor(path)
	for _, report := range manifest.Bench.Reports {
		reportPath := resolvePath(root, report.Path)
		source.BenchReports = append(source.BenchReports, report.Path)
		benchSamples, err := readBenchReport(reportPath, source)
		if err != nil {
			return nil, source, warnings, err
		}
		mem := memoryByReport[absPath(reportPath)]
		missing := missingByReport[absPath(reportPath)]
		for i := range benchSamples {
			if benchSamples[i].profile == "" {
				benchSamples[i].profile = firstNonEmpty(report.Profile, manifest.Axes.Profile)
			}
			if benchSamples[i].density == "" {
				benchSamples[i].density = firstNonEmpty(report.Density, manifest.Axes.Density)
			}
			if benchSamples[i].transport == "" {
				benchSamples[i].transport = firstNonEmpty(report.Transport, manifest.Axes.Transport)
			}
			if benchSamples[i].clickHouseReferenceProfile == "" {
				benchSamples[i].clickHouseReferenceProfile = manifest.Axes.ClickHouseReferenceProfile
			}
			if benchSamples[i].promshimSettingsProfile == "" {
				benchSamples[i].promshimSettingsProfile = manifest.Axes.PromshimSettingsProfile
			}
			comments := benchLogComments(benchSamples[i], "prefer")
			if entry, ok := findMemoryEntry(mem, comments); ok {
				benchSamples[i].memory = &entry
			} else if hasMissingMemoryComment(missing, comments) {
				benchSamples[i].missingMemory = true
			}
		}
		samples = append(samples, benchSamples...)
	}
	source.MemoryReports = manifest.Bench.MemoryReports
	return samples, source, warnings, nil
}

func readSweepMemory(manifestPath string, paths []string) (map[string]map[string]memoryQueryLogEntry, map[string]map[string]bool, []string) {
	root := repoRootFor(manifestPath)
	byReport := map[string]map[string]memoryQueryLogEntry{}
	missing := map[string]map[string]bool{}
	var warnings []string
	for _, path := range paths {
		var summary memorySummary
		resolved := resolvePath(root, path)
		if err := readJSON(resolved, &summary); err != nil {
			warnings = append(warnings, fmt.Sprintf("memory summary %s: %v", path, err))
			continue
		}
		report := absPath(resolvePath(root, summary.SourceReport))
		byReport[report] = map[string]memoryQueryLogEntry{}
		for _, row := range summary.ClickHouseQueryLog {
			byReport[report][row.LogComment] = row
		}
		missing[report] = map[string]bool{}
		for _, comment := range summary.MissingLogComments {
			missing[report][comment] = true
		}
		if len(summary.Errors) > 0 {
			warnings = append(warnings, fmt.Sprintf("memory summary %s errors: %s", path, strings.Join(summary.Errors, "; ")))
		}
	}
	return byReport, missing, warnings
}

func readBenchReport(path string, _ calibrationSource) ([]sample, error) {
	var report benchReportV2
	if err := readJSON(path, &report); err != nil {
		return nil, fmt.Errorf("read bench report %q: %w", path, err)
	}
	if report.SchemaVersion != 2 {
		return nil, fmt.Errorf("bench report %q has schemaVersion %d, want 2", path, report.SchemaVersion)
	}
	var samples []sample
	for _, row := range report.Rows {
		s := sample{name: row.Name, family: firstNonEmpty(row.Category, "uncategorized"), profile: report.RunLabels["profile"], density: report.RunLabels["density"], transport: report.RunLabels["transport"], corpusPath: report.CorpusPath}
		if row.Prom != nil {
			s.promP50 = row.Prom.P50MS
		}
		for mode, result := range row.Shim {
			if result.CostFamily != "" {
				s.family = result.CostFamily
			}
			if result.SettingsProfile != "" && s.promshimSettingsProfile == "" {
				s.promshimSettingsProfile = result.SettingsProfile
			}
			if modeAffectsRoutingCalibration(mode) && result.StrategyFlap {
				s.strategyFlap = true
			}
			if result.Error != "" {
				continue
			}
			switch mode {
			case "off":
				s.localP50 = result.P50MS
			case "force_supported", "prefer":
				if result.Strategy == "native_sql" || result.Strategy == "delegated_promql" || mode == "force_supported" {
					if s.nativeP50 == 0 || mode == "force_supported" {
						s.nativeP50 = result.P50MS
					}
				}
				if result.Strategy == "local" && s.localP50 == 0 {
					s.localP50 = result.P50MS
				}
			case "prefer@cost_prefer":
				s.costPreferP50 = result.P50MS
				s.strictCandidate = result.StrictCandidate
				s.selectedCandidate = result.SelectedCandidate
				s.servedCandidate = result.ServedCandidate
				s.candidateFlip = s.strictCandidate != "" && s.selectedCandidate != "" && s.strictCandidate != s.selectedCandidate
				if result.Strategy == "local" && s.localP50 == 0 {
					s.localP50 = result.P50MS
				}
				if result.Strategy == "native_sql" && s.nativeP50 == 0 {
					s.nativeP50 = result.P50MS
				}
			}
		}
		samples = append(samples, s)
	}
	return samples, nil
}

func summarizeSamples(samples []sample) []calibrationClass {
	groups := map[groupKey][]sample{}
	for _, s := range samples {
		key := groupKey{s.family, s.profile, s.density, s.transport, s.clickHouseReferenceProfile, s.promshimSettingsProfile, s.corpusPath}
		groups[key] = append(groups[key], s)
	}
	keys := make([]groupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	classes := make([]calibrationClass, 0, len(keys))
	for _, key := range keys {
		vals := groups[key]
		var native, local, prom, costPrefer, ratios []float64
		strictCandidates := map[string]int{}
		selectedCandidates := map[string]int{}
		servedCandidates := map[string]int{}
		var flips, missingMemory, candidateFlipRows int
		var coverageNotes []string
		var selectedRows, readCompressed, functionExec, memoryP95 []float64
		for _, s := range vals {
			if s.nativeP50 > 0 {
				native = append(native, s.nativeP50)
			}
			if s.localP50 > 0 {
				local = append(local, s.localP50)
			}
			if s.promP50 > 0 {
				prom = append(prom, s.promP50)
			}
			if s.costPreferP50 > 0 {
				costPrefer = append(costPrefer, s.costPreferP50)
			}
			if s.nativeP50 > 0 && s.localP50 > 0 {
				ratios = append(ratios, s.localP50/s.nativeP50)
			}
			if s.strategyFlap {
				flips++
			}
			if s.candidateFlip {
				candidateFlipRows++
			}
			if s.strictCandidate != "" {
				strictCandidates[s.strictCandidate]++
			}
			if s.selectedCandidate != "" {
				selectedCandidates[s.selectedCandidate]++
			}
			if s.servedCandidate != "" {
				servedCandidates[s.servedCandidate]++
			}
			if s.missingMemory {
				missingMemory++
			}
			if s.memory != nil {
				selectedRows = append(selectedRows, s.memory.SelectedRows)
				readCompressed = append(readCompressed, s.memory.ReadCompressedBytes)
				functionExec = append(functionExec, s.memory.FunctionExecute)
				memoryP95 = append(memoryP95, s.memory.MemoryP95Bytes)
			}
		}
		if len(costPrefer) == 0 {
			coverageNotes = append(coverageNotes, "no cost_prefer rows in class")
		}
		if len(strictCandidates) == 0 {
			coverageNotes = append(coverageNotes, "candidate headers missing in class rows")
		}
		class := calibrationClass{
			Family:                     key.family,
			Profile:                    key.profile,
			Density:                    key.density,
			Transport:                  key.transport,
			ClickHouseReferenceProfile: key.clickHouseReferenceProfile,
			PromshimSettingsProfile:    key.promshimSettingsProfile,
			CorpusPath:                 key.corpusPath,
			Rows:                       len(vals),
			NativeP50MedianMS:          median(native),
			LocalP50MedianMS:           median(local),
			CostPreferP50MedianMS:      median(costPrefer),
			PromP50MedianMS:            median(prom),
			LocalNativeRatioMedian:     median(ratios),
			StrictCandidate:            mostFrequent(strictCandidates),
			SelectedCandidate:          mostFrequent(selectedCandidates),
			ServedCandidate:            mostFrequent(servedCandidates),
			CandidateFlipRows:          candidateFlipRows,
			CoverageNotes:              coverageNotes,
			StrategyFlips:              flips,
			MissingMemoryComments:      missingMemory,
		}
		class.Recommendation, class.Reasons = recommend(class)
		class.Confidence = classConfidence(class)
		if len(selectedRows) > 0 || len(readCompressed) > 0 || len(functionExec) > 0 || len(memoryP95) > 0 {
			class.Memory = &memoryCounters{SelectedRowsMedian: median(selectedRows), ReadCompressedBytesMedian: median(readCompressed), FunctionExecuteMedian: median(functionExec), MemoryP95BytesMedian: median(memoryP95)}
		}
		classes = append(classes, class)
	}
	return classes
}

func recommend(class calibrationClass) (string, []string) {
	if class.StrategyFlips > 0 {
		return "do_not_route_due_to_strategy_flip", []string{"strategy changed across repeats"}
	}
	if class.NativeP50MedianMS == 0 || class.LocalP50MedianMS == 0 || class.LocalNativeRatioMedian == 0 {
		reason := "native/local pair missing from sweep"
		if class.CostPreferP50MedianMS == 0 {
			reason = "insufficient candidate data: native/local pair missing and no cost_prefer rows"
		}
		return "insufficient_data", []string{reason}
	}
	family := strings.ToLower(class.Family)
	if strings.Contains(family, "range") || strings.Contains(family, "subquery") || strings.Contains(family, "aggregation") || class.LocalNativeRatioMedian >= 1.0 {
		return "native_required", []string{fmt.Sprintf("native remains preferred for family; local/native median %.2f", class.LocalNativeRatioMedian)}
	}
	if class.LocalNativeRatioMedian <= 0.70 && (strings.Contains(family, "selector") || strings.Contains(family, "rate") || strings.Contains(family, "histogram")) {
		return "local_candidate", []string{fmt.Sprintf("local/native median %.2f <= 0.70 for bounded candidate family", class.LocalNativeRatioMedian)}
	}
	return "insufficient_data", []string{fmt.Sprintf("no initial rule for local/native median %.2f", class.LocalNativeRatioMedian)}
}

func classConfidence(class calibrationClass) string {
	if class.Rows >= 3 && class.LocalNativeRatioMedian > 0 && class.CostPreferP50MedianMS > 0 {
		return "high"
	}
	if class.Rows >= 2 && class.LocalNativeRatioMedian > 0 {
		return "medium"
	}
	return "low"
}

func renderMarkdown(out calibrationOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Cost routing calibration\n\nGenerated: `%s`\n\n", out.GeneratedAt)
	fmt.Fprintf(&b, "## Sources\n\n")
	for _, source := range out.Sources {
		fmt.Fprintf(&b, "- sweep `%s` manifest `%s` compliance `%s`\n", source.SweepName, source.ManifestPath, source.ComplianceStatus)
		for _, report := range source.BenchReports {
			fmt.Fprintf(&b, "  - bench `%s`\n", report)
		}
		if source.ClickHouseReferenceProfile != "" {
			fmt.Fprintf(&b, "  - ClickHouse reference profile `%s`\n", source.ClickHouseReferenceProfile)
		}
		if source.PromshimSettingsProfile != "" {
			fmt.Fprintf(&b, "  - promshim settings profile `%s`\n", source.PromshimSettingsProfile)
		}
		for _, memory := range source.MemoryReports {
			fmt.Fprintf(&b, "  - memory `%s`\n", memory)
		}
	}
	if len(out.Warnings) > 0 {
		fmt.Fprintf(&b, "\n## Warnings\n\n")
		for _, warning := range out.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	fmt.Fprintf(&b, "\n## Class recommendations\n\n")
	fmt.Fprintf(&b, "| Family | Profile | Density | Settings profile | CH ref profile | Rows | Native p50 ms | Local p50 ms | CostPrefer p50 ms | L/N | Strict cand. | Selected cand. | Served cand. | Cand. flips | Confidence | Recommendation | Reasons |\n")
	fmt.Fprintf(&b, "|---|---|---|---|---|---:|---:|---:|---:|---:|---|---|---|---:|---|---|---|\n")
	for _, class := range out.Classes {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %d | %s | %s | %s |\n", class.Family, class.Profile, class.Density, firstNonEmpty(class.PromshimSettingsProfile, "—"), firstNonEmpty(class.ClickHouseReferenceProfile, "—"), class.Rows, fmtFloat(class.NativeP50MedianMS), fmtFloat(class.LocalP50MedianMS), fmtFloat(class.CostPreferP50MedianMS), fmtFloat(class.LocalNativeRatioMedian), firstNonEmpty(class.StrictCandidate, "—"), firstNonEmpty(class.SelectedCandidate, "—"), firstNonEmpty(class.ServedCandidate, "—"), class.CandidateFlipRows, class.Confidence, class.Recommendation, strings.Join(class.Reasons, "; "))
		if len(class.CoverageNotes) > 0 {
			fmt.Fprintf(&b, "  - coverage: %s\n", strings.Join(class.CoverageNotes, "; "))
		}
	}
	return b.String()
}

func mostFrequent(items map[string]int) string {
	best, count := "", 0
	for item, n := range items {
		if n > count || (n == count && item < best) {
			best, count = item, n
		}
	}
	return best
}

func benchLogComments(s sample, mode string) []string {
	base := "promshim-bench query=" + sanitizeLogCommentPart(s.name) + " mode=" + sanitizeLogCommentPart(mode)
	return []string{
		base + " policy=strict",
		base + " policy=cost_shadow",
		base + " policy=cost_prefer",
		base,
	}
}

func findMemoryEntry(entries map[string]memoryQueryLogEntry, comments []string) (memoryQueryLogEntry, bool) {
	for _, comment := range comments {
		if entry, ok := entries[comment]; ok {
			return entry, true
		}
	}
	return memoryQueryLogEntry{}, false
}

func hasMissingMemoryComment(missing map[string]bool, comments []string) bool {
	for _, comment := range comments {
		if missing[comment] {
			return true
		}
	}
	return false
}

func sanitizeLogCommentPart(value string) string {
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func median(values []float64) float64 {
	clean := make([]float64, 0, len(values))
	for _, v := range values {
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return 0
	}
	sort.Float64s(clean)
	mid := len(clean) / 2
	if len(clean)%2 == 1 {
		return clean[mid]
	}
	return (clean[mid-1] + clean[mid]) / 2
}

func fmtFloat(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func modeAffectsRoutingCalibration(mode string) bool {
	switch mode {
	case "off", "prefer", "prefer@cost_prefer":
		return true
	default:
		return false
	}
}

func readJSON(path string, out any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func writeJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeText(path, string(payload)+"\n")
}

func writeText(path, value string) error {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(value), 0o644)
}

func repoRootFor(path string) string {
	abs := absPath(path)
	for dir := filepath.Dir(abs); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func resolvePath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
