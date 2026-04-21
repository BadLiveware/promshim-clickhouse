package promshim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if body.Data.Plan.Lowering == nil || !body.Data.Plan.Lowering.AggregationPushdownEligible {
		t.Fatalf("expected lowering metadata in explain response, got %#v", body.Data.Plan)
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

func TestQueryRangeExplainBuildsVectorAndRoundPlans(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=vector(0)&start=0&end=300&step=30", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for vector explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success status, got %#v", body)
	}
	if body.Data.Plan.Kind != "vector" || body.Data.Plan.Strategy != "local" {
		t.Fatalf("expected local vector plan, got %#v", body.Data.Plan)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=round(up)&start=0&end=300&step=30", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for round explain, got %d: %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Kind != "round" || body.Data.Plan.Strategy != "local" {
		t.Fatalf("expected local round plan, got %#v", body.Data.Plan)
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

func TestQueryRangeExplainBuildsIncreasePlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=increase(up%5B5m%5D)&start=0&end=300&step=60", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success response, got %#v", body)
	}
	if body.Data.Plan.Kind != "increase" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native increase plan, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainBuildsNativeAggregateOverTimePlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=sum_over_time(up%5B5m%5D)&start=0&end=300&step=60", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success response, got %#v", body)
	}
	if body.Data.Plan.Kind != "sum_over_time" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native sum_over_time plan, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainBuildsNativeCounterRangePlans(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"rate(up%5B5m%5D)", "changes(up%5B5m%5D)", "deriv(up%5B5m%5D)"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query="+query+"&start=0&end=300&step=60", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, res.Code, res.Body.String())
		}

		var body struct {
			Status string `json:"status"`
			Data   struct {
				Plan ExplainNode `json:"plan"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != "success" {
			t.Fatalf("expected success response for %q, got %#v", query, body)
		}
		if body.Data.Plan.Strategy != "native_sql" {
			t.Fatalf("expected native counter range plan for %q, got %#v", query, body.Data.Plan)
		}
	}
}

func TestQueryRangeExplainBuildsNativeRangePlansForSubquery(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"sum_over_time(sum(up)%5B5m%3A1m%5D)", "rate(sum(up)%5B5m%3A1m%5D)", "increase(sum(up)%5B5m%3A1m%5D)", "changes(sum(up)%5B5m%3A1m%5D)"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query="+query+"&start=0&end=300&step=60", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, res.Code, res.Body.String())
		}

		var body struct {
			Status string `json:"status"`
			Data   struct {
				Plan ExplainNode `json:"plan"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != "success" {
			t.Fatalf("expected success response for %q, got %#v", query, body)
		}
		if body.Data.Plan.Strategy != "native_sql" {
			t.Fatalf("expected native subquery-backed range plan for %q, got %#v", query, body.Data.Plan)
		}
	}
}

func TestQueryExplainBuildsIncreaseDeltaIDeltaChangesAndDerivPlansForSubquerySelector(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	testQueries := []string{
		"increase(sum(up)%5B5m%3A%5D)",
		"delta(sum(up)%5B5m%3A%5D)",
		"idelta(sum(up)%5B5m%3A%5D)",
		"changes(sum(up)%5B5m%3A%5D)",
		"deriv(sum(up)%5B5m%3A%5D)",
	}
	for _, query := range testQueries {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query="+query, nil)
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, res.Code, res.Body.String())
		}

		var body struct {
			Status string `json:"status"`
			Data   struct {
				Plan ExplainNode `json:"plan"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != "success" {
			t.Fatalf("expected success response for %q, got %#v", query, body)
		}
		expectStrategy := "local"
		if strings.Contains(query, "increase(") || strings.Contains(query, "delta(") || strings.Contains(query, "idelta(") || strings.Contains(query, "changes(") || strings.Contains(query, "deriv(") {
			expectStrategy = "native_sql"
		}
		if body.Data.Plan.Strategy != expectStrategy {
			t.Fatalf("expected %s plan for %q, got %#v", expectStrategy, query, body.Data.Plan)
		}
		if body.Data.Plan.Kind != "increase" && body.Data.Plan.Kind != "delta" && body.Data.Plan.Kind != "idelta" && body.Data.Plan.Kind != "changes" && body.Data.Plan.Kind != "deriv" {
			t.Fatalf("expected increase/delta/idelta/changes/deriv plan for %q, got %#v", query, body.Data.Plan)
		}
	}
}

func TestQueryExplainBuildsRateAndIratePlansForSubquerySelectors(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	testQueries := []string{
		"rate(sum(up)%5B5m%3A%5D)",
		"irate(sum(up)%5B5m%3A%5D)",
	}
	for _, query := range testQueries {
		url := "/api/v1/query_explain?query=" + query
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, res.Code, res.Body.String())
		}

		var body struct {
			Status string `json:"status"`
			Data   struct {
				Plan ExplainNode `json:"plan"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Status != "success" {
			t.Fatalf("expected success response for %q, got %#v", query, body)
		}
		if body.Data.Plan.Strategy != "native_sql" {
			t.Fatalf("expected native_sql plan for %q, got %#v", query, body.Data.Plan)
		}
		if body.Data.Plan.Kind != "rate" && body.Data.Plan.Kind != "irate" {
			t.Fatalf("expected rate-like plan for %q, got %#v", query, body.Data.Plan)
		}
	}
}

func TestQueryRejectsMalformedHistogramFractionAsBadData(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=histogram_fraction(0.9,sum%20by%20(le,job)%20(up))", nil)
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
	if body.ErrorType != "bad_data" {
		t.Fatalf("expected bad_data error type, got %#v", body)
	}
	if !strings.Contains(body.Error, "histogram_fraction") {
		t.Fatalf("expected histogram_fraction in parse message, got %#v", body)
	}
}

func TestQueryExplainBuildsHistogramFractionPlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=histogram_fraction(0,1,sum%20by%20(le,job)%20(rate(up%5B5m%5D)))", nil)
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success response, got %#v", body)
	}
	if body.Data.Plan.Kind != "histogram_fraction" || body.Data.Plan.Strategy != "local" {
		t.Fatalf("expected local histogram_fraction plan, got %#v", body.Data.Plan)
	}
	if len(body.Data.Plan.Children) != 1 || body.Data.Plan.Children[0].Strategy != "local" {
		t.Fatalf("expected local aggregation child under histogram_fraction, got %#v", body.Data.Plan.Children)
	}
	if len(body.Data.Plan.Children[0].Children) != 1 || body.Data.Plan.Children[0].Children[0].Strategy != "native_sql" {
		t.Fatalf("expected native rate child under histogram_fraction aggregation, got %#v", body.Data.Plan.Children)
	}
}

func TestQueryRejectsParseFailureAsBadData(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=sum(", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorType != "bad_data" {
		t.Fatalf("expected bad_data parse failure, got %#v", body)
	}
	if body.Error == "" {
		t.Fatal("expected parse error message")
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

func TestQueryRangeScalarQueryReturnsConstantMatrix(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?query=1%20%2B%202&start=0&end=120&step=60", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]any           `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success payload, got %#v", body)
	}
	if body.Data.ResultType != "matrix" {
		t.Fatalf("expected matrix result type, got %#v", body.Data)
	}
	if len(body.Data.Result) != 1 {
		t.Fatalf("expected one scalar range series, got %#v", body.Data.Result)
	}
	if len(body.Data.Result[0].Metric) != 0 {
		t.Fatalf("expected empty metric for scalar range result, got %#v", body.Data.Result[0].Metric)
	}
	if len(body.Data.Result[0].Values) != 3 {
		t.Fatalf("expected three step-aligned points, got %#v", body.Data.Result[0].Values)
	}
	for _, point := range body.Data.Result[0].Values {
		if point[1] != "3" {
			t.Fatalf("expected scalar range value 3, got %#v", body.Data.Result[0].Values)
		}
	}
}

func TestQueryRangeRejectsMatrixExpressionType(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range?query=(up%20*%20100)%5B5m%3A30s%5D&start=0&end=120&step=60", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for matrix expression range query, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "invalid expression type") || !strings.Contains(res.Body.String(), "range query") {
		t.Fatalf("expected invalid expression type message, got %s", res.Body.String())
	}
}
