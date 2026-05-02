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

func (s *stubService) Labels(ctx context.Context, _ MetadataRequest) (*Response, *APIError) {
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) LabelValues(ctx context.Context, _ LabelValuesRequest) (*Response, *APIError) {
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) Series(ctx context.Context, _ MetadataRequest) (*Response, *APIError) {
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
	if got.Query != `up{job="api"}` || got.Time != "300" || !got.Explain || got.NativeLoweringMode != "prefer" || got.RoutingPolicy != "cost_prefer" {
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
	if got.Query != "rate(up[5m])" || got.Start != "0" || got.End != "300" || got.Step != "60" || got.NativeLoweringMode != "shadow" || got.RoutingPolicy != "cost_shadow" {
		t.Fatalf("range request = %#v", got)
	}
}

func TestHandleQueryRejectsMalformedPostForm(t *testing.T) {
	stub := &stubService{response: &Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success"}}}
	handler := NewHandler(stub)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /query malformed form status = %d, want %d: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(stub.instantRequests) != 0 {
		t.Fatalf("instant request count = %d, want 0", len(stub.instantRequests))
	}
	if !strings.Contains(rec.Body.String(), `"errorType":"bad_data"`) {
		t.Fatalf("error response = %s", rec.Body.String())
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
