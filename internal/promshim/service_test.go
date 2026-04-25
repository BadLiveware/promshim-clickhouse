package promshim

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

func TestQueryExplainReturnsDelegatedWholeQueryPlanWhenClassifierAllowsIt(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=up%7Bjob%3D%22api%22%7D&time=300", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Mode string            `json:"mode"`
			Plan local.ExplainNode `json:"plan"`
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
	if body.Data.Plan.Strategy != "delegated_promql" {
		t.Fatalf("expected delegated_promql plan, got %#v", body.Data.Plan)
	}
	if body.Data.Plan.Lowering == nil {
		t.Fatalf("expected lowering metadata in explain response, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainReturnsNativeAggregationForLabelMutation(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
			Mode string            `json:"mode"`
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Mode != "range" {
		t.Fatalf("expected range mode, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "native_sql" || body.Data.Plan.Kind != "aggregation" {
		t.Fatalf("expected native aggregation plan, got %#v", body.Data.Plan)
	}
	if len(body.Data.Plan.Children) != 1 || body.Data.Plan.Children[0].Strategy != "native_sql_expression" {
		t.Fatalf("expected native label-mutation child explain, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainBuildsClampPlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=clamp_min(up,scalar(sum(up)))&start=0&end=300&step=30", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for clamp explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success status, got %#v", body)
	}
	if body.Data.Plan.Kind != "clamp_min" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native clamp_min plan, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainBuildsVectorAndRoundPlans(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success status, got %#v", body)
	}
	if body.Data.Plan.Kind != "vector" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native vector plan, got %#v", body.Data.Plan)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=sort_by_label(up,%22job%22)&start=0&end=300&step=30", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for sort explain, got %d: %s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Kind != "sort_by_label" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native sort plan, got %#v", body.Data.Plan)
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
	if body.Data.Plan.Kind != "round" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native round plan, got %#v", body.Data.Plan)
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
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
			Plan local.ExplainNode `json:"plan"`
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
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
			Plan local.ExplainNode `json:"plan"`
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
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"rate(up%5B5m%5D)", "changes(up%5B5m%5D)", "deriv(up%5B5m%5D)", "quantile_over_time(0.95,up%5B5m%5D)", "first_over_time(up%5B5m%5D)", "ts_of_first_over_time(up%5B5m%5D)", "ts_of_last_over_time(up%5B5m%5D)", "ts_of_max_over_time(up%5B5m%5D)", "ts_of_min_over_time(up%5B5m%5D)"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query="+query+"&start=0&end=300&step=60", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, res.Code, res.Body.String())
		}

		var body struct {
			Status string `json:"status"`
			Data   struct {
				Plan local.ExplainNode `json:"plan"`
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
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"sum_over_time(sum(up)%5B5m%3A1m%5D)", "rate(sum(up)%5B5m%3A1m%5D)", "increase(sum(up)%5B5m%3A1m%5D)", "changes(sum(up)%5B5m%3A1m%5D)", "quantile_over_time(0.95,sum(up)%5B5m%3A1m%5D)", "first_over_time(sum(up)%5B5m%3A1m%5D)", "ts_of_first_over_time(sum(up)%5B5m%3A1m%5D)", "ts_of_last_over_time(sum(up)%5B5m%3A1m%5D)", "ts_of_max_over_time(sum(up)%5B5m%3A1m%5D)", "ts_of_min_over_time(sum(up)%5B5m%3A1m%5D)"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query="+query+"&start=0&end=300&step=60", nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, res.Code, res.Body.String())
		}

		var body struct {
			Status string `json:"status"`
			Data   struct {
				Plan local.ExplainNode `json:"plan"`
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
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
				Plan local.ExplainNode `json:"plan"`
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
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
				Plan local.ExplainNode `json:"plan"`
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

func TestQueryExplainBuildsInstantMaxOverTimeSubqueryRowsFastPath(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=max_over_time(sum%20by%20(job)%20(rate(demo_cpu_usage_seconds_total%5B1m%5D))%5B5m%3A30s%5D)", nil)
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success response, got %#v", body)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql plan, got %#v", body.Data.Plan)
	}
	if !strings.Contains(body.Data.Plan.RenderedSQL, "maxIf(value, NOT isNaN(value))") {
		t.Fatalf("expected instant max_over_time subquery explain to use row fast path, got %q", body.Data.Plan.RenderedSQL)
	}
	if !strings.Contains(body.Data.Plan.RenderedSQL, "fromUnixTimestamp64Milli(") || !strings.Contains(body.Data.Plan.RenderedSQL, " AS timestamp") {
		t.Fatalf("expected instant max_over_time subquery explain to stamp the evaluation time directly, got %q", body.Data.Plan.RenderedSQL)
	}
	if strings.Contains(body.Data.Plan.RenderedSQL, "max(timestamp) AS timestamp") {
		t.Fatalf("expected instant max_over_time subquery explain to avoid redundant max(timestamp) aggregation, got %q", body.Data.Plan.RenderedSQL)
	}
	if strings.Contains(body.Data.Plan.RenderedSQL, "SELECT tags AS final_tags, timestamp AS timestamp, value AS value FROM (") {
		t.Fatalf("expected instant max_over_time subquery explain to aggregate directly over source rows without an extra prepared subquery, got %q", body.Data.Plan.RenderedSQL)
	}
	if strings.Contains(body.Data.Plan.RenderedSQL, "groupArray((timestamp, value))) AS time_series") {
		t.Fatalf("expected instant max_over_time subquery explain to avoid time_series materialization, got %q", body.Data.Plan.RenderedSQL)
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

func TestQueryExplainBuildsHistogramProjectionNativePlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	for _, fn := range []string{"histogram_count", "histogram_sum", "histogram_avg"} {
		t.Run(fn, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query="+fn+"(http_request_duration_seconds_bucket%7Bjob%3D%22api%22%7D)", nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)

			if res.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
			}
			var body struct {
				Status string `json:"status"`
				Data   struct {
					Plan local.ExplainNode `json:"plan"`
				} `json:"data"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.Plan.Kind != fn || body.Data.Plan.Strategy != "native_sql" {
				t.Fatalf("expected native %s plan, got %#v", fn, body.Data.Plan)
			}
			if len(body.Data.Plan.Children) != 1 || body.Data.Plan.Children[0].Kind != "leaf" {
				t.Fatalf("expected leaf child under %s, got %#v", fn, body.Data.Plan.Children)
			}
		})
	}
}

func TestQueryExplainBuildsHistogramQuantileNativePlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=histogram_quantile(0.9,sum%20by%20(le,job)%20(rate(http_request_duration_seconds_bucket%5B5m%5D)))", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Kind != "histogram_quantile" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native histogram_quantile plan, got %#v", body.Data.Plan)
	}
	if len(body.Data.Plan.Children) != 1 || body.Data.Plan.Children[0].Kind != "aggregation" {
		t.Fatalf("expected aggregation child under histogram_quantile, got %#v", body.Data.Plan.Children)
	}
}

func TestQueryExplainBuildsHistogramQuantilesPlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=histogram_quantiles(sum%20by%20(le,job)%20(rate(up%5B5m%5D)),%22quantile%22,0.5,scalar(sum(up)))", nil)
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success response, got %#v", body)
	}
	if body.Data.Plan.Kind != "histogram_quantiles" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native histogram_quantiles plan, got %#v", body.Data.Plan)
	}
}

func TestQueryExplainBuildsHistogramFractionPlan(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "success" {
		t.Fatalf("expected success response, got %#v", body)
	}
	if body.Data.Plan.Kind != "histogram_fraction" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native histogram_fraction plan, got %#v", body.Data.Plan)
	}
	if len(body.Data.Plan.Children) != 1 || body.Data.Plan.Children[0].Kind != "aggregation" {
		t.Fatalf("expected aggregation child under histogram_fraction, got %#v", body.Data.Plan.Children)
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
		Plan local.ExplainNode `json:"plan"`
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
		Plan local.ExplainNode `json:"plan"`
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

func TestQueryExplainReportsEntireQueryDelegationEligibility(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", ClickHouseVersion: "26.3"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=up%7Bjob%3D%22api%22%7D", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Data struct {
			EntireQueryDelegation struct {
				Eligible          bool   `json:"eligible"`
				ClickHouseVersion string `json:"clickHouseVersion"`
			} `json:"entireQueryDelegation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.EntireQueryDelegation.Eligible || body.Data.EntireQueryDelegation.ClickHouseVersion != "26.3" {
		t.Fatalf("expected delegation eligibility in explain response, got %#v", body.Data.EntireQueryDelegation)
	}
}

func TestQueryExplainReportsScalarRootAsNotEligibleForEntireQueryDelegation(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", ClickHouseVersion: "26.3"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=1%20%2B%202", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Data struct {
			EntireQueryDelegation struct {
				Eligible bool   `json:"eligible"`
				Reason   string `json:"reason"`
			} `json:"entireQueryDelegation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.EntireQueryDelegation.Eligible || !strings.Contains(body.Data.EntireQueryDelegation.Reason, "scalar-only roots") {
		t.Fatalf("expected scalar-only root query to be reported ineligible, got %#v", body.Data.EntireQueryDelegation)
	}
}

func TestQueryExplainModeReturnsPlanWithoutExplainFlag(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", NativeLoweringMode: local.NativeLoweringModePrefer})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1%20%2B%202&time=300&native_lowering_mode=explain", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status             string            `json:"status"`
		NativeLoweringMode string            `json:"nativeLoweringMode"`
		Plan               local.ExplainNode `json:"plan"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NativeLoweringMode != string(local.NativeLoweringModeExplain) {
		t.Fatalf("expected explain native lowering mode, got %#v", body)
	}
	if body.Plan.Strategy != "local" {
		t.Fatalf("expected local plan for scalar query, got %#v", body.Plan)
	}
}

func TestMetricsEndpointExportsShadowRolloutMetrics(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	queryReq := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1%20%2B%202&time=300&native_lowering_mode=shadow&explain=1", nil)
	queryRes := httptest.NewRecorder()
	handler.ServeHTTP(queryRes, queryReq)
	if queryRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from shadow query, got %d: %s", queryRes.Code, queryRes.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRes := httptest.NewRecorder()
	handler.ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from metrics endpoint, got %d: %s", metricsRes.Code, metricsRes.Body.String())
	}

	metricsBody := metricsRes.Body.String()
	for _, fragment := range []string{
		"# HELP promshim_shadow_comparisons_total",
		"# TYPE promshim_shadow_comparisons_total counter",
		`promshim_shadow_comparisons_total{category="match",compare_mode="exact",status="match"} 1`,
		"# HELP promshim_shadow_duration_milliseconds",
		"# TYPE promshim_shadow_duration_milliseconds histogram",
		`promshim_shadow_duration_milliseconds_bucket{compare_mode="exact",path="served",phase="plan",status="match",le="1"}`,
		`promshim_shadow_duration_milliseconds_bucket{compare_mode="exact",path="shadow",phase="eval",status="match",le="1"}`,
	} {
		if !strings.Contains(metricsBody, fragment) {
			t.Fatalf("expected metrics body to contain %q, got %s", fragment, metricsBody)
		}
	}
}

func TestQueryShadowModeReturnsServedPlanAndShadowReport(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1%20%2B%202&time=300&native_lowering_mode=shadow&explain=1", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status             string            `json:"status"`
		NativeLoweringMode string            `json:"nativeLoweringMode"`
		Plan               local.ExplainNode `json:"plan"`
		Shadow             struct {
			ServedStrategy   string `json:"servedStrategy"`
			ShadowStrategy   string `json:"shadowStrategy"`
			CompareMode      string `json:"compareMode"`
			Status           string `json:"status"`
			ServedPlanMillis int64  `json:"servedPlanMillis"`
			ServedEvalMillis int64  `json:"servedEvalMillis"`
			ShadowPlanMillis int64  `json:"shadowPlanMillis"`
			ShadowEvalMillis int64  `json:"shadowEvalMillis"`
		} `json:"shadow"`
		ShadowSummary struct {
			Total             int64            `json:"total"`
			ByStatus          map[string]int64 `json:"byStatus"`
			ByCategory        map[string]int64 `json:"byCategory"`
			TotalServedPlanMs int64            `json:"totalServedPlanMs"`
			TotalServedEvalMs int64            `json:"totalServedEvalMs"`
			TotalShadowPlanMs int64            `json:"totalShadowPlanMs"`
			TotalShadowEvalMs int64            `json:"totalShadowEvalMs"`
		} `json:"shadowSummary"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NativeLoweringMode != string(local.NativeLoweringModeShadow) {
		t.Fatalf("expected shadow mode, got %#v", body)
	}
	if body.Plan.Strategy != "local" {
		t.Fatalf("expected served local plan in shadow mode, got %#v", body.Plan)
	}
	if body.Shadow.Status != "match" {
		t.Fatalf("expected shadow match for scalar query, got %#v", body.Shadow)
	}
	if body.Shadow.ServedPlanMillis < 0 || body.Shadow.ServedEvalMillis < 0 || body.Shadow.ShadowPlanMillis < 0 || body.Shadow.ShadowEvalMillis < 0 {
		t.Fatalf("expected non-negative shadow timing fields, got %#v", body.Shadow)
	}
	if body.ShadowSummary.Total != 1 || body.ShadowSummary.ByStatus["match"] != 1 || body.ShadowSummary.ByCategory["match"] != 1 {
		t.Fatalf("expected shadow summary counts, got %#v", body.ShadowSummary)
	}
	if body.ShadowSummary.TotalServedPlanMs < 0 || body.ShadowSummary.TotalServedEvalMs < 0 || body.ShadowSummary.TotalShadowPlanMs < 0 || body.ShadowSummary.TotalShadowEvalMs < 0 {
		t.Fatalf("expected non-negative shadow summary timing totals, got %#v", body.ShadowSummary)
	}
}

func TestQueryExplainHonorsNativeLoweringModeOff(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", NativeLoweringMode: local.NativeLoweringModePrefer})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=sum%20by%20(job)%20(up)&native_lowering_mode=off", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var body struct {
		Status string `json:"status"`
		Data   struct {
			NativeLoweringMode string            `json:"nativeLoweringMode"`
			Plan               local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.NativeLoweringMode != string(local.NativeLoweringModeOff) {
		t.Fatalf("expected off mode in explain response, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "local" {
		t.Fatalf("expected local root when native lowering is off, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainForceSupportedAllowsAnchoredAggregationRoot(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=sum%20by%20(job)%20(up%20%40%20start())&start=0&end=300&step=30&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for anchored force_supported range request, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			NativeLoweringMode string            `json:"nativeLoweringMode"`
			Plan               local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.NativeLoweringMode != string(local.NativeLoweringModeForceSupported) {
		t.Fatalf("expected force_supported mode in explain response, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql plan for anchored range explain, got %#v", body.Data.Plan)
	}
}

func TestQueryExplainForceSupportedPrefersNativeRootOverDelegation(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_explain?query=up&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for native force_supported selector explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql root under force_supported, got %#v", body.Data.Plan)
	}
}

func TestQueryExplainForceSupportedAcceptsScalarLiteralNativeRoot(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_explain?query=42&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for scalar-literal force_supported explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql root for scalar literal under force_supported, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainForceSupportedPrefersNativeRootOverDelegation(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=up&start=0&end=300&step=30&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for range native force_supported selector explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql root under range force_supported, got %#v", body.Data.Plan)
	}
}

func TestQueryExplainForceSupportedAcceptsAbsentNativeRoot(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_explain?query=absent(up%7Bjob%3D%22api%22%7D)&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for force_supported absent explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" || body.Data.Plan.Kind != "absent" {
		t.Fatalf("expected native absent root under force_supported, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainForceSupportedAcceptsAbsentOverTimeNativeRoot(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=absent_over_time(up%7Bjob%3D%22api%22%7D%5B5m%5D)&start=0&end=300&step=30&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for force_supported absent_over_time range explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" || body.Data.Plan.Kind != "absent_over_time" {
		t.Fatalf("expected native absent_over_time root under force_supported, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainForceSupportedAcceptsRegexInfoNativeRoot(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=info(up,%20%7B__name__%3D~%22.%2B_info%22%7D)&start=0&end=300&step=30&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for force_supported regex info() range explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" || body.Data.Plan.Kind != "info" {
		t.Fatalf("expected native info root under force_supported, got %#v", body.Data.Plan)
	}
}

func TestQueryExplainForceSupportedAcceptsNativeLabelJoinRoot(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_explain?query=label_join(up,%20%22joined%22,%20%22/%22,%20%22job%22,%20%22namespace%22)&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for native label_join root, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Plan.Strategy != "native_sql" || body.Data.Plan.Kind != "label_join" {
		t.Fatalf("expected native label_join plan, got %#v", body.Data.Plan)
	}
}

func TestQueryRejectsInvalidNativeLoweringMode(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1&native_lowering_mode=definitely_not_real", nil)
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
		t.Fatalf("expected bad_data for invalid mode, got %#v", body)
	}
	if !strings.Contains(body.Error, "native lowering mode") {
		t.Fatalf("expected native lowering mode error, got %#v", body)
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

func TestQueryHeadersIncludeStrictRoutingMetadata(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1&routing_policy=cost_shadow", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	checks := map[string]string{
		"X-Promshim-Routing-Policy":    "cost_shadow",
		"X-Promshim-Routing-Decision":  "strict_low_confidence",
		"X-Promshim-Strict-Strategy":   "local",
		"X-Promshim-Selected-Strategy": "local",
		"X-Promshim-Routing-Reason":    "family_not_local_candidate",
		"X-Promshim-Cost-Family":       "scalar",
	}
	for key, want := range checks {
		if got := res.Header().Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestQueryRejectsInvalidRoutingPolicy(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1&routing_policy=bogus", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "unsupported routing policy") {
		t.Fatalf("expected routing policy error, got %s", res.Body.String())
	}
}

func TestNativeLoweringOffIgnoresCostRoutingPolicy(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query?query=1&native_lowering_mode=off&routing_policy=cost_shadow", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("X-Promshim-Routing-Policy"); got != "strict" {
		t.Fatalf("routing policy = %q, want strict", got)
	}
	if got := res.Header().Get("X-Promshim-Routing-Reason"); got != "native_lowering_mode_ignores_cost_routing" {
		t.Fatalf("routing reason = %q", got)
	}
}

func TestQueryExplainIncludesRoutingCostClass(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=rate(up%5B5m%5D)&time=300&routing_policy=cost_prefer", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Routing struct {
				Policy   string `json:"policy"`
				Decision string `json:"decision"`
				Class    struct {
					Family           string `json:"family"`
					SelectorCount    int    `json:"selectorCount"`
					HasRangeFunction bool   `json:"hasRangeFunction"`
					LookbackMS       int64  `json:"lookbackMs"`
				} `json:"class"`
			} `json:"routing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Routing.Policy != "cost_prefer" || body.Data.Routing.Decision != "strict_missing_estimate" {
		t.Fatalf("unexpected routing: %#v", body.Data.Routing)
	}
	if body.Data.Routing.Class.Family != "rate" || !body.Data.Routing.Class.HasRangeFunction || body.Data.Routing.Class.SelectorCount != 1 {
		t.Fatalf("unexpected cost class: %#v", body.Data.Routing.Class)
	}
	if body.Data.Routing.Class.LookbackMS != int64((5 * time.Minute).Milliseconds()) {
		t.Fatalf("lookback = %d", body.Data.Routing.Class.LookbackMS)
	}
}

func TestQueryCostClassUsesCachedSelectorEstimates(t *testing.T) {
	service := &queryService{selectorStats: newSelectorStatsCache(time.Minute)}
	eval := time.Unix(300, 0).UTC()
	expr, err := logical.ParseExpression(`up{job="api"}`)
	if err != nil {
		t.Fatal(err)
	}
	sigs := extractSelectorSignatures(expr, queryCostTiming{Endpoint: "query", Start: eval, End: eval})
	service.selectorStats.put(sigs[0], selectorStats{MatchedSeries: 4, SamplesPerSeries: 2, ObservedAt: time.Now().UTC()})
	class := service.queryCostClass(`up{job="api"}`, queryCostTiming{Endpoint: "query", Start: eval, End: eval}, "native_sql")
	if class.EstimatedSeries != 4 || class.EstimatedInputSamples != 8 || class.EstimatedOutputPoints != 4 {
		t.Fatalf("unexpected cached estimates: %+v", class)
	}
}

func TestRoutingMissingEstimateMetricExposed(t *testing.T) {
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=up&time=300", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("query_explain status = %d: %s", res.Code, res.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRes := httptest.NewRecorder()
	handler.ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", metricsRes.Code)
	}
	if !strings.Contains(metricsRes.Body.String(), "promshim_routing_estimate_missing_total") {
		t.Fatalf("routing missing estimate metric not exposed:\n%s", metricsRes.Body.String())
	}
}
