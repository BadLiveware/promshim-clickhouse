package promshim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/rules"
)

func TestApplyQueryLimitTruncatesSeriesResults(t *testing.T) {
	value, apiErr := applyQueryLimit(model.VectorValue{Samples: []model.InstantSample{{}, {}, {}}}, "2")
	if apiErr != nil {
		t.Fatalf("unexpected limit error: %v", apiErr)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("vector samples = %d, want 2", len(vector.Samples))
	}

	value, apiErr = applyQueryLimit(model.MatrixValue{Series: []model.RangeSeries{{}, {}, {}}}, "1")
	if apiErr != nil {
		t.Fatalf("unexpected limit error: %v", apiErr)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 1 {
		t.Fatalf("matrix series = %d, want 1", len(matrix.Series))
	}
}

func TestApplyQueryLimitRejectsInvalidLimit(t *testing.T) {
	_, apiErr := applyQueryLimit(model.VectorValue{}, "-1")
	if apiErr == nil || apiErr.StatusCode != http.StatusBadRequest || apiErr.ErrorType != "bad_data" {
		t.Fatalf("apiErr = %#v, want bad_data", apiErr)
	}
}

func TestPromotedTagColumnHelpersMergeExplicitAndDiscovered(t *testing.T) {
	explicit := promotedTagColumnSet([]string{"instance", "pod", "instance"})
	discovered := map[string]struct{}{"node": {}, "pod": {}}
	merged := mergePromotedTagColumns(explicit, discovered)
	for _, label := range []string{"instance", "pod", "node"} {
		if _, ok := merged[label]; !ok {
			t.Fatalf("expected merged promoted tag columns to include %q, got %#v", label, merged)
		}
	}
	if len(merged) != 3 {
		t.Fatalf("unexpected merged promoted tag columns: %#v", merged)
	}
}

func TestPromotedTagColumnHelpersIgnoreEmptyNames(t *testing.T) {
	got := promotedTagColumnSet([]string{"", "instance", ""})
	if len(got) != 1 {
		t.Fatalf("unexpected promoted tag columns: %#v", got)
	}
	if _, ok := got["instance"]; !ok {
		t.Fatalf("expected instance in promoted tag columns: %#v", got)
	}
}

func TestQueryExplainExpandsVirtualRecordingRule(t *testing.T) {
	ruleFile := writeServiceRulesFile(t, `groups:
- name: dashboard
  labels:
    source: rules
  rules:
  - record: job:http_requests:rate5m
    expr: sum by (job) (rate(http_requests_total[5m]))
    labels:
      team: edge
`)
	handler, err := NewHandler(Options{ClickHouseEndpoint: "http://127.0.0.1:8123/", RecordingRuleMode: "virtual", RecordingRuleFiles: []string{ruleFile}, DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=job:http_requests:rate5m%7Bjob%3D%22api%22,source%3D%22rules%22%7D&time=300", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Data struct {
			RecordingRules []struct {
				Record string `json:"record"`
				Mode   string `json:"mode"`
			} `json:"recordingRules"`
			Plan local.ExplainNode `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.RecordingRules) != 1 || body.Data.RecordingRules[0].Record != "job:http_requests:rate5m" || body.Data.RecordingRules[0].Mode != "instant_virtual" {
		t.Fatalf("recordingRules = %#v", body.Data.RecordingRules)
	}
	if !strings.Contains(body.Data.Plan.Expr, "http_requests_total") {
		t.Fatalf("plan expr = %q, want expanded rule expression", body.Data.Plan.Expr)
	}
}

func TestLoadRecordingRuleRegistryRejectsMissingExplicitFile(t *testing.T) {
	path := t.TempDir() + "/rules.yaml"
	_, err := loadRecordingRuleRegistry(rules.ModeVirtual, []string{path})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want missing explicit file error", err)
	}
}

func TestLoadRecordingRuleRegistryAllowsEmptySidecarGlob(t *testing.T) {
	pattern := t.TempDir() + "/*.yaml"
	registry, err := loadRecordingRuleRegistry(rules.ModeVirtual, []string{pattern})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Len() != 0 {
		t.Fatalf("registry length = %d, want 0", registry.Len())
	}
}

func TestReloadRecordingRulesSwapsValidRegistry(t *testing.T) {
	ruleFile := writeServiceRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: demo:recorded
    expr: vector(1)
`)
	registry, err := loadRecordingRuleRegistry(rules.ModeVirtual, []string{ruleFile})
	if err != nil {
		t.Fatal(err)
	}
	service := &queryService{recordingRuleMode: rules.ModeVirtual, opts: Options{RecordingRuleFiles: []string{ruleFile}}}
	service.recordingRules.Store(registry)

	if err := os.WriteFile(ruleFile, []byte(`groups:
- name: dashboard
  rules:
  - record: demo:recorded
    expr: vector(2)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.reloadRecordingRulesOnce(); err != nil {
		t.Fatal(err)
	}
	expr, err := logical.ParseExpression("demo:recorded")
	if err != nil {
		t.Fatal(err)
	}
	expanded, expansions, apiErr := service.expandRecordingRules(expr)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(expansions) != 1 || !strings.Contains(expanded.String(), "vector(2)") {
		t.Fatalf("expanded=%s expansions=%#v", expanded.String(), expansions)
	}
	if service.recordingRuleReloadSuccess.Load() != 1 || service.recordingRuleReloadErrors.Load() != 0 {
		t.Fatalf("success=%d errors=%d", service.recordingRuleReloadSuccess.Load(), service.recordingRuleReloadErrors.Load())
	}
}

func TestReloadRecordingRulesKeepsLastGoodOnInvalidFile(t *testing.T) {
	ruleFile := writeServiceRulesFile(t, `groups:
- name: dashboard
  rules:
  - record: demo:recorded
    expr: vector(1)
`)
	registry, err := loadRecordingRuleRegistry(rules.ModeVirtual, []string{ruleFile})
	if err != nil {
		t.Fatal(err)
	}
	service := &queryService{recordingRuleMode: rules.ModeVirtual, opts: Options{RecordingRuleFiles: []string{ruleFile}}}
	service.recordingRules.Store(registry)

	if err := os.WriteFile(ruleFile, []byte(`groups:
- name: dashboard
  rules:
  - record: demo:recorded
    expr: sum(
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.reloadRecordingRulesOnce(); err == nil {
		t.Fatal("expected invalid reload to fail")
	}
	expr, err := logical.ParseExpression("demo:recorded")
	if err != nil {
		t.Fatal(err)
	}
	expanded, expansions, apiErr := service.expandRecordingRules(expr)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(expansions) != 1 || !strings.Contains(expanded.String(), "vector(1)") {
		t.Fatalf("expanded=%s expansions=%#v", expanded.String(), expansions)
	}
	if service.recordingRuleReloadSuccess.Load() != 0 || service.recordingRuleReloadErrors.Load() != 1 {
		t.Fatalf("success=%d errors=%d", service.recordingRuleReloadSuccess.Load(), service.recordingRuleReloadErrors.Load())
	}
}

func writeServiceRulesFile(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/rules.yaml"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestQueryExplainReturnsDelegatedWholeQueryPlanWhenClassifierAllowsIt(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
			Mode                      string                `json:"mode"`
			ClickHouseSettingsProfile struct{ Name string } `json:"clickHouseSettingsProfile"`
			Plan                      local.ExplainNode     `json:"plan"`
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
	if got := res.Header().Get("X-Promshim-Settings-Profile"); got != "default_safe" {
		t.Fatalf("settings profile header = %q, want default_safe", got)
	}
	if body.Data.ClickHouseSettingsProfile.Name != "default_safe" || body.Data.Plan.SettingsProfile == nil || body.Data.Plan.SettingsProfile.Name != "default_safe" {
		t.Fatalf("expected default_safe settings provenance, got body=%#v plan=%#v", body.Data.ClickHouseSettingsProfile, body.Data.Plan.SettingsProfile)
	}
}

func TestQueryRangeExplainReturnsNativeAggregationForLabelMutation(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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

func TestQueryRangeExplainIncludesPhysicalDecisions(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=avg_over_time(up%5B1h%5D)&start=0&end=10800&step=3600&native_lowering_mode=force_supported"
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
	if body.Data.Mode != "range" || body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native range explain, got %#v", body.Data)
	}
	decision, ok := findExplainPhysicalDecision(body.Data.Plan.PhysicalDecisions, "range_window_aggregate")
	if !ok {
		t.Fatalf("expected range-window physical decision in API response, got %#v", body.Data.Plan.PhysicalDecisions)
	}
	if decision.Strategy != string(physical.RangeWindowAggregateStrategySparseDirectAggregate) {
		t.Fatalf("physical strategy = %q, want %q; decisions=%#v", decision.Strategy, physical.RangeWindowAggregateStrategySparseDirectAggregate, body.Data.Plan.PhysicalDecisions)
	}
}

func TestQueryRangeExplainUsesRuntimePhysicalOptions(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true, CumulativeAvgOverTime: "prefer"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=avg_over_time(up%5B1h%5D)&start=0&end=3600&step=60&native_lowering_mode=force_supported"
	req := httptest.NewRequest(http.MethodGet, url, nil)
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
	decision, ok := findExplainPhysicalDecision(body.Data.Plan.PhysicalDecisions, "range_window_aggregate")
	if !ok {
		t.Fatalf("expected range-window physical decision in API response, got %#v", body.Data.Plan.PhysicalDecisions)
	}
	if decision.Strategy != string(physical.RangeWindowAggregateStrategyCumulativeAvg) {
		t.Fatalf("physical strategy = %q, want %q; decisions=%#v", decision.Strategy, physical.RangeWindowAggregateStrategyCumulativeAvg, body.Data.Plan.PhysicalDecisions)
	}
	if !strings.Contains(body.Data.Plan.RenderedSQL, "ARRAY JOIN [(1, upper_bound), (0, lower_prev_bound)] AS boundary") {
		t.Fatalf("expected explain rendered SQL to use cumulative boundary pivot, got %q", body.Data.Plan.RenderedSQL)
	}
}

func findExplainPhysicalDecision(decisions []physical.Decision, kind string) (physical.Decision, bool) {
	for _, decision := range decisions {
		if decision.Kind == kind {
			return decision, true
		}
	}
	return physical.Decision{}, false
}

func findExplainNodeByKind(node local.ExplainNode, kind string) (local.ExplainNode, bool) {
	if node.Kind == kind {
		return node, true
	}
	for _, child := range node.Children {
		if found, ok := findExplainNodeByKind(child, kind); ok {
			return found, true
		}
	}
	return local.ExplainNode{}, false
}

func TestQueryRangeExplainIncludesSubqueryNodeThreadPreferenceDecision(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name                 string
		url                  string
		wantRootQuerySetting bool
	}{
		{
			name:                 "pure subquery rate keeps root-level no-thread-cap",
			url:                  "/api/v1/query_range_explain?query=rate((sum%20by%20(job)%20(up))%5B30m:1m%5D)&start=0&end=10800&step=30&native_lowering_mode=force_supported",
			wantRootQuerySetting: true,
		},
		{
			name:                 "mixed binary root keeps no-thread-cap on subquery branch",
			url:                  "/api/v1/query_range_explain?query=sum(avg_over_time(up%5B1h%5D))%20%2B%20sum(rate((sum%20by%20(job)%20(up))%5B5m:1m%5D))&start=0&end=10800&step=30&native_lowering_mode=force_supported",
			wantRootQuerySetting: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
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
			if body.Data.Mode != "range" || body.Data.Plan.Strategy != "native_sql" {
				t.Fatalf("expected native range explain, got %#v", body.Data)
			}

			if _, ok := findExplainPhysicalDecision(body.Data.Plan.PhysicalDecisions, "query_settings"); ok != tt.wantRootQuerySetting {
				t.Fatalf("root query_settings presence=%v, want %v; root decisions=%#v", ok, tt.wantRootQuerySetting, body.Data.Plan.PhysicalDecisions)
			}

			subquery, ok := findExplainNodeByKind(body.Data.Plan, "subquery")
			if !ok {
				t.Fatalf("expected subquery node, got %#v", body.Data.Plan)
			}
			decision, ok := findExplainPhysicalDecision(subquery.PhysicalDecisions, "query_settings")
			if !ok {
				t.Fatalf("expected subquery query_settings decision, got %#v", subquery.PhysicalDecisions)
			}
			if decision.Strategy != "no_thread_cap" {
				t.Fatalf("subquery query_settings strategy = %q, want no_thread_cap; decisions=%#v", decision.Strategy, subquery.PhysicalDecisions)
			}
			if decision.Reason != physical.ThreadPreferenceReasonSubqueryRateRows {
				t.Fatalf("subquery query_settings reason = %q, want %q", decision.Reason, physical.ThreadPreferenceReasonSubqueryRateRows)
			}
		})
	}
}

func TestQueryRangeExplainBuildsClampPlan(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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

func TestBuildRangePlanForceSupportedAllowsChunkedNativeRoot(t *testing.T) {
	service := &queryService{opts: Options{
		Database:                           "observability",
		Table:                              "prometheus",
		ClickHouseVersion:                  "26.3",
		NativeLoweringMode:                 local.NativeLoweringModeForceSupported,
		NativeGridFunctions:                "prefer",
		CumulativeAvgOverTime:              "prefer",
		MaxRangePointsPerSeries:            local.DefaultMaxRangePointsPerSeries,
		RangeChunkPointsPerSeries:          local.DefaultRangeChunkPointsPerSeries,
		NativeRangeChunkPointsPerSeries:    local.DefaultNativeRangeChunkPointsPerSeries,
		NativeRangeChunkMaxDuration:        local.DefaultNativeRangeChunkMaxDuration,
		NativeRangeChunkMaxChunks:          local.DefaultNativeRangeChunkMaxChunks,
		NativeRangePreflightTimeout:        local.DefaultNativeRangePreflightTimeout,
		NativeRangePreflightMaxMemoryUsage: local.DefaultNativeRangePreflightMaxMemoryUsage,
	}}

	_, _, _, _, plan, analysis, _, apiErr := service.buildRangePlan(context.Background(), httpapi.RangeQueryRequest{
		Query: "avg_over_time(demo_memory_usage_bytes[1m])",
		Start: "2026-04-21T21:35:42Z",
		End:   "2026-04-21T21:45:42Z",
		Step:  "10s",
	}, false)
	if apiErr != nil {
		t.Fatalf("buildRangePlan returned API error: %#v", apiErr)
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	if explain.Strategy != "chunked_native" {
		t.Fatalf("strategy = %q, want chunked_native", explain.Strategy)
	}
	if len(explain.Children) != 1 || explain.Children[0].Strategy != "native_sql" {
		t.Fatalf("chunked native plan should wrap native_sql child, got %#v", explain)
	}
}

func TestMetadataResponseLimitErrors(t *testing.T) {
	if err := enforceMetadataItemLimit("label names", 3, 2); err == nil || !local.IsBadDataError(err) || !strings.Contains(err.Error(), "more than 2 label names") {
		t.Fatalf("expected label-name limit bad_data error, got %v", err)
	}
	if err := enforceMetadataItemLimit("series", 2, 2); err != nil {
		t.Fatalf("expected equal-to-limit metadata result to pass, got %v", err)
	}
}

func TestQueryRangeRejectsNonPositiveStep(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	for _, step := range []string{"0", "-30"} {
		t.Run(step, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/query_range_explain?query=up&start=0&end=300&step="+step, nil)
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
			if body.Status != "error" || body.ErrorType != "bad_data" || !strings.Contains(body.Error, "step must be greater than zero") {
				t.Fatalf("unexpected error payload: %#v", body)
			}
		})
	}
}

func TestQueryRangeExplainBuildsIncreasePlan(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", ClickHouseVersion: "26.3"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", ClickHouseVersion: "26.3"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", NativeLoweringMode: local.NativeLoweringModePrefer})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", NativeLoweringMode: local.NativeLoweringModePrefer})
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

func TestQueryRangeExplainLocalPushdownForcesLocalRootWithNativeChild(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=sum%20by%20(job)%20(rate(up%5B5m%5D))&start=0&end=300&step=30&native_lowering_mode=local_pushdown"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for local_pushdown range explain, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			NativeLoweringMode string            `json:"nativeLoweringMode"`
			Plan               local.ExplainNode `json:"plan"`
			Routing            struct {
				CandidateDecision struct {
					ServedCandidate string `json:"servedCandidate"`
				} `json:"candidateDecision"`
			} `json:"routing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.NativeLoweringMode != string(local.NativeLoweringModeLocalPushdown) {
		t.Fatalf("expected local_pushdown mode in explain response, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "local" || len(body.Data.Plan.Children) != 1 || body.Data.Plan.Children[0].Strategy != "native_sql" {
		t.Fatalf("expected local root with native child, got %#v", body.Data.Plan)
	}
	if body.Data.Routing.CandidateDecision.ServedCandidate != "local_pushdown" {
		t.Fatalf("expected served local_pushdown candidate, got %#v", body.Data.Routing.CandidateDecision)
	}
}

func TestQueryRangeExplainPreferUsesNativeAggregationOverRangeFunction(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/v1/query_range_explain?query=sum%20by%20(job)%20(rate(up%5B5m%5D))&start=0&end=300&step=30&native_lowering_mode=prefer"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for prefer range aggregation explain, got %d: %s", res.Code, res.Body.String())
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
	if body.Data.NativeLoweringMode != string(local.NativeLoweringModePrefer) {
		t.Fatalf("expected prefer mode in explain response, got %#v", body.Data)
	}
	if body.Data.Plan.Strategy != "native_sql" {
		t.Fatalf("expected native_sql plan for prefer range aggregation, got %#v", body.Data.Plan)
	}
}

func TestQueryRangeExplainForceSupportedAllowsAnchoredAggregationRoot(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
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

func TestDedicatedExplainShadowModeShowsServedLocalPlan(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=1%20%2B%202&time=300&native_lowering_mode=shadow", nil)
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
	if body.Status != "success" || body.Data.NativeLoweringMode != "shadow" {
		t.Fatalf("unexpected explain payload: %#v", body)
	}
	if body.Data.Plan.Strategy != "local" {
		t.Fatalf("expected dedicated shadow explain to show served local plan, got %#v", body.Data.Plan)
	}
}

func TestSelectInstantPlanForRoutingRebuildsLocalOverride(t *testing.T) {
	service := &queryService{opts: Options{Database: "observability", Table: "prometheus", ClickHouseVersion: "26.3", DisableEntireQueryDelegation: true, AllowRequestRoutingOverrides: true}}
	req := httpapi.InstantQueryRequest{Query: "up", Time: "300", NativeLoweringMode: string(local.NativeLoweringModeForceSupported)}
	_, _, plan, analysis, _, apiErr := service.buildInstantPlan(req)
	if apiErr != nil {
		t.Fatalf("buildInstantPlan: %v", apiErr)
	}
	strictExplain := local.ExplainPlanWithLowering(plan, analysis.Root)
	if strictExplain.Strategy != "native_sql" {
		t.Fatalf("expected strict native plan, got %#v", strictExplain)
	}
	routing := httpapi.RoutingInfo{Decision: "local_override", StrictStrategy: strictExplain.Strategy, SelectedStrategy: "local", CandidateDecision: &httpapi.CandidateDecision{SelectedCandidate: "full_local"}}
	_, _, selectedExplain, selectedRouting := service.selectInstantPlanForRouting(httpapi.InstantQueryRequest{Query: "up", Time: "300"}, plan, analysis, routing)
	if selectedRouting.Decision != "local_override" || selectedExplain.Strategy == strictExplain.Strategy {
		t.Fatalf("expected rebuilt override explain, got routing=%#v plan=%#v", selectedRouting, selectedExplain)
	}
}

func TestQueryExplainIncludesRoutingCostClass(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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
				Policy            string `json:"policy"`
				Decision          string `json:"decision"`
				CandidateDecision struct {
					StrictCandidate   string `json:"strictCandidate"`
					SelectedCandidate string `json:"selectedCandidate"`
					ServedCandidate   string `json:"servedCandidate"`
				} `json:"candidateDecision"`
				Candidates []struct {
					ID            string   `json:"id"`
					Tier          string   `json:"tier"`
					RejectReasons []string `json:"rejectReasons"`
				} `json:"candidates"`
				Class struct {
					Family           string `json:"family"`
					SelectorCount    int    `json:"selectorCount"`
					HasRangeFunction bool   `json:"hasRangeFunction"`
					LookbackMS       int64  `json:"lookbackMs"`
					EstimateState    struct {
						Source        string `json:"source"`
						Fresh         bool   `json:"fresh"`
						SelectorCount int    `json:"selectorCount"`
						Missing       int    `json:"missing"`
					} `json:"estimateState"`
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
	if body.Data.Routing.CandidateDecision.StrictCandidate != "native_sql" || body.Data.Routing.CandidateDecision.SelectedCandidate != "native_sql" {
		t.Fatalf("unexpected candidate decision: %#v", body.Data.Routing.CandidateDecision)
	}
	if len(body.Data.Routing.Candidates) == 0 {
		t.Fatalf("expected candidate metadata in routing explain")
	}
	foundFullLocal := false
	for _, candidate := range body.Data.Routing.Candidates {
		if candidate.ID == "full_local" {
			foundFullLocal = true
		}
	}
	if !foundFullLocal {
		t.Fatalf("expected full_local candidate in %+v", body.Data.Routing.Candidates)
	}
	if body.Data.Routing.Class.Family != "rate" || !body.Data.Routing.Class.HasRangeFunction || body.Data.Routing.Class.SelectorCount != 1 {
		t.Fatalf("unexpected cost class: %#v", body.Data.Routing.Class)
	}
	if body.Data.Routing.Class.LookbackMS != int64((5 * time.Minute).Milliseconds()) {
		t.Fatalf("lookback = %d", body.Data.Routing.Class.LookbackMS)
	}
	if body.Data.Routing.Class.EstimateState.Source != "none" || body.Data.Routing.Class.EstimateState.Fresh || body.Data.Routing.Class.EstimateState.SelectorCount != 1 || body.Data.Routing.Class.EstimateState.Missing != 1 {
		t.Fatalf("unexpected estimate state: %#v", body.Data.Routing.Class.EstimateState)
	}
}

func TestQueryExplainIncludesSubqueryEstimateInputs(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=rate(up%5B5m%5D)%5B30m:1m%5D&time=300&routing_policy=cost_prefer", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Data struct {
			Routing struct {
				SelectedStrategy string   `json:"selectedStrategy"`
				StrictStrategy   string   `json:"strictStrategy"`
				Advisory         []string `json:"advisory"`
				Class            struct {
					Family                 string  `json:"family"`
					HasSubquery            bool    `json:"hasSubquery"`
					LookbackMS             int64   `json:"lookbackMs"`
					SubqueryRangeMS        int64   `json:"subqueryRangeMs"`
					SubqueryStepMS         int64   `json:"subqueryStepMs"`
					SubqueryPointsPerEval  int64   `json:"subqueryPointsPerEval"`
					SubqueryOverlapSlots   float64 `json:"subqueryOverlapSlots"`
					SubqueryWorkUnits      int64   `json:"subqueryWorkUnits"`
					SubqueryTemporalFanout int64   `json:"subqueryTemporalFanout"`
					SubqueryComplexityBand string  `json:"subqueryComplexityBand"`
				} `json:"class"`
			} `json:"routing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	class := body.Data.Routing.Class
	if class.Family != "subquery" || !class.HasSubquery {
		t.Fatalf("unexpected subquery class: %#v", class)
	}
	if class.SubqueryRangeMS != int64((30 * time.Minute).Milliseconds()) {
		t.Fatalf("subqueryRangeMs = %d, want %d", class.SubqueryRangeMS, int64((30 * time.Minute).Milliseconds()))
	}
	if class.SubqueryStepMS != int64(time.Minute.Milliseconds()) {
		t.Fatalf("subqueryStepMs = %d, want %d", class.SubqueryStepMS, int64(time.Minute.Milliseconds()))
	}
	if class.LookbackMS != int64((30 * time.Minute).Milliseconds()) {
		t.Fatalf("lookbackMs = %d, want %d", class.LookbackMS, int64((30 * time.Minute).Milliseconds()))
	}
	if class.SubqueryPointsPerEval != 31 {
		t.Fatalf("subqueryPointsPerEval = %d, want 31", class.SubqueryPointsPerEval)
	}
	if class.SubqueryOverlapSlots != 30 {
		t.Fatalf("subqueryOverlapSlots = %v, want 30", class.SubqueryOverlapSlots)
	}
	if class.SubqueryWorkUnits != 31 {
		t.Fatalf("subqueryWorkUnits = %d, want 31", class.SubqueryWorkUnits)
	}
	if class.SubqueryTemporalFanout != 31 {
		t.Fatalf("subqueryTemporalFanout = %d, want 31", class.SubqueryTemporalFanout)
	}
	if class.SubqueryComplexityBand != "light" {
		t.Fatalf("subqueryComplexityBand = %q, want light", class.SubqueryComplexityBand)
	}
	if len(body.Data.Routing.Advisory) == 0 || body.Data.Routing.Advisory[0] != "subquery_complexity=light" {
		t.Fatalf("routing advisory = %#v, want subquery complexity advisory", body.Data.Routing.Advisory)
	}
	if body.Data.Routing.StrictStrategy != "native_sql" || body.Data.Routing.SelectedStrategy != "native_sql" {
		t.Fatalf("advisory path must not change strategy selection, got strict=%q selected=%q", body.Data.Routing.StrictStrategy, body.Data.Routing.SelectedStrategy)
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
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
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

func TestQueryExplainIncludesLogicalOptimizationMetadata(t *testing.T) {
	handler, err := NewHandler(Options{AllowRequestRoutingOverrides: true, ClickHouseEndpoint: "http://127.0.0.1:8123/", DisableEntireQueryDelegation: true})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/query_explain?query=rate(up%7Bjob%3D%22api%22%7D%5B5m%5D)&time=300", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Plan struct {
				LogicalOptimization struct {
					Passes []struct {
						Name        string   `json:"name"`
						SkipReasons []string `json:"skipReasons"`
						Metadata    struct {
							PreservedInvariants []string `json:"preservedInvariants"`
							ExpectedSignals     []string `json:"expectedSignals"`
						} `json:"metadata"`
					} `json:"passes"`
					Selectors []struct {
						Fingerprint             string   `json:"fingerprint"`
						NormalizedMatchers      []string `json:"normalizedMatchers"`
						ReuseBlockedReason      string   `json:"reuseBlockedReason"`
						RequiredLookbackSeconds float64  `json:"requiredLookbackSeconds"`
						RequiredLabels          []string `json:"requiredLabels"`
					} `json:"selectors"`
				} `json:"logicalOptimization"`
			} `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	opt := body.Data.Plan.LogicalOptimization
	if len(opt.Passes) == 0 || opt.Passes[0].Name != "constant_fold_unary_negation" {
		t.Fatalf("logical pass trace missing: %+v", opt.Passes)
	}
	if len(opt.Passes[0].SkipReasons) == 0 || len(opt.Passes[0].Metadata.PreservedInvariants) == 0 || len(opt.Passes[0].Metadata.ExpectedSignals) == 0 {
		t.Fatalf("logical pass metadata incomplete: %+v", opt.Passes[0])
	}
	if len(opt.Selectors) != 1 {
		t.Fatalf("selector metadata count = %d, want 1", len(opt.Selectors))
	}
	selector := opt.Selectors[0]
	if selector.Fingerprint == "" || selector.RequiredLookbackSeconds != 300 || selector.ReuseBlockedReason != "unique_selector" {
		t.Fatalf("unexpected selector metadata: %+v", selector)
	}
	if !containsAll(selector.RequiredLabels, []string{"__name__", "job"}) {
		t.Fatalf("required labels = %v, want __name__ and job", selector.RequiredLabels)
	}
}

func containsAll(values, required []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}
