package promshim

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
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
	return fallbackStubClickHouseWithLocal(t, nativeStatus, http.StatusOK)
}

// fallbackStubClickHouseWithLocal extends fallbackStubClickHouse so the plain
// selector scans issued by the local (tier-4) retry can also be made to fail:
// join_group SQL returns nativeStatus, everything else returns localStatus
// (http.StatusOK serves an empty row set, any error status surfaces to the
// local retry). This drives the fallback's local_error / plan-vs-eval outcome
// branches, which a healthy local server never reaches.
func fallbackStubClickHouseWithLocal(t *testing.T, nativeStatus, localStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "join_group") {
			w.WriteHeader(nativeStatus)
			_, _ = w.Write([]byte("Code: 344. DB::Exception: Reference to materialized CTE is not supported in this context"))
			return
		}
		if localStatus != http.StatusOK {
			w.WriteHeader(localStatus)
			_, _ = w.Write([]byte("Code: 241. DB::Exception: local selector scan rejected"))
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
	// Served-view observability must reflect the local execution that was
	// actually served, not the native plan that failed.
	if got := res.Header().Get("X-Promshim-Strategy"); got != "local" {
		t.Fatalf("X-Promshim-Strategy = %q, want local", got)
	}
	if got := res.Header().Get("X-Promshim-Served-Candidate"); got != "full_local" {
		t.Fatalf("X-Promshim-Served-Candidate = %q, want full_local", got)
	}
	// Routing-decision provenance is preserved: routing selected native, and
	// the selected-vs-served divergence is the fallback signal.
	if got := res.Header().Get("X-Promshim-Selected-Strategy"); got != "native_sql" {
		t.Fatalf("X-Promshim-Selected-Strategy = %q, want native_sql (routing selection preserved)", got)
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
	// Routing metadata in the explain body agrees with what was served: the
	// served candidate is full_local, while the routing selection is preserved.
	routing, ok := payload["routing"].(map[string]any)
	if !ok {
		t.Fatalf("expected routing metadata in explain body, got: %s", res.Body.String())
	}
	candidateDecision, ok := routing["candidateDecision"].(map[string]any)
	if !ok {
		t.Fatalf("expected routing.candidateDecision in explain body, got: %v", routing)
	}
	if candidateDecision["servedCandidate"] != "full_local" {
		t.Fatalf("routing.candidateDecision.servedCandidate = %v, want full_local", candidateDecision["servedCandidate"])
	}
	if routing["selectedStrategy"] != "native_sql" {
		t.Fatalf("routing.selectedStrategy = %v, want native_sql (routing selection preserved)", routing["selectedStrategy"])
	}
	after := fallbackCounterValue(t, "query", "explain", "native_sql", "success")
	if after != before+1 {
		t.Fatalf("fallback success counter = %v, want %v", after, before+1)
	}
}

func TestExplainHasClickHouseSideNodeRecursesIntoChildren(t *testing.T) {
	// A partially-native plan (subtree pushdown): the root runs locally but a
	// child executes on the ClickHouse side. The tier-3 recursion must treat
	// it as fallback-eligible.
	tier3 := local.ExplainNode{
		Strategy: "local",
		Children: []local.ExplainNode{
			{Strategy: "local"},
			{Strategy: "native_sql"},
		},
	}
	if !explainHasClickHouseSideNode(tier3) {
		t.Fatalf("expected partially-native plan (local root, native_sql child) to be fallback-eligible")
	}
	// A pure-local tree gains nothing from a local retry.
	pureLocal := local.ExplainNode{
		Strategy: "local",
		Children: []local.ExplainNode{
			{Strategy: "local"},
			{Strategy: "local", Children: []local.ExplainNode{{Strategy: "local"}}},
		},
	}
	if explainHasClickHouseSideNode(pureLocal) {
		t.Fatalf("expected pure-local plan tree to be ineligible for fallback")
	}
	// A deeply-nested ClickHouse-side node is still found.
	nested := local.ExplainNode{
		Strategy: "local",
		Children: []local.ExplainNode{
			{Strategy: "local", Children: []local.ExplainNode{{Strategy: "delegated_promql"}}},
		},
	}
	if !explainHasClickHouseSideNode(nested) {
		t.Fatalf("expected nested ClickHouse-side node to be fallback-eligible")
	}
	// A pure-local chunked range plan reports the "chunked_local" strategy.
	// It is local-family and must NOT be treated as ClickHouse-side: a local
	// retry gains nothing and would fabricate a misleading native->local
	// fallback. Its native counterpart "chunked_native" IS ClickHouse-side.
	chunkedLocal := local.ExplainNode{
		Kind:     "range_chunk",
		Strategy: "chunked_local",
		Children: []local.ExplainNode{{Strategy: "local"}},
	}
	if explainHasClickHouseSideNode(chunkedLocal) {
		t.Fatalf("expected pure-local chunked range plan (chunked_local) to be ineligible for fallback")
	}
	chunkedNative := local.ExplainNode{
		Kind:     "range_chunk",
		Strategy: "chunked_native",
		Children: []local.ExplainNode{{Strategy: "native_sql"}},
	}
	if !explainHasClickHouseSideNode(chunkedNative) {
		t.Fatalf("expected chunked_native range plan to be fallback-eligible")
	}
}

func TestIsClickHouseSideStrategy(t *testing.T) {
	// Locks the ClickHouse-side strategy set. These are the ExplainNode.Strategy
	// values that execute against ClickHouse.
	clickHouseSide := []string{"native_sql", "native_sql_expression", "delegated_promql", "chunked_native"}
	for _, strategy := range clickHouseSide {
		if !isClickHouseSideStrategy(strategy) {
			t.Fatalf("strategy %q should be ClickHouse-side", strategy)
		}
	}
	// Local-family strategies, the empty root/pre-finalize strategy, and any
	// unknown/future strategy fail safe to NOT ClickHouse-side (not
	// fallback-eligible) so a local failure stays a visible error instead of
	// fabricating a native->local fallback.
	localFamily := []string{"local", "chunked_local", "", "some_future_strategy"}
	for _, strategy := range localFamily {
		if isClickHouseSideStrategy(strategy) {
			t.Fatalf("strategy %q should not be ClickHouse-side", strategy)
		}
	}
}

func TestInstantExecutionFallbackLocalPlanBuildErrorReservesNative(t *testing.T) {
	// The local retry's tier-4 plan fails to build. No local execution
	// happens, so the fallback returns no report and no error and the caller
	// re-serves the original native error; the local_plan_error outcome is
	// still recorded. A topk with a non-literal parameter is rejected at the
	// mode-independent logical-build step, so off-mode build fails without any
	// ClickHouse round-trip — a near-zero-value service suffices.
	h := &queryService{opts: Options{}}
	servedExplain := local.ExplainNode{Strategy: "native_sql"}
	nativeErr := local.NewExecutionErrorf("native clickhouse execution failed")
	before := fallbackCounterValue(t, "query", "prefer", "native_sql", "local_plan_error")

	value, explain, report, apiErr := h.instantExecutionFallback(
		context.Background(),
		httpapi.InstantQueryRequest{Query: "topk(1 + 2, up)"},
		local.NativeLoweringModePrefer,
		servedExplain,
		time.Unix(300, 0),
		nativeErr,
	)
	if value != nil || report != nil || apiErr != nil {
		t.Fatalf("expected no fallback on local plan build failure, got value=%v report=%v apiErr=%v", value, report, apiErr)
	}
	if explain.Strategy != "" {
		t.Fatalf("expected zero explain on plan build failure, got %q", explain.Strategy)
	}
	after := fallbackCounterValue(t, "query", "prefer", "native_sql", "local_plan_error")
	if after != before+1 {
		t.Fatalf("fallback local_plan_error counter = %v, want %v", after, before+1)
	}
}

func TestRangeExecutionFallbackLocalPlanBuildErrorReservesNative(t *testing.T) {
	// Range-endpoint counterpart of the instant plan-build-error case.
	h := &queryService{opts: Options{}}
	servedExplain := local.ExplainNode{Strategy: "native_sql"}
	nativeErr := local.NewExecutionErrorf("native clickhouse execution failed")
	before := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "local_plan_error")

	start, end, step := time.Unix(300, 0), time.Unix(600, 0), time.Minute
	value, explain, report, apiErr := h.rangeExecutionFallback(
		context.Background(),
		httpapi.RangeQueryRequest{Query: "topk(1 + 2, up)", Start: "300", End: "600", Step: "60"},
		local.NativeLoweringModePrefer,
		servedExplain,
		start, end, step,
		nativeErr,
	)
	if value != nil || report != nil || apiErr != nil {
		t.Fatalf("expected no fallback on local plan build failure, got value=%v report=%v apiErr=%v", value, report, apiErr)
	}
	if explain.Strategy != "" {
		t.Fatalf("expected zero explain on plan build failure, got %q", explain.Strategy)
	}
	after := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "local_plan_error")
	if after != before+1 {
		t.Fatalf("fallback local_plan_error counter = %v, want %v", after, before+1)
	}
}

func TestInstantQueryLocalRetryExecutionErrorServesLocalError(t *testing.T) {
	// Native execution fails (500), the local retry builds but its selector
	// scan also fails with an execution-class 500. That local error is itself
	// execution-class, so it is the one served (502), and the local_error
	// outcome is recorded.
	server := fallbackStubClickHouseWithLocal(t, http.StatusInternalServerError, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "prefer", false)
	before := fallbackCounterValue(t, "query", "prefer", "native_sql", "local_error")

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
	if errText, _ := payload["error"].(string); !strings.Contains(errText, "local selector scan rejected") {
		t.Fatalf("expected served error to be the local execution error, got: %v", payload["error"])
	}
	after := fallbackCounterValue(t, "query", "prefer", "native_sql", "local_error")
	if after != before+1 {
		t.Fatalf("fallback local_error counter = %v, want %v", after, before+1)
	}
}

func TestInstantQueryLocalRetryClientErrorReservesNativeError(t *testing.T) {
	// Native execution fails (500), the local retry builds but its selector
	// scan fails with a client-class 400 (bad_data). A local 4xx must not mask
	// the native 502, so the ORIGINAL native error is re-served while the
	// local_error outcome is still recorded.
	server := fallbackStubClickHouseWithLocal(t, http.StatusInternalServerError, http.StatusBadRequest)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "prefer", false)
	before := fallbackCounterValue(t, "query", "prefer", "native_sql", "local_error")

	req := httptest.NewRequest(http.MethodGet, fallbackTriggerQueryPath, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (original native error); body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	if payload["errorType"] != "execution" {
		t.Fatalf("errorType = %v, want execution (native error re-served); body: %s", payload["errorType"], res.Body.String())
	}
	if errText, _ := payload["error"].(string); !strings.Contains(errText, "materialized CTE") {
		t.Fatalf("expected served error to be the original native error, got: %v", payload["error"])
	}
	after := fallbackCounterValue(t, "query", "prefer", "native_sql", "local_error")
	if after != before+1 {
		t.Fatalf("fallback local_error counter = %v, want %v", after, before+1)
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
	if got := res.Header().Get("X-Promshim-Strategy"); got != "local" {
		t.Fatalf("X-Promshim-Strategy = %q, want local", got)
	}
	if got := res.Header().Get("X-Promshim-Served-Candidate"); got != "full_local" {
		t.Fatalf("X-Promshim-Served-Candidate = %q, want full_local", got)
	}
	if got := res.Header().Get("X-Promshim-Selected-Strategy"); got != "native_sql" {
		t.Fatalf("X-Promshim-Selected-Strategy = %q, want native_sql (routing selection preserved)", got)
	}
	after := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "success")
	if after != before+1 {
		t.Fatalf("fallback success counter = %v, want %v", after, before+1)
	}
}

func TestRangeQueryLocalRetryExecutionErrorServesLocalError(t *testing.T) {
	// Range counterpart of the instant local_error case: native execution
	// fails (500), the local retry builds but its selector scan also fails
	// with an execution-class 500, which is the served error (502) and records
	// the local_error outcome.
	server := fallbackStubClickHouseWithLocal(t, http.StatusInternalServerError, http.StatusInternalServerError)
	defer server.Close()
	handler := newFallbackHandler(t, server.URL, "prefer", false)
	before := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "local_error")

	url := "/api/v1/query_range?query=up%20unless%20%28up%20%3D%3D%200%29&start=300&end=600&step=60"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body: %s", res.Code, res.Body.String())
	}
	payload := decodeJSONBody(t, res)
	if payload["errorType"] != "execution" {
		t.Fatalf("errorType = %v, want execution; body: %s", payload["errorType"], res.Body.String())
	}
	if errText, _ := payload["error"].(string); !strings.Contains(errText, "local selector scan rejected") {
		t.Fatalf("expected served error to be the local execution error, got: %v", payload["error"])
	}
	after := fallbackCounterValue(t, "query_range", "prefer", "native_sql", "local_error")
	if after != before+1 {
		t.Fatalf("fallback local_error counter = %v, want %v", after, before+1)
	}
}
