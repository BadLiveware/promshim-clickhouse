package promharness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type CHProfileCaptureOptions struct {
	Mode          string
	ClickHouseURL string
	User          string
	Password      string
	PromshimURL   string
	ReportPath    string
	OutputPath    string
	MarkdownPath  string
	ProfilesDir   string
	Timeout       time.Duration
}

type CHMemoryCaptureOptions struct {
	ClickHouseURL string
	User          string
	Password      string
	PromshimURL   string
	ReportPath    string
	OutputPath    string
	Timeout       time.Duration
}

type MemoryDetailManifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	SourceReport  string   `json:"sourceReport"`
	PromshimURL   string   `json:"promshimURL"`
	Note          string   `json:"note"`
	Files         []string `json:"files"`
}

func WriteMemoryDetailManifest(dir, reportPath, shimURL string) error {
	files := []string{}
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				files = append(files, entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read memory detail dir: %w", err)
	}
	sort.Strings(files)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create memory detail dir: %w", err)
	}
	manifest := MemoryDetailManifest{
		SchemaVersion: 1,
		SourceReport:  reportPath,
		PromshimURL:   shimURL,
		Note:          "Whole-run pprof snapshots. Query-level detailed capture is intentionally deferred because it perturbs timings and needs serialized selected query groups.",
		Files:         files,
	}
	return writeSweepJSONFile(filepath.Join(dir, "manifest.json"), manifest)
}

func CaptureMemorySummary(opts CHMemoryCaptureOptions) error {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	report, err := loadBenchReportV2(opts.ReportPath)
	if err != nil {
		return err
	}
	comments := BuildCHProfileComments(report)
	summary := CHMemorySummary{
		SchemaVersion:      1,
		SourceReport:       opts.ReportPath,
		ClickHouseURL:      opts.ClickHouseURL,
		PromshimURL:        opts.PromshimURL,
		ClickHouseQueryLog: []map[string]any{},
		PromshimMetrics:    map[string]float64{},
		Errors:             []string{},
	}
	client := &http.Client{Timeout: opts.Timeout}
	metrics, err := fetchPromshimMetrics(client, opts.PromshimURL)
	if err != nil {
		summary.Errors = append(summary.Errors, "promshim metrics: "+err.Error())
	} else {
		summary.PromshimMetrics = metrics
	}
	if len(comments) > 0 {
		ch := chHTTPClient{url: opts.ClickHouseURL, user: opts.User, password: opts.Password, client: client}
		if _, err := ch.query("SYSTEM FLUSH LOGS"); err != nil {
			summary.Errors = append(summary.Errors, "flush logs: "+err.Error())
		}
		rows, err := ch.queryJSONEachRow(memorySummarySQL(comments))
		if err != nil {
			summary.Errors = append(summary.Errors, err.Error())
		} else {
			summary.ClickHouseQueryLog = rows
		}
		found := map[string]bool{}
		for _, row := range summary.ClickHouseQueryLog {
			found[toString(row["logComment"])] = true
		}
		for _, comment := range comments {
			if !found[comment.LogComment] {
				summary.MissingLogComments = append(summary.MissingLogComments, comment.LogComment)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return fmt.Errorf("create memory summary dir: %w", err)
	}
	return writeSweepJSONFile(opts.OutputPath, summary)
}

func CaptureClickHouseProfile(opts CHProfileCaptureOptions) error {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	report, err := loadBenchReportV2(opts.ReportPath)
	if err != nil {
		return err
	}
	comments := BuildCHProfileComments(report)
	commentMeta := map[string]CHProfileComment{}
	for _, comment := range comments {
		commentMeta[comment.LogComment] = comment
	}
	summary := CHProfileSummary{
		SchemaVersion: 1,
		Mode:          opts.Mode,
		SourceReport:  opts.ReportPath,
		ClickHouseURL: opts.ClickHouseURL,
		PromshimURL:   opts.PromshimURL,
		Rows:          []CHProfileRow{},
		Errors:        []string{},
	}
	if len(comments) > 0 {
		client := &http.Client{Timeout: opts.Timeout}
		ch := chHTTPClient{url: opts.ClickHouseURL, user: opts.User, password: opts.Password, client: client}
		if _, err := ch.query("SYSTEM FLUSH LOGS"); err != nil {
			summary.Errors = append(summary.Errors, "flush logs: "+err.Error())
		}
		queryRows, err := ch.queryJSONEachRow(chProfileSQL(comments))
		if err != nil {
			summary.Errors = append(summary.Errors, "query_log: "+err.Error())
		} else {
			summary.Rows = BuildCHProfileRows(queryRows, commentMeta)
			if err := writeCHProfileArtifacts(opts, ch, summary.Rows, queryRows); err != nil {
				return err
			}
		}
		found := map[string]bool{}
		for _, row := range queryRows {
			found[toString(row["logComment"])] = true
		}
		for _, comment := range comments {
			if !found[comment.LogComment] {
				summary.MissingLogComments = append(summary.MissingLogComments, comment.LogComment)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0o755); err != nil {
		return fmt.Errorf("create profile summary dir: %w", err)
	}
	if err := writeSweepJSONFile(opts.OutputPath, summary); err != nil {
		return err
	}
	if opts.MarkdownPath != "" {
		if err := os.WriteFile(opts.MarkdownPath, []byte(RenderCHProfileMarkdown(opts.ReportPath, summary.Rows, summary.Errors)), 0o644); err != nil {
			return fmt.Errorf("write profile markdown: %w", err)
		}
	}
	return nil
}

func BuildCHProfileRows(queryRows []map[string]any, commentMeta map[string]CHProfileComment) []CHProfileRow {
	rows := make([]CHProfileRow, 0, len(queryRows))
	for _, queryRow := range queryRows {
		comment := toString(queryRow["logComment"])
		meta := commentMeta[comment]
		row := CHProfileRow{
			QueryName:                        meta.QueryName,
			Query:                            meta.Query,
			Endpoint:                         meta.Endpoint,
			Mode:                             meta.Mode,
			RoutingPolicy:                    meta.RoutingPolicy,
			Strategy:                         meta.Strategy,
			ShimP50MS:                        meta.ShimP50MS,
			PromP50MS:                        meta.PromP50MS,
			ShimPromRatio:                    meta.ShimPromRatio,
			LogComment:                       comment,
			QueryCount:                       int(number(queryRow["queryCount"])),
			QueryDurationP50MS:               number(queryRow["queryDurationP50Ms"]),
			QueryDurationP90MS:               number(queryRow["queryDurationP90Ms"]),
			QueryDurationMaxMS:               number(queryRow["queryDurationMaxMs"]),
			MemoryP50Bytes:                   number(queryRow["memoryP50Bytes"]),
			MemoryP95Bytes:                   number(queryRow["memoryP95Bytes"]),
			MemoryMaxBytes:                   number(queryRow["memoryMaxBytes"]),
			ReadRowsTotal:                    number(queryRow["readRowsTotal"]),
			ReadRowsP50:                      number(queryRow["readRowsP50"]),
			ReadBytesTotal:                   number(queryRow["readBytesTotal"]),
			ReadBytesP50:                     number(queryRow["readBytesP50"]),
			ResultRowsTotal:                  number(queryRow["resultRowsTotal"]),
			ResultRowsP50:                    number(queryRow["resultRowsP50"]),
			SelectedRowsTotal:                number(queryRow["selectedRowsTotal"]),
			SelectedRowsP50:                  number(queryRow["selectedRowsP50"]),
			SelectedBytesTotal:               number(queryRow["selectedBytesTotal"]),
			SelectedBytesP50:                 number(queryRow["selectedBytesP50"]),
			ReadCompressedBytesTotal:         number(queryRow["readCompressedBytesTotal"]),
			ReadCompressedBytesP50:           number(queryRow["readCompressedBytesP50"]),
			JoinBuildTableRowCountTotal:      number(queryRow["joinBuildTableRowCountTotal"]),
			JoinBuildTableRowCountP50:        number(queryRow["joinBuildTableRowCountP50"]),
			JoinProbeTableRowCountTotal:      number(queryRow["joinProbeTableRowCountTotal"]),
			JoinProbeTableRowCountP50:        number(queryRow["joinProbeTableRowCountP50"]),
			JoinResultRowCountTotal:          number(queryRow["joinResultRowCountTotal"]),
			JoinResultRowCountP50:            number(queryRow["joinResultRowCountP50"]),
			FilterTransformPassedRowsTotal:   number(queryRow["filterTransformPassedRowsTotal"]),
			FilterTransformPassedRowsP50:     number(queryRow["filterTransformPassedRowsP50"]),
			FunctionExecuteTotal:             number(queryRow["functionExecuteTotal"]),
			FunctionExecuteP50:               number(queryRow["functionExecuteP50"]),
			RealTimeMicrosecondsTotal:        number(queryRow["realTimeMicrosecondsTotal"]),
			RealTimeMicrosecondsP50:          number(queryRow["realTimeMicrosecondsP50"]),
			UserTimeMicrosecondsTotal:        number(queryRow["userTimeMicrosecondsTotal"]),
			UserTimeMicrosecondsP50:          number(queryRow["userTimeMicrosecondsP50"]),
			SystemTimeMicrosecondsTotal:      number(queryRow["systemTimeMicrosecondsTotal"]),
			SystemTimeMicrosecondsP50:        number(queryRow["systemTimeMicrosecondsP50"]),
			DiskReadElapsedMicrosecondsTotal: number(queryRow["diskReadElapsedMicrosecondsTotal"]),
			DiskReadElapsedMicrosecondsP50:   number(queryRow["diskReadElapsedMicrosecondsP50"]),
			SampleQueryID:                    toString(queryRow["sampleQueryId"]),
			SampleNativeSQL:                  toString(queryRow["sampleNativeSQL"]),
		}
		rows = append(rows, row)
	}
	return rows
}

func writeCHProfileArtifacts(opts CHProfileCaptureOptions, ch chHTTPClient, rows []CHProfileRow, queryRows []map[string]any) error {
	if opts.ProfilesDir == "" {
		return nil
	}
	if err := os.MkdirAll(opts.ProfilesDir, 0o755); err != nil {
		return fmt.Errorf("create profiles dir: %w", err)
	}
	baseDir := filepath.Dir(opts.OutputPath)
	queryRowsByComment := map[string]map[string]any{}
	for _, queryRow := range queryRows {
		queryRowsByComment[toString(queryRow["logComment"])] = queryRow
	}
	for i := range rows {
		row := &rows[i]
		queryDir := filepath.Join(opts.ProfilesDir, CHProfileDirectoryName(row.QueryName, row.Mode, row.RoutingPolicy))
		if err := os.MkdirAll(queryDir, 0o755); err != nil {
			return fmt.Errorf("create query profile dir: %w", err)
		}
		nativePath := filepath.Join(queryDir, "native.sql")
		if err := os.WriteFile(nativePath, []byte(row.SampleNativeSQL+"\n"), 0o644); err != nil {
			return fmt.Errorf("write native sql: %w", err)
		}
		rel, err := filepath.Rel(baseDir, nativePath)
		if err != nil {
			return fmt.Errorf("relative native sql path: %w", err)
		}
		row.NativeSQLPath = filepath.ToSlash(rel)
		querySummary := map[string]any{}
		for key, value := range queryRowsByComment[row.LogComment] {
			if key != "sampleNativeSQL" {
				querySummary[key] = value
			}
		}
		if err := writeSweepJSONFile(filepath.Join(queryDir, "query-log-summary.json"), querySummary); err != nil {
			return err
		}
		if CHProfileNeedsProcessors(opts.Mode, *row) && row.SampleQueryID != "" {
			if err := captureProcessorRollups(baseDir, queryDir, ch, row); err != nil {
				row.ProcessorProfileError = err.Error()
			}
		}
	}
	return nil
}

type chHTTPClient struct {
	url      string
	user     string
	password string
	client   *http.Client
}

func (c chHTTPClient) query(sql string) (string, error) {
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.url, "/")+"/", bytes.NewBufferString(sql))
	if err != nil {
		return "", err
	}
	if c.user != "" || c.password != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.user+":"+c.password)))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("clickhouse status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func (c chHTTPClient) queryJSONEachRow(sql string) ([]map[string]any, error) {
	body, err := c.query(sql)
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("decode JSONEachRow: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func loadBenchReportV2(path string) (BenchReportV2, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return BenchReportV2{}, fmt.Errorf("read report: %w", err)
	}
	var report BenchReportV2
	if err := json.Unmarshal(content, &report); err != nil {
		return BenchReportV2{}, fmt.Errorf("decode report: %w", err)
	}
	if report.SchemaVersion != 2 {
		return BenchReportV2{}, fmt.Errorf("report must be schemaVersion 2")
	}
	return report, nil
}

func fetchPromshimMetrics(client *http.Client, shimURL string) (map[string]float64, error) {
	resp, err := client.Get(strings.TrimRight(shimURL, "/") + "/metrics")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{"go_memstats_heap_alloc_bytes": true, "go_memstats_heap_inuse_bytes": true, "go_memstats_heap_sys_bytes": true, "process_resident_memory_bytes": true}
	metrics := map[string]float64{}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") || strings.Contains(line, "{") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 && wanted[parts[0]] {
			value, err := strconv.ParseFloat(parts[1], 64)
			if err == nil {
				metrics[parts[0]] = value
			}
		}
	}
	return metrics, nil
}

func memorySummarySQL(comments []CHProfileComment) string {
	return fmt.Sprintf(`
SELECT
  log_comment AS logComment,
  count() AS queryCount,
  quantile(0.5)(query_duration_ms) AS queryDurationP50Ms,
  quantile(0.9)(query_duration_ms) AS queryDurationP90Ms,
  max(query_duration_ms) AS queryDurationMaxMs,
  quantile(0.5)(memory_usage) AS memoryP50Bytes,
  quantile(0.95)(memory_usage) AS memoryP95Bytes,
  max(memory_usage) AS memoryMaxBytes,
  sum(read_rows) AS readRows,
  sum(read_bytes) AS readBytes,
  sum(result_rows) AS resultRows,
  sum(ProfileEvents['SelectedRows']) AS selectedRows,
  sum(ProfileEvents['SelectedBytes']) AS selectedBytes,
  sum(ProfileEvents['ReadCompressedBytes']) AS readCompressedBytes,
  sum(ProfileEvents['FunctionExecute']) AS functionExecute,
  sum(ProfileEvents['MemoryTrackerUsage']) AS memoryTrackerUsage
FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= now() - INTERVAL 6 HOUR
  AND log_comment IN (%s)
GROUP BY log_comment
ORDER BY log_comment
FORMAT JSONEachRow
`, commentLiterals(comments))
}

func chProfileSQL(comments []CHProfileComment) string {
	return fmt.Sprintf(`
SELECT
  log_comment AS logComment,
  count() AS queryCount,
  quantile(0.5)(query_duration_ms) AS queryDurationP50Ms,
  quantile(0.9)(query_duration_ms) AS queryDurationP90Ms,
  max(query_duration_ms) AS queryDurationMaxMs,
  quantile(0.5)(memory_usage) AS memoryP50Bytes,
  quantile(0.95)(memory_usage) AS memoryP95Bytes,
  max(memory_usage) AS memoryMaxBytes,
  sum(read_rows) AS readRowsTotal,
  quantile(0.5)(read_rows) AS readRowsP50,
  sum(read_bytes) AS readBytesTotal,
  quantile(0.5)(read_bytes) AS readBytesP50,
  sum(result_rows) AS resultRowsTotal,
  quantile(0.5)(result_rows) AS resultRowsP50,
  sum(ProfileEvents['SelectedRows']) AS selectedRowsTotal,
  quantile(0.5)(ProfileEvents['SelectedRows']) AS selectedRowsP50,
  sum(ProfileEvents['SelectedBytes']) AS selectedBytesTotal,
  quantile(0.5)(ProfileEvents['SelectedBytes']) AS selectedBytesP50,
  sum(ProfileEvents['ReadCompressedBytes']) AS readCompressedBytesTotal,
  quantile(0.5)(ProfileEvents['ReadCompressedBytes']) AS readCompressedBytesP50,
  sum(ProfileEvents['JoinBuildTableRowCount']) AS joinBuildTableRowCountTotal,
  quantile(0.5)(ProfileEvents['JoinBuildTableRowCount']) AS joinBuildTableRowCountP50,
  sum(ProfileEvents['JoinProbeTableRowCount']) AS joinProbeTableRowCountTotal,
  quantile(0.5)(ProfileEvents['JoinProbeTableRowCount']) AS joinProbeTableRowCountP50,
  sum(ProfileEvents['JoinResultRowCount']) AS joinResultRowCountTotal,
  quantile(0.5)(ProfileEvents['JoinResultRowCount']) AS joinResultRowCountP50,
  sum(ProfileEvents['FilterTransformPassedRows']) AS filterTransformPassedRowsTotal,
  quantile(0.5)(ProfileEvents['FilterTransformPassedRows']) AS filterTransformPassedRowsP50,
  sum(ProfileEvents['FunctionExecute']) AS functionExecuteTotal,
  quantile(0.5)(ProfileEvents['FunctionExecute']) AS functionExecuteP50,
  sum(ProfileEvents['RealTimeMicroseconds']) AS realTimeMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['RealTimeMicroseconds']) AS realTimeMicrosecondsP50,
  sum(ProfileEvents['UserTimeMicroseconds']) AS userTimeMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['UserTimeMicroseconds']) AS userTimeMicrosecondsP50,
  sum(ProfileEvents['SystemTimeMicroseconds']) AS systemTimeMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['SystemTimeMicroseconds']) AS systemTimeMicrosecondsP50,
  sum(ProfileEvents['DiskReadElapsedMicroseconds']) AS diskReadElapsedMicrosecondsTotal,
  quantile(0.5)(ProfileEvents['DiskReadElapsedMicroseconds']) AS diskReadElapsedMicrosecondsP50,
  argMax(query_id, query_duration_ms) AS sampleQueryId,
  argMax(query, query_duration_ms) AS sampleNativeSQL
FROM system.query_log
WHERE type = 'QueryFinish'
  AND event_time >= now() - INTERVAL 6 HOUR
  AND log_comment IN (%s)
GROUP BY log_comment
ORDER BY log_comment
FORMAT JSONEachRow
`, commentLiterals(comments))
}

func commentLiterals(comments []CHProfileComment) string {
	literals := make([]string, 0, len(comments))
	for _, comment := range comments {
		literals = append(literals, "'"+strings.ReplaceAll(comment.LogComment, "'", "''")+"'")
	}
	return strings.Join(literals, ",")
}

func captureProcessorRollups(baseDir, queryDir string, ch chHTTPClient, row *CHProfileRow) error {
	queryID := strings.ReplaceAll(row.SampleQueryID, "'", "''")
	procRows, err := ch.queryJSONEachRow(fmt.Sprintf(processorsByNameSQL, queryID))
	if err != nil {
		return err
	}
	stepRows, err := ch.queryJSONEachRow(fmt.Sprintf(processorsByStepSQL, queryID))
	if err != nil {
		return err
	}
	if err := writeSweepJSONFile(filepath.Join(queryDir, "processors-by-name.json"), procRows); err != nil {
		return err
	}
	if err := writeSweepJSONFile(filepath.Join(queryDir, "processors-by-step.json"), stepRows); err != nil {
		return err
	}
	nameTSV := filepath.Join(queryDir, "processors-by-name.tsv")
	if err := writeTSV(nameTSV, procRows, []string{"name", "processors", "elapsedMicroseconds", "elapsedSeconds", "inputRows", "outputRows", "inputBytes", "outputBytes"}); err != nil {
		return err
	}
	if err := writeTSV(filepath.Join(queryDir, "processors-by-step.tsv"), stepRows, []string{"planStepName", "name", "processors", "elapsedMicroseconds", "elapsedSeconds", "inputRows", "outputRows"}); err != nil {
		return err
	}
	rel, err := filepath.Rel(baseDir, nameTSV)
	if err != nil {
		return err
	}
	row.ProcessorsByNamePath = filepath.ToSlash(rel)
	for _, procRow := range procRows {
		if len(row.TopProcessors) >= 5 {
			break
		}
		row.TopProcessors = append(row.TopProcessors, CHProcessorRow{Name: toString(procRow["name"]), Processors: int(number(procRow["processors"])), ElapsedMicroseconds: number(procRow["elapsedMicroseconds"]), ElapsedSeconds: number(procRow["elapsedSeconds"]), InputRows: number(procRow["inputRows"]), OutputRows: number(procRow["outputRows"]), InputBytes: number(procRow["inputBytes"]), OutputBytes: number(procRow["outputBytes"])})
	}
	return nil
}

const processorsByNameSQL = `
SELECT
  name,
  count() AS processors,
  sum(elapsed_us) AS elapsedMicroseconds,
  round(elapsedMicroseconds / 1000000, 6) AS elapsedSeconds,
  sum(input_rows) AS inputRows,
  sum(output_rows) AS outputRows,
  sum(input_bytes) AS inputBytes,
  sum(output_bytes) AS outputBytes
FROM system.processors_profile_log
WHERE query_id = '%s'
GROUP BY name
ORDER BY elapsedMicroseconds DESC
FORMAT JSONEachRow
`

const processorsByStepSQL = `
SELECT
  plan_step_name AS planStepName,
  name,
  count() AS processors,
  sum(elapsed_us) AS elapsedMicroseconds,
  round(elapsedMicroseconds / 1000000, 6) AS elapsedSeconds,
  sum(input_rows) AS inputRows,
  sum(output_rows) AS outputRows
FROM system.processors_profile_log
WHERE query_id = '%s'
GROUP BY plan_step_name, name
ORDER BY elapsedMicroseconds DESC
FORMAT JSONEachRow
`

func writeTSV(path string, rows []map[string]any, columns []string) error {
	var b strings.Builder
	b.WriteString(strings.Join(columns, "\t"))
	b.WriteByte('\n')
	for _, row := range rows {
		for i, col := range columns {
			if i > 0 {
				b.WriteByte('\t')
			}
			b.WriteString(toString(row[col]))
		}
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func number(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}
