package promshim

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type Handler struct {
	opts   Options
	client *ClickHouseClient
	mux    *http.ServeMux
}

type apiError struct {
	StatusCode int    `json:"-"`
	ErrorType  string `json:"errorType"`
	Error      string `json:"error"`
}

type instantRow struct {
	Tags      [][]string      `json:"tags"`
	Timestamp string          `json:"timestamp"`
	Value     json.RawMessage `json:"value"`
	Scalar    json.RawMessage `json:"scalar"`
	String    json.RawMessage `json:"string"`
}

type matrixRow struct {
	Tags       [][]string          `json:"tags"`
	TimeSeries [][]json.RawMessage `json:"time_series"`
}

type labelRow struct {
	Label string `json:"label"`
}

type valueRow struct {
	Value string `json:"value"`
}

type tagsRow struct {
	Tags [][]string `json:"tags"`
}

func NewHandler(opts Options) (http.Handler, error) {
	client, err := NewClickHouseClient(opts)
	if err != nil {
		return nil, err
	}

	h := &Handler{
		opts:   opts,
		client: client,
		mux:    http.NewServeMux(),
	}

	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /-/healthy", h.handleHealthy)
	h.mux.HandleFunc("GET /-/ready", h.handleReady)
	h.mux.HandleFunc("GET /api/v1/query", h.handleQuery)
	h.mux.HandleFunc("GET /api/v1/query_range", h.handleQueryRange)
	h.mux.HandleFunc("GET /api/v1/labels", h.handleLabels)
	h.mux.HandleFunc("GET /api/v1/label/{name}/values", h.handleLabelValues)
	h.mux.HandleFunc("GET /api/v1/series", h.handleSeries)

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleHealthy(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ready")
}

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: "missing required parameter 'query'"})
		return
	}

	expr, err := ParseExpression(query)
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}

	analysis := AnalyzeExpression(expr)
	if !analysis.Supported {
		writePromError(w, apiError{StatusCode: http.StatusUnprocessableEntity, ErrorType: "unsupported", Error: fmt.Sprintf("unsupported PromQL (difficulty=%s): %s", analysis.Difficulty, analysis.Reason)})
		return
	}

	evaluationTime := time.Now().UTC()
	if raw := r.URL.Query().Get("time"); raw != "" {
		evaluationTime, err = parsePrometheusTimestamp(raw)
		if err != nil {
			writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
			return
		}
	}

	if agg, ok := supportedSumAggregation(expr); ok {
		rows, apiErr := h.executeInstantSumAggregation(r.Context(), agg, evaluationTime)
		if apiErr != nil {
			writePromError(w, *apiErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result":     rows,
			},
		})
		return
	}

	sql, params := buildInstantQuerySQL(h.opts, expr.String(), evaluationTime.UnixMilli())
	response, err := h.client.Execute(r.Context(), sql, params)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	defer response.Body.Close()

	resultType := string(expr.Type())
	if resultType == "matrix" {
		rows, apiErr := decodeMatrixRows(response.Body)
		if apiErr != nil {
			writePromError(w, *apiErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result":     rows,
			},
		})
		return
	}

	rows, apiErr := decodeInstantRows(response.Body)
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "vector",
			"result":     rows,
		},
	})
}

func (h *Handler) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: "missing required parameter 'query'"})
		return
	}

	expr, err := ParseExpression(query)
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}

	analysis := AnalyzeExpression(expr)
	if !analysis.Supported {
		writePromError(w, apiError{StatusCode: http.StatusUnprocessableEntity, ErrorType: "unsupported", Error: fmt.Sprintf("unsupported PromQL (difficulty=%s): %s", analysis.Difficulty, analysis.Reason)})
		return
	}

	start, err := parsePrometheusTimestamp(r.URL.Query().Get("start"))
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}
	end, err := parsePrometheusTimestamp(r.URL.Query().Get("end"))
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}
	if end.Before(start) {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: "end must be greater than or equal to start"})
		return
	}
	step, err := parsePrometheusDuration(r.URL.Query().Get("step"))
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}

	if agg, ok := supportedSumAggregation(expr); ok {
		rows, apiErr := h.executeRangeSumAggregation(r.Context(), agg, start, end, step)
		if apiErr != nil {
			writePromError(w, *apiErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result":     rows,
			},
		})
		return
	}

	sql, params := buildRangeQuerySQL(h.opts, expr.String(), start.UnixMilli(), end.UnixMilli(), step.Milliseconds())
	response, err := h.client.Execute(r.Context(), sql, params)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	defer response.Body.Close()

	rows, apiErr := decodeMatrixRows(response.Body)
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data": map[string]any{
			"resultType": "matrix",
			"result":     rows,
		},
	})
}

func (h *Handler) handleLabels(w http.ResponseWriter, r *http.Request) {
	sql, params, err := buildLabelsQuery(h.opts, r)
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}
	response, err := h.client.Execute(r.Context(), sql, params)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	defer response.Body.Close()

	labels, apiErr := decodeStringRows[labelRow](response.Body, func(row labelRow) string { return row.Label })
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": labels})
}

func (h *Handler) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sql, params, err := buildLabelValuesQuery(h.opts, r, name)
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}
	response, err := h.client.Execute(r.Context(), sql, params)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	defer response.Body.Close()

	values, apiErr := decodeStringRows[valueRow](response.Body, func(row valueRow) string { return row.Value })
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": values})
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	sql, params, err := buildSeriesQuery(h.opts, r)
	if err != nil {
		writePromError(w, apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return
	}
	response, err := h.client.Execute(r.Context(), sql, params)
	if err != nil {
		writeAnyError(w, err)
		return
	}
	defer response.Body.Close()

	rows, apiErr := decodeSeriesRows(response.Body)
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": rows})
}

func decodeInstantRows(body io.Reader) ([]map[string]any, *apiError) {
	rows := make([]map[string]any, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row instantRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		timestamp, err := parseClickHouseTimestamp(row.Timestamp)
		if err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		rows = append(rows, map[string]any{
			"metric": tagsToObject(row.Tags),
			"value":  []any{timestamp, valueToPromString(row.Value)},
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
	}
	return rows, nil
}

func decodeMatrixRows(body io.Reader) ([]map[string]any, *apiError) {
	rows := make([]map[string]any, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row matrixRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		values := make([][]any, 0, len(row.TimeSeries))
		for _, sample := range row.TimeSeries {
			if len(sample) != 2 {
				return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: "unexpected time_series row shape"}
			}
			var timestampRaw string
			if err := json.Unmarshal(sample[0], &timestampRaw); err != nil {
				return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
			}
			timestamp, err := parseClickHouseTimestamp(timestampRaw)
			if err != nil {
				return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
			}
			values = append(values, []any{timestamp, valueToPromString(sample[1])})
		}
		rows = append(rows, map[string]any{
			"metric": tagsToObject(row.Tags),
			"values": values,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
	}
	return rows, nil
}

func decodeStringRows[T any](body io.Reader, getValue func(T) string) ([]string, *apiError) {
	rows := make([]string, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		rows = append(rows, getValue(row))
	}
	if err := scanner.Err(); err != nil {
		return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
	}
	return rows, nil
}

func decodeSeriesRows(body io.Reader) ([]map[string]string, *apiError) {
	rows := make([]map[string]string, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row tagsRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		rows = append(rows, tagsToObject(row.Tags))
	}
	if err := scanner.Err(); err != nil {
		return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
	}
	return rows, nil
}

func tagsToObject(tags [][]string) map[string]string {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		if len(tag) == 2 {
			result[tag[0]] = tag[1]
		}
	}
	return result
}

func valueToPromString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "NaN"
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			return text
		}
	}
	return string(raw)
}

func newScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 8*1024*1024)
	return scanner
}

func writeAnyError(w http.ResponseWriter, err error) {
	var queryErr *QueryError
	if ok := asQueryError(err, &queryErr); ok {
		writePromError(w, apiError{StatusCode: queryErr.StatusCode, ErrorType: queryErr.ErrorType, Error: queryErr.Message})
		return
	}
	writePromError(w, apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()})
}

func asQueryError(err error, target **QueryError) bool {
	queryErr, ok := err.(*QueryError)
	if ok {
		*target = queryErr
	}
	return ok
}

func writePromError(w http.ResponseWriter, apiErr apiError) {
	writeJSON(w, apiErr.StatusCode, map[string]any{
		"status":    "error",
		"errorType": apiErr.ErrorType,
		"error":     apiErr.Error,
	})
}

func writePlain(w http.ResponseWriter, statusCode int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = io.WriteString(w, body)
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	encoder := json.NewEncoder(w)
	_ = encoder.Encode(value)
}

func mustQueryStringValue(r *http.Request, key string) (string, *apiError) {
	value := r.URL.Query().Get(key)
	if value == "" {
		return "", &apiError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: fmt.Sprintf("missing required parameter %q", key)}
	}
	return value, nil
}

func withContextTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func formatUnixSeconds(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/float64(time.Second), 'f', -1, 64)
}
