package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
)

type stubService struct {
	response               *Response
	apiErr                 *APIError
	readyErr               error
	transport              string
	instantRequests        []InstantQueryRequest
	rangeRequests          []RangeQueryRequest
	explainInstantRequests []InstantQueryRequest
	explainRangeRequests   []RangeQueryRequest
	labelRequests          []MetadataRequest
	seriesRequests         []MetadataRequest
	// observeOnCall, if non-nil, is called on every service method with the
	// passed context so the stub can simulate ClickHouse round-trips against
	// the CHMetrics that the router attached to the context.
	observeOnCall func(ctx context.Context)
}

func (s *stubService) obs(ctx context.Context) {
	if s.observeOnCall != nil {
		s.observeOnCall(ctx)
	}
}

func (s *stubService) ClickHouseTransport() string {
	return s.transport
}

func (s *stubService) InstantQuery(ctx context.Context, req InstantQueryRequest) (*Response, *APIError) {
	s.instantRequests = append(s.instantRequests, req)
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) RangeQuery(ctx context.Context, req RangeQueryRequest) (*Response, *APIError) {
	s.rangeRequests = append(s.rangeRequests, req)
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) ExplainInstant(ctx context.Context, req InstantQueryRequest) (*Response, *APIError) {
	s.explainInstantRequests = append(s.explainInstantRequests, req)
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) ExplainRange(ctx context.Context, req RangeQueryRequest) (*Response, *APIError) {
	s.explainRangeRequests = append(s.explainRangeRequests, req)
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) Labels(ctx context.Context, req MetadataRequest) (*Response, *APIError) {
	s.labelRequests = append(s.labelRequests, req)
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) LabelValues(ctx context.Context, _ LabelValuesRequest) (*Response, *APIError) {
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) Series(ctx context.Context, req MetadataRequest) (*Response, *APIError) {
	s.seriesRequests = append(s.seriesRequests, req)
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) Ready(context.Context) error {
	return s.readyErr
}

func TestHandleReadyChecksServiceReadiness(t *testing.T) {
	handler := NewHandler(&stubService{})
	req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/-/ready status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.TrimSpace(rec.Body.String()) != "ready" {
		t.Fatalf("/-/ready body = %q", rec.Body.String())
	}
}

func TestHandleReadyReturnsUnavailableWhenBackendFails(t *testing.T) {
	handler := NewHandler(&stubService{readyErr: context.DeadlineExceeded})
	req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/-/ready status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.TrimSpace(rec.Body.String()) != "not ready" {
		t.Fatalf("/-/ready body = %q", rec.Body.String())
	}
}

func TestHandleQuerySetsPromshimHeaders(t *testing.T) {
	var observedComment string
	stub := &stubService{
		response: &Response{
			StatusCode:      http.StatusOK,
			Strategy:        "native_sql",
			FallbackReason:  "",
			SettingsProfile: "default_safe",
			Routing: &RoutingInfo{
				Policy:           "cost_shadow",
				Decision:         "shadow_only",
				Reason:           "cost_shadow_strict_default",
				StrictStrategy:   "native_sql",
				SelectedStrategy: "native_sql",
				CandidateDecision: &CandidateDecision{
					StrictCandidate:   "native_sql",
					SelectedCandidate: "native_sql",
					ServedCandidate:   "native_sql",
				},
				Class: QueryCostClass{Family: "selector"},
			},
			Body: map[string]any{"status": "success"},
		},
		transport: "native",
		observeOnCall: func(ctx context.Context) {
			observedComment = obs.LogCommentFromContext(ctx)
			obs.FromContext(ctx).Observe(7 * time.Millisecond)
			obs.FromContext(ctx).Observe(3 * time.Millisecond)
		},
	}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Promshim-Strategy"); got != "native_sql" {
		t.Fatalf("X-Promshim-Strategy = %q, want native_sql", got)
	}
	if got := rec.Header().Get("X-Promshim-Fallback-Reason"); got != "" {
		t.Fatalf("X-Promshim-Fallback-Reason = %q, want empty", got)
	}
	if got := rec.Header().Get("X-Promshim-Strict-Candidate"); got != "native_sql" {
		t.Fatalf("X-Promshim-Strict-Candidate = %q, want native_sql", got)
	}
	if got := rec.Header().Get("X-Promshim-Selected-Candidate"); got != "native_sql" {
		t.Fatalf("X-Promshim-Selected-Candidate = %q, want native_sql", got)
	}
	if got := rec.Header().Get("X-Promshim-Served-Candidate"); got != "native_sql" {
		t.Fatalf("X-Promshim-Served-Candidate = %q, want native_sql", got)
	}
	if got := rec.Header().Get("X-Promshim-CH-Roundtrips"); got != "2" {
		t.Fatalf("X-Promshim-CH-Roundtrips = %q, want 2", got)
	}
	if got := rec.Header().Get("X-Promshim-CH-Millis"); got != "10" {
		t.Fatalf("X-Promshim-CH-Millis = %q, want 10", got)
	}
	if got := rec.Header().Get("X-Promshim-CH-Transport"); got != "native" {
		t.Fatalf("X-Promshim-CH-Transport = %q, want native", got)
	}
	if got := rec.Header().Get("X-Promshim-Settings-Profile"); got != "default_safe" {
		t.Fatalf("X-Promshim-Settings-Profile = %q, want default_safe", got)
	}
	if observedComment == "" || !strings.Contains(observedComment, "endpoint=query") || strings.Contains(observedComment, "up") {
		t.Fatalf("generated log comment = %q, want bounded hashed query comment", observedComment)
	}
}

func TestHandleQuerySanitizesHeaderLogComment(t *testing.T) {
	var observedComment string
	stub := &stubService{
		response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}},
		observeOnCall: func(ctx context.Context) {
			observedComment = obs.LogCommentFromContext(ctx)
		},
	}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up", nil)
	req.Header.Set("X-Promshim-Log-Comment", strings.Repeat("unsafe comment\n", 40))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if observedComment == "" {
		t.Fatal("expected log comment")
	}
	if strings.ContainsAny(observedComment, " \n\r\t") {
		t.Fatalf("expected sanitized log comment, got %q", observedComment)
	}
	if len(observedComment) > maxRequestLogCommentLen {
		t.Fatalf("expected bounded log comment, len=%d", len(observedComment))
	}
}

func TestHandleQueryRangeSetsFallbackReason(t *testing.T) {
	stub := &stubService{
		response: &Response{
			StatusCode:     http.StatusOK,
			Strategy:       "local",
			FallbackReason: "subquery root not lowered",
			Body:           map[string]any{"status": "success"},
		},
	}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?query=up&start=0&end=60&step=30s", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Promshim-Strategy"); got != "local" {
		t.Fatalf("X-Promshim-Strategy = %q, want local", got)
	}
	if got := rec.Header().Get("X-Promshim-Fallback-Reason"); got != "subquery root not lowered" {
		t.Fatalf("X-Promshim-Fallback-Reason = %q", got)
	}
	if got := rec.Header().Get("X-Promshim-CH-Roundtrips"); got != "0" {
		t.Fatalf("X-Promshim-CH-Roundtrips = %q, want 0", got)
	}
}

func TestHandleQuerySupportsPostForm(t *testing.T) {
	stub := &stubService{response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}}}
	handler := NewHandler(stub)
	form := url.Values{
		"query":                {`up{job="api"}`},
		"time":                 {"300"},
		"limit":                {"10"},
		"timeout":              {"5s"},
		"stats":                {"all"},
		"lookback_delta":       {"30s"},
		"explain":              {"true"},
		"native_lowering_mode": {"prefer"},
		"routing_policy":       {"cost_prefer"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query?query=url_query", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /query status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(stub.instantRequests) != 1 {
		t.Fatalf("instant request count = %d, want 1", len(stub.instantRequests))
	}
	got := stub.instantRequests[0]
	if got.Query != `up{job="api"}` || got.Time != "300" || got.Limit != "10" || got.Timeout != "5s" || got.Stats != "all" || got.LookbackDelta != "30s" || !got.Explain || got.NativeLoweringMode != "prefer" || got.RoutingPolicy != "cost_prefer" {
		t.Fatalf("instant request = %#v", got)
	}
}

func TestHandleQueryRangeSupportsPostForm(t *testing.T) {
	stub := &stubService{response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}}}
	handler := NewHandler(stub)
	form := url.Values{
		"query":                {"rate(up[5m])"},
		"start":                {"0"},
		"end":                  {"300"},
		"step":                 {"60"},
		"limit":                {"5"},
		"timeout":              {"5s"},
		"native_lowering_mode": {"shadow"},
		"routing_policy":       {"cost_shadow"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query_range", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /query_range status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(stub.rangeRequests) != 1 {
		t.Fatalf("range request count = %d, want 1", len(stub.rangeRequests))
	}
	got := stub.rangeRequests[0]
	if got.Query != "rate(up[5m])" || got.Start != "0" || got.End != "300" || got.Step != "60" || got.Limit != "5" || got.Timeout != "5s" || got.NativeLoweringMode != "shadow" || got.RoutingPolicy != "cost_shadow" {
		t.Fatalf("range request = %#v", got)
	}
}

func TestHandleQueryIgnoresMalformedUnusedParameters(t *testing.T) {
	stub := &stubService{response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}}}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=up&unused=%zz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /query malformed unused parameter status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(stub.instantRequests) != 1 {
		t.Fatalf("instant request count = %d, want 1", len(stub.instantRequests))
	}
	if got := stub.instantRequests[0].Query; got != "up" {
		t.Fatalf("query = %q, want up", got)
	}
}

func TestHandleLabelsOmitsStrategyHeader(t *testing.T) {
	stub := &stubService{
		response: &Response{
			StatusCode: http.StatusOK,
			Body:       map[string]any{"status": "success"},
		},
		observeOnCall: func(ctx context.Context) {
			obs.FromContext(ctx).Observe(5 * time.Millisecond)
		},
	}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/labels", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Promshim-Strategy"); got != "" {
		t.Fatalf("X-Promshim-Strategy = %q, want empty for /labels", got)
	}
	if got := rec.Header().Get("X-Promshim-CH-Roundtrips"); got != "1" {
		t.Fatalf("X-Promshim-CH-Roundtrips = %q, want 1", got)
	}
}

func TestHandleLabelsSupportsPostForm(t *testing.T) {
	stub := &stubService{response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}}}
	handler := NewHandler(stub)
	form := url.Values{"match[]": {`up{job="api"}`, `process_cpu_seconds_total`}, "start": {"0"}, "end": {"300"}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/labels", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /labels status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(stub.labelRequests) != 1 {
		t.Fatalf("label request count = %d, want 1", len(stub.labelRequests))
	}
	got := stub.labelRequests[0]
	if strings.Join(got.Matchers, ",") != `up{job="api"},process_cpu_seconds_total` || got.Start != "0" || got.End != "300" {
		t.Fatalf("label request = %#v", got)
	}
}

func TestHandleSeriesSupportsPostForm(t *testing.T) {
	stub := &stubService{response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}}}
	handler := NewHandler(stub)
	form := url.Values{"match[]": {`up{job="api"}`}, "start": {"0"}, "end": {"300"}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/series", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /series status = %d: %s", rec.Code, rec.Body.String())
	}
	if len(stub.seriesRequests) != 1 {
		t.Fatalf("series request count = %d, want 1", len(stub.seriesRequests))
	}
	got := stub.seriesRequests[0]
	if len(got.Matchers) != 1 || got.Matchers[0] != `up{job="api"}` || got.Start != "0" || got.End != "300" {
		t.Fatalf("series request = %#v", got)
	}
}

func TestHandlePrometheusCompatibilityDiscoveryEndpoints(t *testing.T) {
	handler := NewHandler(&stubService{})
	for _, path := range []string{"/api/v1/metadata", "/api/v1/targets", "/api/v1/rules", "/api/v1/alerts"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d: %s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"status":"success"`) {
			t.Fatalf("GET %s body = %s", path, rec.Body.String())
		}
	}
}

func TestHandleFormatAndParseQuery(t *testing.T) {
	handler := NewHandler(&stubService{})
	form := url.Values{"query": {`sum by (job) (up)`}}
	formatReq := httptest.NewRequest(http.MethodPost, "/api/v1/format_query", strings.NewReader(form.Encode()))
	formatReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	formatRec := httptest.NewRecorder()
	handler.ServeHTTP(formatRec, formatReq)
	if formatRec.Code != http.StatusOK {
		t.Fatalf("POST /format_query status = %d: %s", formatRec.Code, formatRec.Body.String())
	}
	if !strings.Contains(formatRec.Body.String(), "sum by") {
		t.Fatalf("format response = %s", formatRec.Body.String())
	}

	parseReq := httptest.NewRequest(http.MethodGet, "/api/v1/parse_query?query=up%7Bjob%3D%22api%22%7D", nil)
	parseRec := httptest.NewRecorder()
	handler.ServeHTTP(parseRec, parseReq)
	if parseRec.Code != http.StatusOK {
		t.Fatalf("GET /parse_query status = %d: %s", parseRec.Code, parseRec.Body.String())
	}
	if !strings.Contains(parseRec.Body.String(), `"type":"vectorSelector"`) {
		t.Fatalf("parse response = %s", parseRec.Body.String())
	}
}

func TestHandleFormatAndParseQueryUsePrometheusParser(t *testing.T) {
	handler := NewHandler(&stubService{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/format_query?query=holt_winters%28up%5B5m%5D%2C0.1%2C0.2%29", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /format_query status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "holt_winters") || strings.Contains(rec.Body.String(), "double_exponential_smoothing") {
		t.Fatalf("format error should preserve original function name without alias rewrite, got %s", rec.Body.String())
	}
}

func TestTranslatePromQLASTNumberLiteralUsesAnyMap(t *testing.T) {
	expr, err := promQLAPIParser().ParseExpr("1")
	if err != nil {
		t.Fatalf("ParseExpr: %v", err)
	}
	if _, ok := translatePromQLAST(expr).(map[string]any); !ok {
		t.Fatalf("number literal AST node type = %T, want map[string]any", translatePromQLAST(expr))
	}
}

func TestHandleOptionsWildcard(t *testing.T) {
	handler := NewHandler(&stubService{})

	postReq := httptest.NewRequest(http.MethodOptions, "/api/v1/query_range", nil)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS POST-capable status = %d, want %d", postRec.Code, http.StatusNoContent)
	}
	if got := postRec.Header().Get("Allow"); !strings.Contains(got, "POST") || !strings.Contains(got, "GET") {
		t.Fatalf("POST-capable Allow = %q", got)
	}

	getOnlyReq := httptest.NewRequest(http.MethodOptions, "/api/v1/metadata", nil)
	getOnlyRec := httptest.NewRecorder()
	handler.ServeHTTP(getOnlyRec, getOnlyReq)
	if getOnlyRec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS GET-only status = %d, want %d", getOnlyRec.Code, http.StatusNoContent)
	}
	if got := getOnlyRec.Header().Get("Allow"); strings.Contains(got, "POST") || !strings.Contains(got, "GET") {
		t.Fatalf("GET-only Allow = %q", got)
	}
}

func TestErrorResponseOmitsHeaders(t *testing.T) {
	stub := &stubService{
		apiErr: &APIError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: "nope"},
	}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Promshim-Strategy"); got != "" {
		t.Fatalf("expected no strategy header on error, got %q", got)
	}
	if got := rec.Header().Get("X-Promshim-CH-Roundtrips"); got != "" {
		t.Fatalf("expected no CH roundtrips header on error, got %q", got)
	}
}
