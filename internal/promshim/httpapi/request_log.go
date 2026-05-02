package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const maxRequestLogValueLen = 256

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	bytes       int64
	wroteHeader bool
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.statusCode = statusCode
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	recorder := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	h.mux.ServeHTTP(recorder, r)
	h.logHTTPRequest(r, recorder, time.Since(started))
}

func (h *Handler) logHTTPRequest(r *http.Request, recorder *loggingResponseWriter, elapsed time.Duration) {
	logger := slog.Default()
	if !logger.Enabled(r.Context(), slog.LevelDebug) {
		return
	}

	values := r.Form
	if values == nil {
		values = r.URL.Query()
	}
	endpoint := requestLogEndpoint(r.URL.Path)
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"endpoint", endpoint,
		"status", recorder.statusCode,
		"duration_ms", elapsed.Milliseconds(),
		"bytes", recorder.bytes,
		"remote_addr", truncateLogPart(normalizeLogValue(r.RemoteAddr), maxRequestLogValueLen),
		"user_agent", truncateLogPart(normalizeLogValue(r.UserAgent()), maxRequestLogValueLen),
		"query_hash", requestLogHash(endpoint, values),
	}
	if query := values.Get("query"); !h.hidePromQL && query != "" {
		attrs = append(attrs, "query", truncateLogPart(normalizeLogValue(query), maxRequestLogValueLen))
	}
	addHeaderAttr := func(attrName, headerName string) {
		if value := recorder.Header().Get(headerName); value != "" {
			attrs = append(attrs, attrName, truncateLogPart(normalizeLogValue(value), maxRequestLogValueLen))
		}
	}
	addHeaderAttr("strategy", "X-Promshim-Strategy")
	addHeaderAttr("fallback_reason", "X-Promshim-Fallback-Reason")
	addHeaderAttr("routing_decision", "X-Promshim-Routing-Decision")
	addHeaderAttr("selected_strategy", "X-Promshim-Selected-Strategy")
	addHeaderAttr("served_candidate", "X-Promshim-Served-Candidate")
	addHeaderAttr("settings_profile", "X-Promshim-Settings-Profile")
	addHeaderAttr("ch_transport", "X-Promshim-CH-Transport")
	addHeaderAttr("ch_roundtrips", "X-Promshim-CH-Roundtrips")
	addHeaderAttr("ch_millis", "X-Promshim-CH-Millis")
	addHeaderAttr("error_type", "X-Promshim-Error-Type")

	logger.Debug("promshim http request", attrs...)
}

func normalizeLogValue(value string) string {
	value = strings.ReplaceAll(value, "\r", `\r`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\t", `\t`)
	return value
}

func requestLogEndpoint(path string) string {
	if strings.HasPrefix(path, "/api/v1/label/") && strings.HasSuffix(path, "/values") {
		return "label_values"
	}
	switch path {
	case "/api/v1/query":
		return "query"
	case "/api/v1/query_range":
		return "query_range"
	case "/api/v1/query_explain":
		return "query_explain"
	case "/api/v1/query_range_explain":
		return "query_range_explain"
	case "/api/v1/labels":
		return "labels"
	case "/api/v1/series":
		return "series"
	case "/api/v1/metadata":
		return "metadata"
	case "/api/v1/targets":
		return "targets"
	case "/api/v1/rules":
		return "rules"
	case "/api/v1/alerts":
		return "alerts"
	case "/api/v1/format_query":
		return "format_query"
	case "/api/v1/parse_query":
		return "parse_query"
	case "/health":
		return "health"
	case "/-/healthy":
		return "healthy"
	case "/-/ready":
		return "ready"
	default:
		return safeLogPart(path, "unknown")
	}
}
