package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"ch-observability/internal/promshim/obs"
)

type APIError struct {
	StatusCode int    `json:"-"`
	ErrorType  string `json:"errorType"`
	Error      string `json:"error"`
}

type Response struct {
	StatusCode int
	Body       any
	Stream     func(http.ResponseWriter) error
	// Strategy and FallbackReason surface the root plan strategy on the
	// successful response as X-Promshim-Strategy / X-Promshim-Fallback-Reason
	// headers. They are advisory — producers may leave them unset on endpoints
	// where the concept does not apply (labels, series, label_values).
	Strategy       string
	FallbackReason string
}

type InstantQueryRequest struct {
	Query              string
	Time               string
	Explain            bool
	NativeLoweringMode string
}

type RangeQueryRequest struct {
	Query              string
	Start              string
	End                string
	Step               string
	Explain            bool
	NativeLoweringMode string
}

type MetadataRequest struct {
	Matchers []string
	Start    string
	End      string
}

type LabelValuesRequest struct {
	Name string
	MetadataRequest
}

type Service interface {
	InstantQuery(context.Context, InstantQueryRequest) (*Response, *APIError)
	RangeQuery(context.Context, RangeQueryRequest) (*Response, *APIError)
	ExplainInstant(context.Context, InstantQueryRequest) (*Response, *APIError)
	ExplainRange(context.Context, RangeQueryRequest) (*Response, *APIError)
	Labels(context.Context, MetadataRequest) (*Response, *APIError)
	LabelValues(context.Context, LabelValuesRequest) (*Response, *APIError)
	Series(context.Context, MetadataRequest) (*Response, *APIError)
}

type Handler struct {
	service Service
	mux     *http.ServeMux
}

func NewHandler(service Service) http.Handler {
	h := &Handler{service: service, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /-/healthy", h.handleHealthy)
	h.mux.HandleFunc("GET /-/ready", h.handleReady)
	h.mux.HandleFunc("GET /api/v1/query", h.handleQuery)
	h.mux.HandleFunc("GET /api/v1/query_range", h.handleQueryRange)
	h.mux.HandleFunc("GET /api/v1/query_explain", h.handleQueryExplain)
	h.mux.HandleFunc("GET /api/v1/query_range_explain", h.handleQueryRangeExplain)
	h.mux.HandleFunc("GET /api/v1/labels", h.handleLabels)
	h.mux.HandleFunc("GET /api/v1/label/{name}/values", h.handleLabelValues)
	h.mux.HandleFunc("GET /api/v1/series", h.handleSeries)
	return h
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
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.InstantQuery(ctx, InstantQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Time:               r.URL.Query().Get("time"),
		Explain:            wantsExplain(r),
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func (h *Handler) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.RangeQuery(ctx, RangeQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Start:              r.URL.Query().Get("start"),
		End:                r.URL.Query().Get("end"),
		Step:               r.URL.Query().Get("step"),
		Explain:            wantsExplain(r),
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func (h *Handler) handleQueryExplain(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.ExplainInstant(ctx, InstantQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Time:               r.URL.Query().Get("time"),
		Explain:            true,
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func (h *Handler) handleQueryRangeExplain(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.ExplainRange(ctx, RangeQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Start:              r.URL.Query().Get("start"),
		End:                r.URL.Query().Get("end"),
		Step:               r.URL.Query().Get("step"),
		Explain:            true,
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func (h *Handler) handleLabels(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.Labels(ctx, MetadataRequest{
		Matchers: readMatchers(r),
		Start:    r.URL.Query().Get("start"),
		End:      r.URL.Query().Get("end"),
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func (h *Handler) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.LabelValues(ctx, LabelValuesRequest{
		Name: r.PathValue("name"),
		MetadataRequest: MetadataRequest{
			Matchers: readMatchers(r),
			Start:    r.URL.Query().Get("start"),
			End:      r.URL.Query().Get("end"),
		},
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(r.Context())
	resp, apiErr := h.service.Series(ctx, MetadataRequest{
		Matchers: readMatchers(r),
		Start:    r.URL.Query().Get("start"),
		End:      r.URL.Query().Get("end"),
	})
	writeServiceResult(w, resp, apiErr, metrics)
}

func writeServiceResult(w http.ResponseWriter, resp *Response, apiErr *APIError, metrics *obs.CHMetrics) {
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	if resp == nil {
		writePromError(w, APIError{StatusCode: http.StatusInternalServerError, ErrorType: "execution", Error: "service returned no response"})
		return
	}
	setPromshimHeaders(w, resp, metrics)
	if resp.Stream != nil {
		if err := resp.Stream(w); err != nil {
			writePromError(w, APIError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()})
		}
		return
	}
	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, resp.Body)
}

func setPromshimHeaders(w http.ResponseWriter, resp *Response, metrics *obs.CHMetrics) {
	if resp.Strategy != "" {
		w.Header().Set("X-Promshim-Strategy", resp.Strategy)
	}
	if resp.FallbackReason != "" {
		w.Header().Set("X-Promshim-Fallback-Reason", resp.FallbackReason)
	}
	if metrics != nil {
		w.Header().Set("X-Promshim-CH-Roundtrips", strconv.FormatInt(metrics.Roundtrips(), 10))
		w.Header().Set("X-Promshim-CH-Millis", strconv.FormatInt(metrics.Millis(), 10))
	}
}

func readMatchers(r *http.Request) []string {
	matchers := r.URL.Query()["match[]"]
	if len(matchers) > 0 {
		return matchers
	}
	return r.URL.Query()["match"]
}

func wantsExplain(r *http.Request) bool {
	switch r.URL.Query().Get("explain") {
	case "1", "true", "TRUE", "True":
		return true
	default:
		return false
	}
}

func writePromError(w http.ResponseWriter, apiErr APIError) {
	writeJSON(w, apiErr.StatusCode, map[string]any{
		"status":    "error",
		"errorType": apiErr.ErrorType,
		"error":     apiErr.Error,
	})
}

func writePlain(w http.ResponseWriter, statusCode int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(body))
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
