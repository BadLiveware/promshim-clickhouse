package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
)

type stubService struct {
	response  *Response
	apiErr    *APIError
	transport string
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

func (s *stubService) InstantQuery(ctx context.Context, _ InstantQueryRequest) (*Response, *APIError) {
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) RangeQuery(ctx context.Context, _ RangeQueryRequest) (*Response, *APIError) {
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) ExplainInstant(ctx context.Context, _ InstantQueryRequest) (*Response, *APIError) {
	s.obs(ctx)
	return s.response, s.apiErr
}

func (s *stubService) ExplainRange(ctx context.Context, _ RangeQueryRequest) (*Response, *APIError) {
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
