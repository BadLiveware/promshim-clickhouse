package promharness

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var chProfileSafePartPattern = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

type CHProfileComment struct {
	LogComment    string
	QueryName     string
	Query         string
	Endpoint      string
	Mode          string
	RoutingPolicy string
	Strategy      string
	ShimP50MS     float64
	PromP50MS     *float64
	ShimPromRatio *float64
	ReportModeKey string
	NativeMode    string
}

type CHMemorySummary struct {
	SchemaVersion      int                `json:"schemaVersion"`
	SourceReport       string             `json:"sourceReport"`
	ClickHouseURL      string             `json:"clickhouseURL"`
	PromshimURL        string             `json:"promshimURL"`
	ClickHouseQueryLog []map[string]any   `json:"clickHouseQueryLog"`
	PromshimMetrics    map[string]float64 `json:"promshimMetricsAfter"`
	Errors             []string           `json:"errors"`
	MissingLogComments []string           `json:"missingLogComments"`
}

type CHProfileSummary struct {
	SchemaVersion      int            `json:"schemaVersion"`
	Mode               string         `json:"mode"`
	SourceReport       string         `json:"sourceReport"`
	ClickHouseURL      string         `json:"clickhouseURL"`
	PromshimURL        string         `json:"promshimURL"`
	Rows               []CHProfileRow `json:"rows"`
	Errors             []string       `json:"errors"`
	MissingLogComments []string       `json:"missingLogComments,omitempty"`
}

type CHProfileRow struct {
	QueryName                        string           `json:"queryName,omitempty"`
	Query                            string           `json:"query,omitempty"`
	Endpoint                         string           `json:"endpoint,omitempty"`
	Mode                             string           `json:"mode,omitempty"`
	RoutingPolicy                    string           `json:"routingPolicy,omitempty"`
	Strategy                         string           `json:"strategy,omitempty"`
	ShimP50MS                        float64          `json:"shimP50Ms,omitempty"`
	PromP50MS                        *float64         `json:"promP50Ms,omitempty"`
	ShimPromRatio                    *float64         `json:"shimPromRatio,omitempty"`
	LogComment                       string           `json:"logComment,omitempty"`
	QueryCount                       int              `json:"queryCount,omitempty"`
	QueryDurationP50MS               float64          `json:"queryDurationP50Ms,omitempty"`
	QueryDurationP90MS               float64          `json:"queryDurationP90Ms,omitempty"`
	QueryDurationMaxMS               float64          `json:"queryDurationMaxMs,omitempty"`
	MemoryP50Bytes                   float64          `json:"memoryP50Bytes,omitempty"`
	MemoryP95Bytes                   float64          `json:"memoryP95Bytes,omitempty"`
	MemoryMaxBytes                   float64          `json:"memoryMaxBytes,omitempty"`
	ReadRowsTotal                    float64          `json:"readRowsTotal,omitempty"`
	ReadRowsP50                      float64          `json:"readRowsP50,omitempty"`
	ReadBytesTotal                   float64          `json:"readBytesTotal,omitempty"`
	ReadBytesP50                     float64          `json:"readBytesP50,omitempty"`
	ResultRowsTotal                  float64          `json:"resultRowsTotal,omitempty"`
	ResultRowsP50                    float64          `json:"resultRowsP50,omitempty"`
	SelectedRowsTotal                float64          `json:"selectedRowsTotal,omitempty"`
	SelectedRowsP50                  float64          `json:"selectedRowsP50,omitempty"`
	SelectedBytesTotal               float64          `json:"selectedBytesTotal,omitempty"`
	SelectedBytesP50                 float64          `json:"selectedBytesP50,omitempty"`
	ReadCompressedBytesTotal         float64          `json:"readCompressedBytesTotal,omitempty"`
	ReadCompressedBytesP50           float64          `json:"readCompressedBytesP50,omitempty"`
	JoinBuildTableRowCountTotal      float64          `json:"joinBuildTableRowCountTotal,omitempty"`
	JoinBuildTableRowCountP50        float64          `json:"joinBuildTableRowCountP50,omitempty"`
	JoinProbeTableRowCountTotal      float64          `json:"joinProbeTableRowCountTotal,omitempty"`
	JoinProbeTableRowCountP50        float64          `json:"joinProbeTableRowCountP50,omitempty"`
	JoinResultRowCountTotal          float64          `json:"joinResultRowCountTotal,omitempty"`
	JoinResultRowCountP50            float64          `json:"joinResultRowCountP50,omitempty"`
	FilterTransformPassedRowsTotal   float64          `json:"filterTransformPassedRowsTotal,omitempty"`
	FilterTransformPassedRowsP50     float64          `json:"filterTransformPassedRowsP50,omitempty"`
	FunctionExecuteTotal             float64          `json:"functionExecuteTotal,omitempty"`
	FunctionExecuteP50               float64          `json:"functionExecuteP50,omitempty"`
	RealTimeMicrosecondsTotal        float64          `json:"realTimeMicrosecondsTotal,omitempty"`
	RealTimeMicrosecondsP50          float64          `json:"realTimeMicrosecondsP50,omitempty"`
	UserTimeMicrosecondsTotal        float64          `json:"userTimeMicrosecondsTotal,omitempty"`
	UserTimeMicrosecondsP50          float64          `json:"userTimeMicrosecondsP50,omitempty"`
	SystemTimeMicrosecondsTotal      float64          `json:"systemTimeMicrosecondsTotal,omitempty"`
	SystemTimeMicrosecondsP50        float64          `json:"systemTimeMicrosecondsP50,omitempty"`
	DiskReadElapsedMicrosecondsTotal float64          `json:"diskReadElapsedMicrosecondsTotal,omitempty"`
	DiskReadElapsedMicrosecondsP50   float64          `json:"diskReadElapsedMicrosecondsP50,omitempty"`
	SampleQueryID                    string           `json:"sampleQueryId,omitempty"`
	SampleNativeSQL                  string           `json:"-"`
	NativeSQLPath                    string           `json:"nativeSQLPath,omitempty"`
	ProcessorsByNamePath             string           `json:"processorsByNamePath,omitempty"`
	TopProcessors                    []CHProcessorRow `json:"topProcessors,omitempty"`
	ProcessorProfileError            string           `json:"processorProfileError,omitempty"`
}

type CHProcessorRow struct {
	Name                string  `json:"name,omitempty"`
	PlanStepName        string  `json:"planStepName,omitempty"`
	Processors          int     `json:"processors,omitempty"`
	ElapsedMicroseconds float64 `json:"elapsedMicroseconds,omitempty"`
	ElapsedSeconds      float64 `json:"elapsedSeconds,omitempty"`
	InputRows           float64 `json:"inputRows,omitempty"`
	OutputRows          float64 `json:"outputRows,omitempty"`
	InputBytes          float64 `json:"inputBytes,omitempty"`
	OutputBytes         float64 `json:"outputBytes,omitempty"`
}

func BuildCHProfileComments(report BenchReportV2) []CHProfileComment {
	runLabel := strings.TrimSpace(report.RunLabels["run"])
	commentsByLog := map[string]CHProfileComment{}
	for _, row := range report.Rows {
		safeName := safeCHProfilePart(valueOr(row.Name, "unknown"))
		modes := make([]string, 0, len(row.Shim))
		for modeKey := range row.Shim {
			modes = append(modes, modeKey)
		}
		sort.Strings(modes)
		for _, modeKey := range modes {
			result := row.Shim[modeKey]
			nativeMode := result.NativeLoweringMode
			if nativeMode == "" {
				nativeMode = strings.SplitN(modeKey, "@", 2)[0]
			}
			comment := "promshim-bench"
			if runLabel != "" {
				comment += " run=" + safeCHProfilePart(runLabel)
			}
			comment += " query=" + safeName + " mode=" + safeCHProfilePart(nativeMode)
			if result.RoutingPolicy != "" {
				comment += " policy=" + safeCHProfilePart(result.RoutingPolicy)
			}
			var promP50 *float64
			var ratio *float64
			if row.Prom != nil {
				prom := row.Prom.P50MS
				promP50 = &prom
				if prom != 0 {
					value := result.P50MS / prom
					ratio = &value
				}
			}
			commentsByLog[comment] = CHProfileComment{
				LogComment:    comment,
				QueryName:     row.Name,
				Query:         row.Query,
				Endpoint:      row.Endpoint,
				Mode:          nativeMode,
				NativeMode:    nativeMode,
				RoutingPolicy: result.RoutingPolicy,
				Strategy:      result.Strategy,
				ShimP50MS:     result.P50MS,
				PromP50MS:     promP50,
				ShimPromRatio: ratio,
				ReportModeKey: modeKey,
			}
		}
	}
	comments := make([]CHProfileComment, 0, len(commentsByLog))
	for _, comment := range commentsByLog {
		comments = append(comments, comment)
	}
	sort.Slice(comments, func(i, j int) bool { return comments[i].LogComment < comments[j].LogComment })
	return comments
}

func CHProfileDirectoryName(queryName, mode, routingPolicy string) string {
	return safeCHProfilePart(valueOr(queryName, "unknown") + "__" + valueOr(mode, "unknown") + "__" + valueOr(routingPolicy, "strict"))
}

func CHProfileNeedsProcessors(profileMode string, row CHProfileRow) bool {
	if profileMode == "processors" {
		return true
	}
	if profileMode != "auto" {
		return false
	}
	ratio := 0.0
	if row.ShimPromRatio != nil {
		ratio = *row.ShimPromRatio
	}
	return row.QueryDurationP50MS >= 500 ||
		row.MemoryP95Bytes >= 1_000_000_000 ||
		row.JoinResultRowCountP50 >= 100_000_000 ||
		row.FilterTransformPassedRowsP50 >= 100_000_000 ||
		(ratio >= 2 && row.QueryDurationP50MS >= 100)
}

func RenderCHProfileMarkdown(sourceReport string, rows []CHProfileRow, errors []string) string {
	sortedRows := append([]CHProfileRow(nil), rows...)
	sort.SliceStable(sortedRows, func(i, j int) bool {
		if sortedRows[i].QueryDurationP50MS == sortedRows[j].QueryDurationP50MS {
			return sortedRows[i].MemoryP95Bytes > sortedRows[j].MemoryP95Bytes
		}
		return sortedRows[i].QueryDurationP50MS > sortedRows[j].QueryDurationP50MS
	})
	if len(sortedRows) > 10 {
		sortedRows = sortedRows[:10]
	}
	lines := []string{
		"# ClickHouse profile summary",
		"",
		fmt.Sprintf("Source report: `%s`", sourceReport),
		"",
		"| Query | Mode | CH p50 | Mem p95 | Read rows | Join rows | Filter rows | Native SQL |",
		"|---|---|---:|---:|---:|---:|---:|---|",
	}
	for _, row := range sortedRows {
		lines = append(lines, fmt.Sprintf("| `%s` | `%s` | %gms | %s | %s | %s | %s | `%s` |",
			row.QueryName,
			row.Mode,
			row.QueryDurationP50MS,
			humanCHBytes(row.MemoryP95Bytes),
			humanCHNumber(row.ReadRowsP50),
			humanCHNumber(row.JoinResultRowCountP50),
			humanCHNumber(row.FilterTransformPassedRowsP50),
			filepath.ToSlash(row.NativeSQLPath),
		))
	}
	lines = append(lines, "")
	if len(errors) > 0 {
		lines = append(lines, "## Errors")
		for _, err := range errors {
			lines = append(lines, "- "+err)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func safeCHProfilePart(value string) string {
	value = strings.TrimSpace(value)
	value = chProfileSafePartPattern.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "unknown"
	}
	return value
}

func humanCHNumber(value float64) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	for _, suffix := range []string{"", "K", "M", "B", "T"} {
		if abs < 1000 {
			if suffix == "" {
				return fmt.Sprintf("%.0f", value)
			}
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
		value /= 1000
		abs /= 1000
	}
	return fmt.Sprintf("%.1fP", value)
}

func humanCHBytes(value float64) string {
	abs := value
	if abs < 0 {
		abs = -abs
	}
	for _, suffix := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if abs < 1024 {
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
		value /= 1024
		abs /= 1024
	}
	return fmt.Sprintf("%.1fPiB", value)
}
