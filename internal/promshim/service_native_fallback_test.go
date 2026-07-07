package promshim

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fallbackStubClickHouse fakes the ClickHouse HTTP endpoint: any SQL that
// contains a native binary-vector-join shape (identified by its join_group
// projection) fails with an execution-class 500, while plain selector scans
// issued by local (tier-4) execution succeed with an empty row set. This
// simulates a committed native plan whose rendered SQL ClickHouse rejects at
// execution (issue #39) against an otherwise healthy server.
func fallbackStubClickHouse(t *testing.T, nativeStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "join_group") {
			w.WriteHeader(nativeStatus)
			_, _ = w.Write([]byte("Code: 344. DB::Exception: Reference to materialized CTE is not supported in this context"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func newFallbackHandler(t *testing.T, endpoint, mode string, allowOverrides bool) http.Handler {
	t.Helper()
	handler, err := NewHandler(Options{
		ClickHouseEndpoint:           endpoint,
		NativeLoweringMode:           local.NativeLoweringMode(mode),
		AllowRequestRoutingOverrides: allowOverrides,
		DisableEntireQueryDelegation: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler
}

func fallbackCounterValue(t *testing.T, endpoint, mode, fromStrategy, outcome string) float64 {
	t.Helper()
	return testutil.ToFloat64(routingmetrics.ExecutionFallbacks.WithLabelValues(endpoint, mode, fromStrategy, outcome))
}

func decodeJSONBody(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding response body %q: %v", res.Body.String(), err)
	}
	return payload
}

const fallbackTriggerQueryPath = "/api/v1/query?query=up%20unless%20%28up%20%3D%3D%200%29&time=300"

func TestInstantQueryPreferModeFallsBackToLocalOnExecutionError(t *testing.T) {
	server := fallbackStubClickHouse(t, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "prefer", false)
	before := fallbackCounterValue(t, "query", "prefer", "native_sql", "success")

	req := httptest.NewRequest(http.MethodGet, fallbackTriggerQueryPath, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	if payload["status"] != "success" {
		t.Fatalf("status field = %v, want success; body: %s", payload["status"], res.Body.String())
	}
	if got := res.Header().Get("X-Promshim-Fallback-Reason"); got != "native_execution_error" {
		t.Fatalf("X-Promshim-Fallback-Reason = %q, want native_execution_error", got)
	}
	after := fallbackCounterValue(t, "query", "prefer", "native_sql", "success")
	if after != before+1 {
		t.Fatalf("fallback success counter = %v, want %v", after, before+1)
	}
}

func TestInstantQueryForceSupportedModeStillHardFails(t *testing.T) {
	server := fallbackStubClickHouse(t, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "force_supported", false)
	before := fallbackCounterValue(t, "query", "force_supported", "native_sql", "success")

	req := httptest.NewRequest(http.MethodGet, fallbackTriggerQueryPath, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	if payload["errorType"] != "execution" {
		t.Fatalf("errorType = %v, want execution; body: %s", payload["errorType"], res.Body.String())
	}
	after := fallbackCounterValue(t, "query", "force_supported", "native_sql", "success")
	if after != before {
		t.Fatalf("fallback counter moved in force_supported mode: %v -> %v", before, after)
	}
}

func TestInstantQueryClientClassErrorStays4xxWithoutFallback(t *testing.T) {
	// ClickHouse HTTP 4xx responses surface as bad_data (client-class) and
	// must keep their 400 status instead of triggering a local retry.
	server := fallbackStubClickHouse(t, http.StatusBadRequest)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "prefer", false)
	before := fallbackCounterValue(t, "query", "prefer", "native_sql", "success")

	req := httptest.NewRequest(http.MethodGet, fallbackTriggerQueryPath, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	if payload["errorType"] != "bad_data" {
		t.Fatalf("errorType = %v, want bad_data; body: %s", payload["errorType"], res.Body.String())
	}
	after := fallbackCounterValue(t, "query", "prefer", "native_sql", "success")
	if after != before {
		t.Fatalf("fallback counter moved for client-class error: %v -> %v", before, after)
	}
}

func TestInstantQueryShadowModeUnaffectedByNativeExecutionError(t *testing.T) {
	// Shadow serves tier 4 directly; native failures belong to the shadow
	// runner's divergence records, never to the execution fallback.
	server := fallbackStubClickHouse(t, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "shadow", true)
	before := fallbackCounterValue(t, "query", "shadow", "native_sql", "success")

	req := httptest.NewRequest(http.MethodGet, fallbackTriggerQueryPath, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", res.Code, res.Body.String())
	}
	after := fallbackCounterValue(t, "query", "shadow", "native_sql", "success")
	if after != before {
		t.Fatalf("fallback counter moved in shadow mode: %v -> %v", before, after)
	}
}

func TestInstantQueryExplainSurfacesExecutionFallback(t *testing.T) {
	server := fallbackStubClickHouse(t, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "explain", false)
	before := fallbackCounterValue(t, "query", "explain", "native_sql", "success")

	req := httptest.NewRequest(http.MethodGet, fallbackTriggerQueryPath, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	fallback, ok := payload["executionFallback"].(map[string]any)
	if !ok {
		t.Fatalf("expected executionFallback in explain body, got: %s", res.Body.String())
	}
	if fallback["fromStrategy"] != "native_sql" || fallback["outcome"] != "success" {
		t.Fatalf("unexpected executionFallback payload: %v", fallback)
	}
	if fallback["clickHouseError"] == "" {
		t.Fatalf("expected clickHouseError in executionFallback payload: %v", fallback)
	}
	plan, ok := payload["plan"].(map[string]any)
	if !ok || plan["strategy"] != "local" {
		t.Fatalf("expected served plan strategy local, got: %v", payload["plan"])
	}
	if plan["fallbackReason"] != "native_execution_error" {
		t.Fatalf("expected plan fallbackReason native_execution_error, got: %v", plan)
	}
	after := fallbackCounterValue(t, "query", "explain", "native_sql", "success")
	if after != before+1 {
		t.Fatalf("fallback success counter = %v, want %v", after, before+1)
	}
}

func TestRangeQueryPreferModeFallsBackToLocalOnExecutionError(t *testing.T) {
	server := fallbackStubClickHouse(t, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "prefer", false)
	before := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "success")

	url := "/api/v1/query_range?query=up%20unless%20%28up%20%3D%3D%200%29&start=300&end=600&step=60"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	if payload["status"] != "success" {
		t.Fatalf("status field = %v, want success; body: %s", payload["status"], res.Body.String())
	}
	after := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "success")
	if after != before+1 {
		t.Fatalf("fallback success counter = %v, want %v", after, before+1)
	}
}
