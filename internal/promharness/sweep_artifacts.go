package promharness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SweepArtifactOptions struct {
	RepoRoot                   string
	ArtifactDir                string
	RunName                    string
	Profile                    string
	ActiveSeries               string
	Transport                  string
	SeedPolicy                 string
	ShimModes                  string
	RoutingPolicies            string
	WarmupRoutingPolicies      string
	CostRoutingLocalFamilies   string
	IncludeProm                string
	CorpusSet                  string
	ComplianceStatus           string
	BenchStatus                string
	PromURL                    string
	ShimURL                    string
	ClickHouseURL              string
	MemoryMode                 string
	ClickHouseProfileMode      string
	ClickHouseReferenceProfile string
	SettingsProfile            string
	Now                        time.Time
	CommandRunner              SweepCommandRunner
}

type SweepCommandRunner interface {
	Run(cwd string, args ...string) SweepCommandResult
}

type SweepCommandResult struct {
	OK         bool
	Stdout     string
	Stderr     string
	ReturnCode *int
}

type execSweepCommandRunner struct{}

func (execSweepCommandRunner) Run(cwd string, args ...string) SweepCommandResult {
	if len(args) == 0 {
		code := 1
		return SweepCommandResult{OK: false, Stderr: "missing command", ReturnCode: &code}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		result := SweepCommandResult{OK: false, Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String())}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			result.ReturnCode = &code
		} else {
			result.Stderr = err.Error()
		}
		return result
	}
	code := 0
	return SweepCommandResult{OK: true, Stdout: strings.TrimSpace(stdout.String()), Stderr: strings.TrimSpace(stderr.String()), ReturnCode: &code}
}

type SweepManifest struct {
	SchemaVersion  int                    `json:"schemaVersion"`
	RunName        string                 `json:"runName"`
	GeneratedAt    string                 `json:"generatedAt"`
	ArtifactDir    string                 `json:"artifactDir"`
	Axes           SweepAxes              `json:"axes"`
	Endpoints      SweepEndpoints         `json:"endpoints"`
	BenchmarkStack map[string]interface{} `json:"benchmarkStack"`
	Compliance     SweepCompliance        `json:"compliance"`
	Bench          SweepBench             `json:"bench"`
	Summaries      SweepSummaries         `json:"summaries"`
}

type SweepAxes struct {
	Profile                    string   `json:"profile"`
	ActiveSeries               string   `json:"activeSeries"`
	LegacyDensity              string   `json:"density,omitempty"`
	Transport                  string   `json:"transport"`
	SeedPolicy                 string   `json:"seedPolicy"`
	ShimModes                  []string `json:"shimModes"`
	RoutingPolicies            []string `json:"routingPolicies"`
	WarmupRoutingPolicies      []string `json:"warmupRoutingPolicies"`
	CostRoutingLocalFamilies   []string `json:"costRoutingLocalFamilies"`
	IncludeProm                string   `json:"includeProm"`
	MemoryMode                 string   `json:"memoryMode"`
	ClickHouseProfileMode      string   `json:"clickHouseProfileMode"`
	ClickHouseReferenceProfile string   `json:"clickHouseReferenceProfile"`
	PromshimSettingsProfile    string   `json:"promshimSettingsProfile"`
	CorpusSet                  string   `json:"corpusSet"`
}

type SweepEndpoints struct {
	Prometheus string `json:"prometheus"`
	Promshim   string `json:"promshim"`
	ClickHouse string `json:"clickhouse"`
}

type SweepCompliance struct {
	Status string  `json:"status"`
	Log    *string `json:"log"`
}

type SweepBench struct {
	Status                string             `json:"status"`
	Reports               []SweepBenchReport `json:"reports"`
	MemoryReports         []string           `json:"memoryReports"`
	MemoryDetailManifests []string           `json:"memoryDetailManifests"`
	ClickHouseProfiles    []string           `json:"clickHouseProfiles"`
}

type SweepBenchReport struct {
	Path            string   `json:"path"`
	CorpusPath      string   `json:"corpusPath,omitempty"`
	Profile         string   `json:"profile,omitempty"`
	ActiveSeries    string   `json:"activeSeries,omitempty"`
	LegacyDensity   string   `json:"density,omitempty"`
	Transport       string   `json:"transport,omitempty"`
	RoutingPolicies []string `json:"routingPolicies"`
	RowCount        int      `json:"rowCount"`
}

type SweepSummaries struct {
	Markdown string `json:"markdown"`
	JSON     string `json:"json"`
}

type SweepSummary struct {
	SchemaVersion              int            `json:"schemaVersion"`
	RunName                    string         `json:"runName"`
	ComplianceStatus           string         `json:"complianceStatus"`
	BenchStatus                string         `json:"benchStatus"`
	ReportCount                int            `json:"reportCount"`
	MemoryReportCount          int            `json:"memoryReportCount"`
	MemoryDetailCount          int            `json:"memoryDetailCount"`
	ClickHouseProfileCount     int            `json:"clickHouseProfileCount"`
	StrategyHistogram          map[string]int `json:"strategyHistogram"`
	RoutingPolicyHistogram     map[string]int `json:"routingPolicyHistogram"`
	TargetBands                map[string]int `json:"targetBands"`
	ClickHouseReferenceProfile string         `json:"clickHouseReferenceProfile"`
	PromshimSettingsProfile    string         `json:"promshimSettingsProfile"`
	TopSlowRows                []SweepSlowRow `json:"topSlowRows"`
}

type SweepSlowRow struct {
	Query         string   `json:"query,omitempty"`
	Category      string   `json:"category,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	RoutingPolicy string   `json:"routingPolicy,omitempty"`
	Strategy      string   `json:"strategy,omitempty"`
	PromP50MS     *float64 `json:"promP50Ms"`
	ShimP50MS     float64  `json:"shimP50Ms"`
	ShimPromRatio *float64 `json:"shimPromRatio"`
	Report        string   `json:"report,omitempty"`
}

func BuildSweepArtifacts(opts SweepArtifactOptions) error {
	if opts.RepoRoot == "" {
		return fmt.Errorf("repo root is required")
	}
	if opts.ArtifactDir == "" {
		return fmt.Errorf("artifact dir is required")
	}
	if opts.RunName == "" {
		return fmt.Errorf("run name is required")
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = execSweepCommandRunner{}
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	root, err := filepath.Abs(opts.RepoRoot)
	if err != nil {
		return fmt.Errorf("resolve repo root: %w", err)
	}
	outDir := filepath.Join(root, filepath.FromSlash(opts.ArtifactDir))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}

	collector := newSweepCollector(root, outDir)
	if err := collector.collect(); err != nil {
		return err
	}

	routingPolicies := splitNonEmpty(opts.RoutingPolicies)
	if len(routingPolicies) == 0 {
		for key := range collector.routingPolicyHist {
			if key != "unknown" {
				routingPolicies = append(routingPolicies, key)
			}
		}
		sort.Strings(routingPolicies)
	}
	complianceLog := (*string)(nil)
	if opts.ComplianceStatus != "skipped" {
		logPath := opts.ArtifactDir + "/compliance.log"
		complianceLog = &logPath
	}
	manifest := SweepManifest{
		SchemaVersion: 1,
		RunName:       opts.RunName,
		GeneratedAt:   opts.Now.UTC().Format(time.RFC3339Nano),
		ArtifactDir:   opts.ArtifactDir,
		Axes: SweepAxes{
			Profile:                    opts.Profile,
			ActiveSeries:               opts.ActiveSeries,
			Transport:                  opts.Transport,
			SeedPolicy:                 opts.SeedPolicy,
			ShimModes:                  splitNonEmpty(opts.ShimModes),
			RoutingPolicies:            routingPolicies,
			WarmupRoutingPolicies:      splitNonEmpty(opts.WarmupRoutingPolicies),
			CostRoutingLocalFamilies:   splitNonEmpty(opts.CostRoutingLocalFamilies),
			IncludeProm:                opts.IncludeProm,
			MemoryMode:                 opts.MemoryMode,
			ClickHouseProfileMode:      opts.ClickHouseProfileMode,
			ClickHouseReferenceProfile: opts.ClickHouseReferenceProfile,
			PromshimSettingsProfile:    opts.SettingsProfile,
			CorpusSet:                  opts.CorpusSet,
		},
		Endpoints:      SweepEndpoints{Prometheus: opts.PromURL, Promshim: opts.ShimURL, ClickHouse: opts.ClickHouseURL},
		BenchmarkStack: benchmarkStackProvenance(root, opts, opts.CommandRunner),
		Compliance:     SweepCompliance{Status: opts.ComplianceStatus, Log: complianceLog},
		Bench: SweepBench{
			Status:                opts.BenchStatus,
			Reports:               collector.reports,
			MemoryReports:         collector.memoryReports,
			MemoryDetailManifests: collector.memoryDetails,
			ClickHouseProfiles:    collector.clickhouseProfiles,
		},
		Summaries: SweepSummaries{Markdown: opts.ArtifactDir + "/summary.md", JSON: opts.ArtifactDir + "/summary.json"},
	}
	summary := SweepSummary{
		SchemaVersion:              1,
		RunName:                    opts.RunName,
		ComplianceStatus:           opts.ComplianceStatus,
		BenchStatus:                opts.BenchStatus,
		ReportCount:                len(collector.reports),
		MemoryReportCount:          len(collector.memoryReports),
		MemoryDetailCount:          len(collector.memoryDetails),
		ClickHouseProfileCount:     len(collector.clickhouseProfiles),
		StrategyHistogram:          collector.strategyHist,
		RoutingPolicyHistogram:     collector.routingPolicyHist,
		TargetBands:                collector.targetBands,
		ClickHouseReferenceProfile: opts.ClickHouseReferenceProfile,
		PromshimSettingsProfile:    opts.SettingsProfile,
		TopSlowRows:                firstSlowRows(collector.slowRows, 20),
	}
	if err := writeSweepJSONFile(filepath.Join(outDir, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := writeSweepJSONFile(filepath.Join(outDir, "summary.json"), summary); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(renderSweepSummaryMarkdown(opts, collector)), 0o644); err != nil {
		return fmt.Errorf("write summary.md: %w", err)
	}
	return nil
}

type sweepCollector struct {
	root               string
	outDir             string
	reports            []SweepBenchReport
	memoryReports      []string
	memoryDetails      []string
	clickhouseProfiles []string
	strategyHist       map[string]int
	routingPolicyHist  map[string]int
	targetBands        map[string]int
	slowRows           []SweepSlowRow
}

func newSweepCollector(root, outDir string) *sweepCollector {
	return &sweepCollector{root: root, outDir: outDir, strategyHist: map[string]int{}, routingPolicyHist: map[string]int{}, targetBands: map[string]int{}}
}

func (c *sweepCollector) collect() error {
	var err error
	if c.memoryReports, err = globRelative(c.root, c.outDir, "memory-summary*.json"); err != nil {
		return err
	}
	if c.memoryDetails, err = globRelative(c.root, c.outDir, "memory-detail*/manifest.json"); err != nil {
		return err
	}
	if c.clickhouseProfiles, err = globRelative(c.root, c.outDir, "clickhouse-profile-*.json"); err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(c.outDir, "bench-report*.json"))
	if err != nil {
		return fmt.Errorf("glob benchmark reports: %w", err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := c.collectReport(path); err != nil {
			return err
		}
	}
	sort.SliceStable(c.slowRows, func(i, j int) bool { return c.slowRows[i].ShimP50MS > c.slowRows[j].ShimP50MS })
	return nil
}

func (c *sweepCollector) collectReport(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read benchmark report %s: %w", path, err)
	}
	var report BenchReportV2
	if err := json.Unmarshal(content, &report); err != nil || report.SchemaVersion != 2 {
		return nil
	}
	rel, err := relSlash(c.root, path)
	if err != nil {
		return err
	}
	reportPolicies := map[string]bool{}
	for _, row := range report.Rows {
		for _, result := range row.Shim {
			if result.RoutingPolicy != "" {
				reportPolicies[result.RoutingPolicy] = true
			}
		}
	}
	c.reports = append(c.reports, SweepBenchReport{
		Path:            rel,
		CorpusPath:      report.CorpusPath,
		Profile:         report.RunLabels["profile"],
		ActiveSeries:    firstNonEmpty(report.RunLabels["active-series"], report.RunLabels["density"]),
		Transport:       report.RunLabels["transport"],
		RoutingPolicies: sortedKeys(reportPolicies),
		RowCount:        len(report.Rows),
	})
	for key, value := range report.Summary.StrategyHistogram {
		c.strategyHist[key] += value
	}
	for _, row := range report.Rows {
		band := row.PromBand
		if band == "" {
			band = "n/a"
		}
		c.targetBands[band]++
		var promP50 *float64
		if row.Prom != nil {
			prom := row.Prom.P50MS
			promP50 = &prom
		}
		modes := make([]string, 0, len(row.Shim))
		for mode := range row.Shim {
			modes = append(modes, mode)
		}
		sort.Strings(modes)
		for _, mode := range modes {
			result := row.Shim[mode]
			routingPolicy := result.RoutingPolicy
			if routingPolicy == "" {
				routingPolicy = "unknown"
			}
			c.routingPolicyHist[routingPolicy]++
			var ratio *float64
			if promP50 != nil && *promP50 != 0 {
				value := result.P50MS / *promP50
				ratio = &value
			}
			c.slowRows = append(c.slowRows, SweepSlowRow{
				Query:         row.Name,
				Category:      row.Category,
				Mode:          mode,
				RoutingPolicy: routingPolicy,
				Strategy:      result.Strategy,
				PromP50MS:     promP50,
				ShimP50MS:     result.P50MS,
				ShimPromRatio: ratio,
				Report:        rel,
			})
		}
	}
	return nil
}

func benchmarkStackProvenance(root string, opts SweepArtifactOptions, runner SweepCommandRunner) map[string]interface{} {
	gitStatus := runStdout(runner, root, "git", "status", "--porcelain")
	statusLines := splitLines(gitStatus)
	if len(statusLines) > 50 {
		statusLines = statusLines[:50]
	}
	provenance := map[string]interface{}{
		"composeBuildRequested": opts.BenchStatus != "skipped",
		"git": map[string]interface{}{
			"revision":        runStdout(runner, root, "git", "rev-parse", "HEAD"),
			"dirty":           strings.TrimSpace(gitStatus) != "",
			"statusPorcelain": statusLines,
		},
		"promshim": map[string]interface{}{},
	}
	if opts.BenchStatus == "skipped" {
		return provenance
	}
	composeArgs := []string{"docker", "compose", "-f", "docker-compose.yml"}
	if opts.ClickHouseReferenceProfile == "promshim-ch-timeseries-reference-v1" {
		composeArgs = append(composeArgs, "-f", "docker-compose.reference.yml")
	}
	composeFiles := []string{}
	for i := 3; i < len(composeArgs); i += 2 {
		composeFiles = append(composeFiles, composeArgs[i])
	}
	provenance["composeFiles"] = composeFiles
	benchDir := filepath.Join(root, "harness", "bench")
	containerResult := runner.Run(benchDir, append(composeArgs, "ps", "-q", "promshim")...)
	containerID := strings.TrimSpace(containerResult.Stdout)
	promshim := provenance["promshim"].(map[string]interface{})
	if !containerResult.OK || containerID == "" {
		promshim["available"] = false
		if !containerResult.OK {
			promshim["reason"] = "compose_ps_failed"
		} else {
			promshim["reason"] = "container_not_found"
		}
		if containerResult.ReturnCode != nil {
			promshim["returncode"] = *containerResult.ReturnCode
		} else {
			promshim["returncode"] = nil
		}
		promshim["stderr"] = containerResult.Stderr
		return provenance
	}
	promshim["containerId"] = containerID
	inspectResult := runner.Run(root, "docker", "inspect", containerID)
	if !inspectResult.OK || strings.TrimSpace(inspectResult.Stdout) == "" {
		promshim["available"] = false
		promshim["reason"] = "docker_inspect_failed"
		if inspectResult.ReturnCode != nil {
			promshim["returncode"] = *inspectResult.ReturnCode
		} else {
			promshim["returncode"] = nil
		}
		promshim["stderr"] = inspectResult.Stderr
		return provenance
	}
	var infos []struct {
		Name    string `json:"Name"`
		Created string `json:"Created"`
		Image   string `json:"Image"`
		Config  struct {
			Image string `json:"Image"`
		} `json:"Config"`
	}
	if err := json.Unmarshal([]byte(inspectResult.Stdout), &infos); err != nil || len(infos) == 0 {
		promshim["available"] = false
		promshim["reason"] = "docker_inspect_invalid_json"
		if err != nil {
			promshim["error"] = err.Error()
		}
		return provenance
	}
	promshim["available"] = true
	promshim["containerName"] = strings.TrimPrefix(infos[0].Name, "/")
	promshim["containerCreatedAt"] = infos[0].Created
	promshim["image"] = infos[0].Config.Image
	promshim["imageId"] = infos[0].Image
	return provenance
}

func renderSweepSummaryMarkdown(opts SweepArtifactOptions, c *sweepCollector) string {
	routing := opts.RoutingPolicies
	if routing == "" {
		routing = strings.Join(sortedNonUnknownKeys(c.routingPolicyHist), ",")
		if routing == "" {
			routing = "n/a"
		}
	}
	lines := []string{
		fmt.Sprintf("# Sweep summary: %s", opts.RunName),
		"",
		fmt.Sprintf("- Artifacts: `%s`", opts.ArtifactDir),
		fmt.Sprintf("- Compliance: `%s`", opts.ComplianceStatus),
		fmt.Sprintf("- Benchmark: `%s`", opts.BenchStatus),
		fmt.Sprintf("- Reports: `%d`", len(c.reports)),
		fmt.Sprintf("- Transport: `%s`", opts.Transport),
		fmt.Sprintf("- Profiles: `%s`", opts.Profile),
		fmt.Sprintf("- Active series: `%s`", opts.ActiveSeries),
		fmt.Sprintf("- Modes: `%s`", opts.ShimModes),
		fmt.Sprintf("- Routing policies: `%s`", routing),
		fmt.Sprintf("- Warmup routing policies: `%s`", valueOr(opts.WarmupRoutingPolicies, "none")),
		fmt.Sprintf("- Cost routing local families: `%s`", valueOr(opts.CostRoutingLocalFamilies, "none")),
		fmt.Sprintf("- Memory mode: `%s`", opts.MemoryMode),
		fmt.Sprintf("- ClickHouse profile mode: `%s`", opts.ClickHouseProfileMode),
		fmt.Sprintf("- ClickHouse reference profile: `%s`", opts.ClickHouseReferenceProfile),
		fmt.Sprintf("- promshim settings profile: `%s`", opts.SettingsProfile),
		fmt.Sprintf("- Memory summaries: `%d`", len(c.memoryReports)),
		fmt.Sprintf("- Memory detail manifests: `%d`", len(c.memoryDetails)),
		fmt.Sprintf("- ClickHouse profile summaries: `%d`", len(c.clickhouseProfiles)),
		"",
		"## Strategy histogram",
		"",
	}
	if len(c.strategyHist) == 0 {
		lines = append(lines, "- No benchmark strategy data captured.")
	} else {
		for _, key := range sortedIntKeys(c.strategyHist) {
			lines = append(lines, fmt.Sprintf("- `%s`: %d", key, c.strategyHist[key]))
		}
	}
	lines = append(lines, "", "## Routing policy histogram", "")
	if len(c.routingPolicyHist) == 0 {
		lines = append(lines, "- No routing policy data captured.")
	} else {
		for _, key := range sortedIntKeys(c.routingPolicyHist) {
			lines = append(lines, fmt.Sprintf("- `%s`: %d", key, c.routingPolicyHist[key]))
		}
	}
	lines = append(lines, "", "## Prometheus target bands", "")
	if len(c.targetBands) == 0 {
		lines = append(lines, "- No target-band data captured.")
	} else {
		for _, key := range sortedIntKeys(c.targetBands) {
			lines = append(lines, fmt.Sprintf("- `%s`: %d", key, c.targetBands[key]))
		}
	}
	lines = append(lines, "", "## Top slow rows", "", "| Query | Mode | Routing policy | Strategy | Prom p50 ms | Shim p50 ms | S/P | Report |", "|---|---|---|---|---:|---:|---:|---|")
	for _, row := range firstSlowRows(c.slowRows, 10) {
		lines = append(lines, fmt.Sprintf("| %s | %s | %s | %s | %s | %.2f | %s | `%s` |",
			row.Query, row.Mode, row.RoutingPolicy, row.Strategy, formatPtrFloat(row.PromP50MS), row.ShimP50MS, formatRatio(row.ShimPromRatio), row.Report))
	}
	return strings.Join(lines, "\n") + "\n"
}

func writeSweepJSONFile(path string, value interface{}) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func globRelative(root, base, pattern string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(base, pattern))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", pattern, err)
	}
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		rel, err := relSlash(root, path)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, nil
}

func relSlash(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("relative path for %s: %w", path, err)
	}
	return filepath.ToSlash(rel), nil
}

func runStdout(runner SweepCommandRunner, cwd string, args ...string) string {
	result := runner.Run(cwd, args...)
	if !result.OK {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

func splitNonEmpty(value string) []string {
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

func splitLines(value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{}
	}
	return strings.Split(value, "\n")
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedIntKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNonUnknownKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "unknown" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func firstSlowRows(rows []SweepSlowRow, n int) []SweepSlowRow {
	if len(rows) <= n {
		return rows
	}
	return rows[:n]
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatPtrFloat(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f", *value)
}

func formatRatio(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f×", *value)
}
