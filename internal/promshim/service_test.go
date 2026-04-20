package promshim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryExplainReturnsNativePlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=sum%20by%20(job)%20(up)&time=300", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Mode string      `json:"mode"`
			Plan ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success status, got %#v", body)
	}
	if body.Data.Mode != "instant" {
		t.Fatalf("expected instant mode, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql plan, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainReturnsLocalFallbackReason(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=sum%20by%20(job)%20(label_join(up,%20%22joined%22,%20%22/%22,%20%22job%22,%20%22namespace%22))&start=0&end=300&step=30"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Mode string      `json:"mode"`
			Plan ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Mode != "range" {
		t.Fatalf("expected range mode, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "local" {
		t.Fatalf("expected local plan, got %#v", body.Data.Plan)
	}
	if body.Data.Plan.Reason == "" {
		t.Fatalf("expected fallback reason, got %#v", body.Data.Plan)
	}
}

func TestQueryExplainRejectsMissingQuery(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status    string `json:"status"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "error" || body.ErrorType != "bad_data" {
		t.Fatalf("unexpected error payload: %#v", body)
	}
}

func TestQueryWithExplainIncludesPlanAndNormalResult(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1%20%2B%202&time=300&explain=1", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []any  `json:"result"`
		} `json:"data"`
		Plan ExplainNode `json:"plan"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success, got %#v", body)
	}
	if body.Data.ResultType != "scalar" {
		t.Fatalf("expected scalar result, got %#v", body.Data)
	}
	if len(body.Data.Result) != 2 || body.Data.Result[1] != "3" {
		t.Fatalf("expected scalar result payload, got %#v", body.Data.Result)
	}
	if body.Plan.Strategy != "local" {
		t.Fatalf("expected local plan, got %#v", body.Plan)
	}
}

func TestQueryRangeWithExplainIncludesPlanAndNormalResult(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?query=1%20%2B%202&start=0&end=120&step=60&explain=true", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string           `json:"resultType"`
			Result     []map[string]any `json:"result"`
		} `json:"data"`
		Plan ExplainNode `json:"plan"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.ResultType != "matrix" {
		t.Fatalf("expected matrix result, got %#v", body.Data)
	}
	if len(body.Data.Result) != 1 {
		t.Fatalf("expected one series, got %#v", body.Data.Result)
	}
	if body.Plan.Strategy != "local" {
		t.Fatalf("expected local plan, got %#v", body.Plan)
	}
}
