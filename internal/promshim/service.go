package promshim

import (
	"context"
	"fmt"
	"net/http"
	"time"

	httpapi "ch-observability/internal/promshim/httpapi"
	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

type queryService struct {
	opts          Options
	client        *storage.Client
	evaluator     *evaluator
	shadowSummary *shadowSummaryRecorder
}

func NewHandler(opts Options) (http.Handler, error) {
	opts = normalizeOptions(opts)
	client, err := storage.NewClient(storage.Config{
		Endpoint:       opts.ClickHouseEndpoint,
		Username:       opts.Username,
		Password:       opts.Password,
		RequestTimeout: opts.RequestTimeout,
	})
	if err != nil {
		return nil, err
	}
	shadowMetrics := newShadowMetrics()
	service := &queryService{
		opts:          opts,
		client:        client,
		evaluator:     newEvaluator(opts, client),
		shadowSummary: newShadowSummaryRecorder(shadowMetrics),
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", shadowMetrics.handler())
	mux.Handle("/", httpapi.NewHandler(service))
	return mux, nil
}

func (h *queryService) InstantQuery(ctx context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	if mode == NativeLoweringModeShadow {
		return h.instantQueryShadow(ctx, req)
	}
	_, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(req)
	if apiErr != nil {
		return nil, apiErr
	}

	value, err := h.evaluator.evaluate(ctx, plan, evalParams{Mode: evalModeInstant, EvaluationTime: evaluationTime})
	if err != nil {
		return nil, apiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, apiErrorToHTTP(err)
	}
	if req.Explain || mode.forcesExplainResponse() {
		resultType, result, err := httpapi.RenderInstantQueryValue(value)
		if err != nil {
			return nil, apiErrorToHTTP(newExecutionErrorf("rendering instant query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{
			"status":                "success",
			"nativeLoweringMode":    string(mode),
			"entireQueryDelegation": h.entireQueryDelegationForQuery(req.Query),
			"data":                  map[string]any{"resultType": resultType, "result": result},
			"plan":                  explainPlanWithLowering(plan, analysis.Root),
		}}, nil
	}
	return &httpapi.Response{Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessInstantValue(w, value)
	}}, nil
}

func (h *queryService) RangeQuery(ctx context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	if mode == NativeLoweringModeShadow {
		return h.rangeQueryShadow(ctx, req)
	}
	_, start, end, step, plan, analysis, apiErr := h.buildRangePlan(req)
	if apiErr != nil {
		return nil, apiErr
	}

	value, err := h.evaluator.evaluate(ctx, plan, evalParams{Mode: evalModeRange, Start: start, End: end, Step: step})
	if err != nil {
		return nil, apiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, apiErrorToHTTP(err)
	}
	if req.Explain || mode.forcesExplainResponse() {
		resultType, result, err := httpapi.RenderRangeQueryValue(value)
		if err != nil {
			return nil, apiErrorToHTTP(newExecutionErrorf("rendering range query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{
			"status":                "success",
			"nativeLoweringMode":    string(mode),
			"entireQueryDelegation": h.entireQueryDelegationForQuery(req.Query),
			"data":                  map[string]any{"resultType": resultType, "result": result},
			"plan":                  explainPlanWithLowering(plan, analysis.Root),
		}}, nil
	}
	return &httpapi.Response{Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessRangeValue(w, value)
	}}, nil
}

func (h *queryService) instantQueryShadow(ctx context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	servedReq := req
	servedReq.NativeLoweringMode = string(NativeLoweringModeOff)
	planStart := time.Now()
	_, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(servedReq)
	servedPlanDuration := time.Since(planStart)
	if apiErr != nil {
		return nil, apiErr
	}
	evalStart := time.Now()
	value, err := h.evaluator.evaluate(ctx, plan, evalParams{Mode: evalModeInstant, EvaluationTime: evaluationTime})
	servedEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, apiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, apiErrorToHTTP(err)
	}
	shadow := h.runInstantShadowComparison(ctx, req, plan, value, servedPlanDuration, servedEvalDuration)
	if req.Explain {
		resultType, result, err := httpapi.RenderInstantQueryValue(value)
		if err != nil {
			return nil, apiErrorToHTTP(newExecutionErrorf("rendering instant query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{
			"status":                "success",
			"nativeLoweringMode":    string(NativeLoweringModeShadow),
			"entireQueryDelegation": h.entireQueryDelegationForQuery(req.Query),
			"data":                  map[string]any{"resultType": resultType, "result": result},
			"plan":                  explainPlanWithLowering(plan, analysis.Root),
			"shadow":                shadow,
			"shadowSummary":         h.shadowSummary.snapshot(),
		}}, nil
	}
	return &httpapi.Response{Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessInstantValue(w, value)
	}}, nil
}

func (h *queryService) rangeQueryShadow(ctx context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	servedReq := req
	servedReq.NativeLoweringMode = string(NativeLoweringModeOff)
	planStart := time.Now()
	_, start, end, step, plan, analysis, apiErr := h.buildRangePlan(servedReq)
	servedPlanDuration := time.Since(planStart)
	if apiErr != nil {
		return nil, apiErr
	}
	evalStart := time.Now()
	value, err := h.evaluator.evaluate(ctx, plan, evalParams{Mode: evalModeRange, Start: start, End: end, Step: step})
	servedEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, apiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, apiErrorToHTTP(err)
	}
	shadow := h.runRangeShadowComparison(ctx, req, plan, value, servedPlanDuration, servedEvalDuration)
	if req.Explain {
		resultType, result, err := httpapi.RenderRangeQueryValue(value)
		if err != nil {
			return nil, apiErrorToHTTP(newExecutionErrorf("rendering range query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{
			"status":                "success",
			"nativeLoweringMode":    string(NativeLoweringModeShadow),
			"entireQueryDelegation": h.entireQueryDelegationForQuery(req.Query),
			"data":                  map[string]any{"resultType": resultType, "result": result},
			"plan":                  explainPlanWithLowering(plan, analysis.Root),
			"shadow":                shadow,
			"shadowSummary":         h.shadowSummary.snapshot(),
		}}, nil
	}
	return &httpapi.Response{Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessRangeValue(w, value)
	}}, nil
}

func (h *queryService) ExplainInstant(_ context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	query, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(req)
	if apiErr != nil {
		return nil, apiErr
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{
		"status": "success",
		"data": map[string]any{
			"mode":                  string(evalModeInstant),
			"nativeLoweringMode":    string(mode),
			"entireQueryDelegation": h.entireQueryDelegationForQuery(query),
			"query":                 query,
			"evaluationTime":        evaluationTime.UTC().Format(time.RFC3339Nano),
			"plan":                  explainPlanWithLowering(plan, analysis.Root),
		},
	}}, nil
}

func (h *queryService) ExplainRange(_ context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	query, start, end, step, plan, analysis, apiErr := h.buildRangePlan(req)
	if apiErr != nil {
		return nil, apiErr
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{
		"status": "success",
		"data": map[string]any{
			"mode":                  string(evalModeRange),
			"nativeLoweringMode":    string(mode),
			"entireQueryDelegation": h.entireQueryDelegationForQuery(query),
			"query":                 query,
			"start":                 start.UTC().Format(time.RFC3339Nano),
			"end":                   end.UTC().Format(time.RFC3339Nano),
			"step":                  step.String(),
			"plan":                  explainPlanWithLowering(plan, analysis.Root),
		},
	}}, nil
}

func (h *queryService) Labels(ctx context.Context, req httpapi.MetadataRequest) (*httpapi.Response, *httpapi.APIError) {
	httpReq, apiErr := metadataHTTPRequest(ctx, req)
	if apiErr != nil {
		return nil, apiErr
	}
	sql, params, err := storage.BuildLabelsQuery(storage.QueryConfig{Database: h.opts.Database, Table: h.opts.Table}, httpReq)
	if err != nil {
		return nil, badRequestHTTPError(err.Error())
	}
	response, err := h.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, apiErrorToHTTP(normalizeInternalError(err))
	}
	defer response.Body.Close()
	labels, decErr := decodeStringRows[labelRow](response.Body, func(row labelRow) string { return row.Label })
	if decErr != nil {
		return nil, apiErrorPtr(toHTTPAPIError(*decErr))
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success", "data": labels}}, nil
}

func (h *queryService) LabelValues(ctx context.Context, req httpapi.LabelValuesRequest) (*httpapi.Response, *httpapi.APIError) {
	httpReq, apiErr := metadataHTTPRequest(ctx, req.MetadataRequest)
	if apiErr != nil {
		return nil, apiErr
	}
	sql, params, err := storage.BuildLabelValuesQuery(storage.QueryConfig{Database: h.opts.Database, Table: h.opts.Table}, httpReq, req.Name)
	if err != nil {
		return nil, badRequestHTTPError(err.Error())
	}
	response, err := h.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, apiErrorToHTTP(normalizeInternalError(err))
	}
	defer response.Body.Close()
	values, decErr := decodeStringRows[valueRow](response.Body, func(row valueRow) string { return row.Value })
	if decErr != nil {
		return nil, apiErrorPtr(toHTTPAPIError(*decErr))
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success", "data": values}}, nil
}

func (h *queryService) Series(ctx context.Context, req httpapi.MetadataRequest) (*httpapi.Response, *httpapi.APIError) {
	httpReq, apiErr := metadataHTTPRequest(ctx, req)
	if apiErr != nil {
		return nil, apiErr
	}
	sql, params, err := storage.BuildSeriesQuery(storage.QueryConfig{Database: h.opts.Database, Table: h.opts.Table}, httpReq)
	if err != nil {
		return nil, badRequestHTTPError(err.Error())
	}
	response, err := h.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, apiErrorToHTTP(normalizeInternalError(err))
	}
	defer response.Body.Close()
	rows, decErr := decodeSeriesRows(response.Body)
	if decErr != nil {
		return nil, apiErrorPtr(toHTTPAPIError(*decErr))
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success", "data": rows}}, nil
}

func enforceResponseLimits(value model.RuntimeValue, opts Options) error {
	stats, err := httpapi.RuntimeValueResponseStats(value)
	if err != nil {
		return newExecutionErrorf("computing response stats: %v", err)
	}
	if opts.MaxResponseSeries > 0 && stats.Series > opts.MaxResponseSeries {
		return newBadDataErrorf("query result would return %d series, exceeding configured limit %d", stats.Series, opts.MaxResponseSeries)
	}
	if opts.MaxResponsePoints > 0 && stats.Points > opts.MaxResponsePoints {
		return newBadDataErrorf("query result would return %d points, exceeding configured limit %d", stats.Points, opts.MaxResponsePoints)
	}
	return nil
}

func (h *queryService) nativeLoweringModeForRequest(requestMode string) (NativeLoweringMode, *httpapi.APIError) {
	mode := h.opts.NativeLoweringMode
	if requestMode != "" {
		parsed, err := parseNativeLoweringMode(requestMode)
		if err != nil {
			return "", badRequestHTTPError(err.Error())
		}
		mode = parsed
	}
	return normalizeNativeLoweringMode(mode), nil
}

func (h *queryService) entireQueryDelegationForQuery(query string) *delegationClassifierResult {
	expr, err := plan.ParseExpression(query)
	if err != nil {
		return nil
	}
	result := classifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion)
	return &result
}

func (h *queryService) buildInstantPlan(req httpapi.InstantQueryRequest) (string, time.Time, queryPlan, *nativeplan.Analysis, *httpapi.APIError) {
	query := req.Query
	if query == "" {
		return "", time.Time{}, nil, nil, badRequestHTTPError("missing required parameter 'query'")
	}
	expr, err := plan.ParseExpression(query)
	if err != nil {
		return "", time.Time{}, nil, nil, badRequestHTTPError(err.Error())
	}
	evaluationTime := time.Now().UTC()
	if req.Time != "" {
		evaluationTime, err = model.ParsePrometheusTimestamp(req.Time)
		if err != nil {
			return "", time.Time{}, nil, nil, badRequestHTTPError(err.Error())
		}
	}
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return "", time.Time{}, nil, nil, apiErr
	}
	ctx := planContext{Mode: evalModeInstant, EvaluationTime: evaluationTime, ClickHouseVersion: h.opts.ClickHouseVersion, NativeLoweringMode: mode, PreferNativeAggregationPushdown: mode.enablesNativePlanning(), MaxRangePointsPerSeries: h.opts.MaxRangePointsPerSeries, RangeChunkPointsPerSeries: h.opts.RangeChunkPointsPerSeries}
	delegation := classifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion)
	var plan queryPlan
	var analysis *nativeplan.Analysis
	if mode != NativeLoweringModeOff && delegation.Eligible && !mode.forcesNativeRoot() && !h.opts.DisableEntireQueryDelegation {
		plan, analysis, err = buildEntireQueryDelegatedPlan(expr)
		if err != nil {
			return "", time.Time{}, nil, nil, apiErrorToHTTP(err)
		}
	} else {
		plan, analysis, err = buildPlanWithContextAndAnalysis(expr, ctx)
		if err != nil {
			return "", time.Time{}, nil, nil, apiErrorToHTTP(err)
		}
	}
	if mode.forcesNativeRoot() {
		explain := explainPlan(plan)
		if explain.Strategy != "native_sql" {
			return "", time.Time{}, nil, nil, apiErrorToHTTP(newUnsupportedErrorf("native lowering mode %q requires a native_sql root plan for %q, got %s", mode, query, explain.Strategy))
		}
	}
	return query, evaluationTime, plan, analysis, nil
}

func (h *queryService) buildRangePlan(req httpapi.RangeQueryRequest) (string, time.Time, time.Time, time.Duration, queryPlan, *nativeplan.Analysis, *httpapi.APIError) {
	query := req.Query
	if query == "" {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError("missing required parameter 'query'")
	}
	expr, err := plan.ParseExpression(query)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError(err.Error())
	}
	if expr.Type() != parser.ValueTypeScalar && expr.Type() != parser.ValueTypeVector {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError(fmt.Sprintf("invalid expression type %q for range query, must be scalar or instant vector", expr.Type()))
	}
	start, err := model.ParsePrometheusTimestamp(req.Start)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError(err.Error())
	}
	end, err := model.ParsePrometheusTimestamp(req.End)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError(err.Error())
	}
	if end.Before(start) {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError("end must be greater than or equal to start")
	}
	step, err := model.ParsePrometheusDuration(req.Step)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, badRequestHTTPError(err.Error())
	}
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, apiErr
	}
	ctx := planContext{Mode: evalModeRange, Start: start, End: end, Step: step, ClickHouseVersion: h.opts.ClickHouseVersion, NativeLoweringMode: mode, PreferNativeAggregationPushdown: mode.enablesNativePlanning(), MaxRangePointsPerSeries: h.opts.MaxRangePointsPerSeries, RangeChunkPointsPerSeries: h.opts.RangeChunkPointsPerSeries}
	delegation := classifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion)
	var plan queryPlan
	var analysis *nativeplan.Analysis
	if mode != NativeLoweringModeOff && delegation.Eligible && !mode.forcesNativeRoot() && !h.opts.DisableEntireQueryDelegation {
		plan, analysis, err = buildEntireQueryDelegatedPlan(expr)
		if err != nil {
			return "", time.Time{}, time.Time{}, 0, nil, nil, apiErrorToHTTP(err)
		}
	} else {
		plan, analysis, err = buildPlanWithContextAndAnalysis(expr, ctx)
		if err != nil {
			return "", time.Time{}, time.Time{}, 0, nil, nil, apiErrorToHTTP(err)
		}
	}
	if mode.forcesNativeRoot() {
		explain := explainPlan(plan)
		if explain.Strategy != "native_sql" {
			return "", time.Time{}, time.Time{}, 0, nil, nil, apiErrorToHTTP(newUnsupportedErrorf("native lowering mode %q requires a native_sql root plan for %q, got %s", mode, query, explain.Strategy))
		}
	}
	return query, start, end, step, plan, analysis, nil
}
