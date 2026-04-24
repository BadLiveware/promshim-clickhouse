package local

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/storage"
)

func TestNativeSubtreePlanNormalizesInstantVectorTimestampToEvaluationTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","api"]],"timestamp":"2026-04-20 11:34:00.000","value":1}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	plan := &nativeSubtreePlan{
		Kind: "leaf",
		Expr: "up",
		Fragment: &nativeplan.NativeFragment{
			Kind:       nativeplan.FragmentKindLeafSource,
			OutputKind: nativeplan.OutputKindInstantVector,
			Selector:   &nativeplan.SelectorSource{Kind: nativeplan.SelectorKindInstantVector, MetricName: "up", Lookback: 5 * time.Minute},
			ValueExpr:  "{value}",
			TagsExpr:   "{tags}",
		},
		Info:               &nativeplan.LoweringInfo{OutputKind: nativeplan.OutputKindInstantVector},
		OptimizationReport: &nativeplan.OptimizationReport{RequiredInputStartMS: 0, RequiredInputEndMS: 0},
	}

	evalTime := time.Unix(1234, 0).UTC()
	value, err := plan.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeInstant, EvaluationTime: evalTime})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got, want := vector.Samples[0].Timestamp, float64(evalTime.Unix()); got != want {
		t.Fatalf("expected evaluation timestamp %v, got %v", want, got)
	}
}

func TestNativeSubtreePlanAppliesRuntimeModuloCorrectionToInstantVectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","demo"]],"timestamp":"2026-04-20 11:34:00.000","value":1264108626}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	scalar := -7.333333333333334
	plan := &nativeSubtreePlan{
		Kind: "binary",
		Expr: "demo_memory_usage_bytes % (1 * 2 + 4 / 6 - 10)",
		Fragment: &nativeplan.NativeFragment{
			Kind:       nativeplan.FragmentKindValueTransform,
			OutputKind: nativeplan.OutputKindInstantVector,
			ValueTransform: &nativeplan.ValueTransformFragment{
				Child: &nativeplan.NativeFragment{
					Kind:       nativeplan.FragmentKindLeafSource,
					OutputKind: nativeplan.OutputKindInstantVector,
					Selector:   &nativeplan.SelectorSource{Kind: nativeplan.SelectorKindInstantVector, MetricName: "demo_memory_usage_bytes", Lookback: 5 * time.Minute},
					ValueExpr:  "{value}",
					TagsExpr:   "{tags}",
				},
				ValueExpr: "{value}",
				RuntimeTransform: &nativeplan.RuntimeValueTransform{
					Op:     nativeplan.RuntimeValueTransformPromQLModulo,
					Scalar: &scalar,
				},
				DropsMetric: true,
			},
		},
		Info:               &nativeplan.LoweringInfo{OutputKind: nativeplan.OutputKindInstantVector},
		OptimizationReport: &nativeplan.OptimizationReport{RequiredInputStartMS: 0, RequiredInputEndMS: 0},
	}

	evalTime := time.Unix(1776807862, 0).UTC()
	value, err := plan.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeInstant, EvaluationTime: evalTime})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got, want := vector.Samples[0].Value, 7.333333231264788; got != want {
		t.Fatalf("expected PromQL modulo-corrected value %v, got %v", want, got)
	}
}

func TestNativeSubtreePlanAppliesRuntimeModuloCorrectionToRangeMatrices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","demo"]],"time_series":[["2026-04-20 11:34:00.000",1264108626],["2026-04-20 11:34:10.000",1380797813]]}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	scalar := -7.333333333333334
	plan := &nativeSubtreePlan{
		Kind: "binary",
		Expr: "demo_memory_usage_bytes % (1 * 2 + 4 / 6 - 10)",
		Fragment: &nativeplan.NativeFragment{
			Kind:       nativeplan.FragmentKindValueTransform,
			OutputKind: nativeplan.OutputKindInstantVector,
			ValueTransform: &nativeplan.ValueTransformFragment{
				Child: &nativeplan.NativeFragment{
					Kind:       nativeplan.FragmentKindLeafSource,
					OutputKind: nativeplan.OutputKindInstantVector,
					Selector:   &nativeplan.SelectorSource{Kind: nativeplan.SelectorKindRangeVector, MetricName: "demo_memory_usage_bytes", Lookback: 5 * time.Minute, RequireFullTags: true},
					ValueExpr:  "{value}",
					TagsExpr:   "{tags}",
				},
				ValueExpr: "{value}",
				RuntimeTransform: &nativeplan.RuntimeValueTransform{
					Op:     nativeplan.RuntimeValueTransformPromQLModulo,
					Scalar: &scalar,
				},
				DropsMetric: true,
			},
		},
		Info:               &nativeplan.LoweringInfo{OutputKind: nativeplan.OutputKindInstantVector},
		OptimizationReport: &nativeplan.OptimizationReport{RequiredInputStartMS: 0, RequiredInputEndMS: 0},
	}

	value, err := plan.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeRange, Start: time.Unix(1776807862, 0).UTC(), End: time.Unix(1776807872, 0).UTC(), Step: 10 * time.Second})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected one 2-point series, got %#v", matrix.Series)
	}
	if got, want := matrix.Series[0].Values[0].Value, 7.333333231264788; got != want {
		t.Fatalf("expected first PromQL modulo-corrected value %v, got %v", want, got)
	}
	if got, want := matrix.Series[0].Values[1].Value, 6.333333221842896; got != want {
		t.Fatalf("expected second PromQL modulo-corrected value %v, got %v", want, got)
	}
}

func TestNativeSubtreePlanExecutesRangeRateOverSubquery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got, want := r.FormValue("param_step_ms"), "60000"; got != want {
			t.Fatalf("expected inner subquery step %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_start_ms"), "-300000"; got != want {
			t.Fatalf("expected expanded subquery start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_end_ms"), "300000"; got != want {
			t.Fatalf("expected expanded subquery end %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_required_start_ms"), "-600000"; got != want {
			t.Fatalf("expected shifted required start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_required_end_ms"), "300000"; got != want {
			t.Fatalf("expected shifted required end %q, got %q", want, got)
		}
		if query := r.FormValue("query"); query == "" {
			t.Fatal("expected rendered native SQL query")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","api"]],"time_series":[["1970-01-01 00:00:00.000",1],["1970-01-01 00:01:00.000",2]]}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	expr, err := logical.ParseExpression(`rate(sum(up)[5m:1m])`)
	if err != nil {
		t.Fatalf("parse expression: %v", err)
	}
	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	nativeBuilt, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected native subtree plan, got %T", built)
	}

	value, err := nativeBuilt.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected decoded range result, got %#v", matrix.Series)
	}
}

func TestNativeSubtreePlanExecutesRangeQuantileOverTimeOverSubquery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got, want := r.FormValue("param_step_ms"), "60000"; got != want {
			t.Fatalf("expected inner subquery step %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_start_ms"), "-300000"; got != want {
			t.Fatalf("expected expanded subquery start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_end_ms"), "300000"; got != want {
			t.Fatalf("expected expanded subquery end %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_required_start_ms"), "-600000"; got != want {
			t.Fatalf("expected shifted required start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_required_end_ms"), "300000"; got != want {
			t.Fatalf("expected shifted required end %q, got %q", want, got)
		}
		if query := r.FormValue("query"); query == "" {
			t.Fatal("expected rendered native SQL query")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","api"]],"time_series":[["1970-01-01 00:00:00.000",1], ["1970-01-01 00:01:00.000",2]]}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	expr, err := logical.ParseExpression(`quantile_over_time(0.95, sum(up)[5m:1m])`)
	if err != nil {
		t.Fatalf("parse expression: %v", err)
	}
	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	nativeBuilt, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected native subtree plan, got %T", built)
	}

	value, err := nativeBuilt.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected decoded range result, got %#v", matrix.Series)
	}
}

func TestNativeSubtreePlanExecutesInstantInfoRegexQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got, want := r.FormValue("param_instant_matcher_0_value"), "^(?:.+_info)$"; got != want {
			t.Fatalf("expected regex info selector matcher %q, got %q", want, got)
		}
		query := r.FormValue("query")
		for _, check := range []string{"groupArrayIf(rhs.original_group", "duplicate series for info metric", "lhs_is_info_metric"} {
			if !strings.Contains(query, check) {
				t.Fatalf("expected rendered info query to contain %q, got %q", check, query)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["__name__","up"],["instance","a"],["job","api"],["k8s_cluster_name","prod-eu"]],"timestamp":"1970-01-01 00:05:00.000","value":1}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	expr, err := logical.ParseExpression(`info(up, {__name__=~".+_info"})`)
	if err != nil {
		t.Fatalf("parse expression: %v", err)
	}
	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC(), NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	nativeBuilt, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected native subtree plan, got %T", built)
	}

	value, err := nativeBuilt.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(300, 0).UTC()})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 || vector.Samples[0].Metric["k8s_cluster_name"] != "prod-eu" {
		t.Fatalf("expected merged info output, got %#v", vector.Samples)
	}
}

func TestNativeSubtreePlanExecutesRangeRootSubquery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got, want := r.FormValue("param_step_ms"), "60000"; got != want {
			t.Fatalf("expected root-subquery step %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_start_ms"), "-300000"; got != want {
			t.Fatalf("expected root-subquery start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_end_ms"), "300000"; got != want {
			t.Fatalf("expected root-subquery end %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_required_start_ms"), "-600000"; got != want {
			t.Fatalf("expected root-subquery required start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_required_end_ms"), "300000"; got != want {
			t.Fatalf("expected root-subquery required end %q, got %q", want, got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","api"]],"time_series":[["1970-01-01 00:00:00.000",5],["1970-01-01 00:01:00.000",6]]}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	expr, err := logical.ParseExpression(`sum(up)[5m:1m]`)
	if err != nil {
		t.Fatalf("parse expression: %v", err)
	}
	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	nativeBuilt, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected native subtree plan, got %T", built)
	}

	value, err := nativeBuilt.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(300, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected decoded root-subquery range result, got %#v", matrix.Series)
	}
}

func TestNativeSubtreePlanExecutesAnchoredRangePointwiseQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got, want := r.FormValue("param_anchored_range_start_ms"), "0"; got != want {
			t.Fatalf("expected anchored range start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_anchored_range_end_ms"), "180000"; got != want {
			t.Fatalf("expected anchored range end %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_anchored_range_step_ms"), "60000"; got != want {
			t.Fatalf("expected anchored range step %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_anchored_child_required_start_ms"), "0"; got != want {
			t.Fatalf("expected anchored child required start %q, got %q", want, got)
		}
		if got, want := r.FormValue("param_anchored_child_required_end_ms"), "300000"; got != want {
			t.Fatalf("expected anchored child required end %q, got %q", want, got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","api"]],"time_series":[["1970-01-01 00:00:00.000",1],["1970-01-01 00:01:00.000",1],["1970-01-01 00:02:00.000",1],["1970-01-01 00:03:00.000",1]]}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	expr, err := logical.ParseExpression(`abs(up @ 300)`)
	if err != nil {
		t.Fatalf("parse expression: %v", err)
	}
	built, err := buildPlanWithContext(expr, PlanContext{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: time.Minute, NativeLoweringMode: NativeLoweringModeForceSupported})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	nativeBuilt, ok := built.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected native subtree plan, got %T", built)
	}

	value, err := nativeBuilt.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeRange, Start: time.Unix(0, 0).UTC(), End: time.Unix(180, 0).UTC(), Step: time.Minute})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 4 {
		t.Fatalf("expected anchored broadcast result, got %#v", matrix.Series)
	}
}
