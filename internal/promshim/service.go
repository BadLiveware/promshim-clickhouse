package promshim

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/shadow"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

type queryService struct {
	opts               Options
	client             *storage.Client
	evaluator          *local.Evaluator
	promotedTagColumns map[string]struct{}
	timeSeriesIDType   string
	shadow             *shadow.Runner
	selectorStats      *selectorStatsCache
	selectorProbeSem   chan struct{}
}

func (h *queryService) ClickHouseTransport() string {
	return string(h.opts.ClickHouseTransport)
}

func (h *queryService) queryConfig() storage.QueryConfig {
	return storage.QueryConfig{Database: h.opts.Database, Table: h.opts.Table, PromotedTagColumns: h.promotedTagColumns, EnableNativeGridFunctions: h.opts.NativeGridFunctions == "prefer", EnableCumulativeAvgOverTime: h.opts.CumulativeAvgOverTime == "prefer", MaxMetadataSeries: h.opts.MaxResponseSeries, MaxMetadataItems: h.opts.MaxMetadataItems}
}

func mergePromotedTagColumns(base, extra map[string]struct{}) map[string]struct{} {
	if len(extra) == 0 {
		return base
	}
	if base == nil {
		base = map[string]struct{}{}
	}
	for column := range extra {
		base[column] = struct{}{}
	}
	return base
}

func promotedTagColumnSet(columns []string) map[string]struct{} {
	if len(columns) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if column == "" {
			continue
		}
		out[column] = struct{}{}
	}
	return out
}

func hSettingsProfileConfig(opts Options) storage.SettingsProfileConfig {
	return storage.SettingsProfileConfig{
		Name:                opts.ClickHouseSettingsProfile,
		ClickHouseVersion:   opts.ClickHouseVersion,
		RequestTimeout:      opts.RequestTimeout,
		MaxMemoryUsageBytes: opts.ClickHouseMaxMemoryUsageBytes,
		MaxRowsToRead:       opts.ClickHouseMaxRowsToRead,
		MaxResultRows:       opts.ClickHouseMaxResultRows,
	}
}

func (h *queryService) applySettingsProfileProvenance(routing *httpapi.RoutingInfo, explain *local.ExplainNode) storage.SettingsProfileExplain {
	family := ""
	candidate := ""
	if routing != nil {
		family = routing.Class.Family
		routing.SettingsProfile = storage.NormalizeSettingsProfileName(h.opts.ClickHouseSettingsProfile)
		if routing.CandidateDecision != nil {
			candidate = routing.CandidateDecision.ServedCandidate
		}
		for i := range routing.Candidates {
			routing.Candidates[i].SettingsProfile = routing.SettingsProfile
		}
	}
	resolution := storage.ResolveSettingsProfile(hSettingsProfileConfig(h.opts), "", family, candidate)
	if explain != nil {
		profile := resolution.Explain
		explain.SettingsProfile = &profile
	}
	return resolution.Explain
}

func NewHandler(opts Options) (http.Handler, error) {
	opts = normalizeOptions(opts)
	client, err := storage.NewClient(storage.Config{
		Endpoint:        opts.ClickHouseEndpoint,
		NativeAddr:      opts.ClickHouseNativeAddr,
		Database:        opts.Database,
		Username:        opts.Username,
		Password:        opts.Password,
		Compression:     opts.ClickHouseCompression,
		RequestTimeout:  opts.RequestTimeout,
		Transport:       opts.ClickHouseTransport,
		MaxOpenConns:    opts.ClickHouseMaxOpenConns,
		MaxIdleConns:    opts.ClickHouseMaxIdleConns,
		ConnMaxLifetime: opts.ClickHouseConnMaxLifetime,
		SettingsProfile: hSettingsProfileConfig(opts),
	})
	if err != nil {
		return nil, err
	}
	promotedTagColumns := promotedTagColumnSet(opts.PromotedTagColumns)
	if opts.DiscoverPromotedTagColumns {
		discoveryCtx, cancel := context.WithTimeout(context.Background(), opts.RequestTimeout)
		discovered, discoveryErr := storage.DiscoverPromotedTagColumns(discoveryCtx, client, storage.QueryConfig{Database: opts.Database, Table: opts.Table})
		cancel()
		if discoveryErr != nil {
			log.Printf("promshim: promoted tag column discovery failed: %v", discoveryErr)
		} else {
			promotedTagColumns = mergePromotedTagColumns(promotedTagColumns, discovered)
		}
	}
	discoveryCtx, cancel := context.WithTimeout(context.Background(), opts.RequestTimeout)
	timeSeriesIDType, idTypeErr := storage.DiscoverTimeSeriesIDType(discoveryCtx, client, storage.QueryConfig{Database: opts.Database, Table: opts.Table})
	cancel()
	if idTypeErr != nil {
		log.Printf("promshim: TimeSeries id type discovery failed: %v", idTypeErr)
	} else if timeSeriesIDType != "" {
		log.Printf("promshim: TimeSeries id column type: %s", timeSeriesIDType)
	}
	service := &queryService{
		opts:               opts,
		client:             client,
		evaluator:          local.NewEvaluator(opts.Database, opts.Table, client).WithPromotedTagColumns(promotedTagColumns).WithNativeGridFunctions(opts.NativeGridFunctions == "prefer").WithCumulativeAvgOverTime(opts.CumulativeAvgOverTime == "prefer"),
		promotedTagColumns: promotedTagColumns,
		timeSeriesIDType:   timeSeriesIDType,
		selectorStats:      newSelectorStatsCache(5 * time.Minute),
		selectorProbeSem:   make(chan struct{}, 2),
	}
	service.shadow = shadow.NewRunner(service)
	mux := http.NewServeMux()
	mux.Handle("/metrics", service.shadow.MetricsHandler())
	mux.Handle("/", httpapi.NewHandler(service))
	return mux, nil
}

func (h *queryService) InstantQuery(ctx context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	policy, apiErr := h.routingPolicyForRequest(req.RoutingPolicy)
	if apiErr != nil {
		return nil, apiErr
	}
	if mode == local.NativeLoweringModeShadow {
		return h.instantQueryShadow(ctx, req)
	}
	query, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(req)
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForInstant(query, evaluationTime, mode, policy, explain.Strategy)
	selectedPlan, _, selectedExplain, routing := h.selectInstantPlanForRouting(req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &selectedExplain)
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, selectedPlan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime})
	strictEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	if policy == RoutingPolicyCostShadow {
		h.runCostShadowInstant(ctx, req, value, routing, strictEvalDuration)
	}
	if req.Explain || mode.ForcesExplainResponse() {
		resultType, result, err := httpapi.RenderInstantQueryValue(value)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NewExecutionErrorf("rendering instant query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Strategy: selectedExplain.Strategy, FallbackReason: selectedExplain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
			"status":                    "success",
			"nativeLoweringMode":        string(mode),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(req.Query),
			"data":                      map[string]any{"resultType": resultType, "result": result},
			"plan":                      selectedExplain,
			"routing":                   routing,
		}}, nil
	}
	return &httpapi.Response{Strategy: selectedExplain.Strategy, FallbackReason: selectedExplain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, PhysicalDecisions: physicalDecisionSummary(selectedExplain), Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessInstantValue(w, value)
	}}, nil
}

func (h *queryService) RangeQuery(ctx context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	policy, apiErr := h.routingPolicyForRequest(req.RoutingPolicy)
	if apiErr != nil {
		return nil, apiErr
	}
	if mode == local.NativeLoweringModeShadow {
		return h.rangeQueryShadow(ctx, req)
	}
	query, start, end, step, plan, analysis, apiErr := h.buildRangePlan(req)
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForRange(query, start, end, step, mode, policy, explain.Strategy)
	selectedPlan, _, selectedExplain, routing := h.selectRangePlanForRouting(req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &selectedExplain)
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, selectedPlan, local.EvalParams{Mode: local.EvalModeRange, Start: start, End: end, Step: step})
	strictEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	if policy == RoutingPolicyCostShadow {
		h.runCostShadowRange(ctx, req, value, routing, strictEvalDuration)
	}
	if req.Explain || mode.ForcesExplainResponse() {
		resultType, result, err := httpapi.RenderRangeQueryValue(value)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NewExecutionErrorf("rendering range query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Strategy: selectedExplain.Strategy, FallbackReason: selectedExplain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
			"status":                    "success",
			"nativeLoweringMode":        string(mode),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(req.Query),
			"data":                      map[string]any{"resultType": resultType, "result": result},
			"plan":                      selectedExplain,
			"routing":                   routing,
		}}, nil
	}
	return &httpapi.Response{Strategy: selectedExplain.Strategy, FallbackReason: selectedExplain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, PhysicalDecisions: physicalDecisionSummary(selectedExplain), Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessRangeValue(w, value)
	}}, nil
}

func (h *queryService) instantQueryShadow(ctx context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	servedReq := req
	servedReq.NativeLoweringMode = string(local.NativeLoweringModeOff)
	planStart := time.Now()
	query, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(servedReq)
	servedPlanDuration := time.Since(planStart)
	if apiErr != nil {
		return nil, apiErr
	}
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime})
	servedEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForInstant(query, evaluationTime, local.NativeLoweringModeShadow, RoutingPolicyStrict, explain.Strategy)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &explain)
	shadowReport := h.shadow.RunInstant(ctx, req, explain.Strategy, value, servedPlanDuration, servedEvalDuration)
	if req.Explain {
		resultType, result, err := httpapi.RenderInstantQueryValue(value)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NewExecutionErrorf("rendering instant query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
			"status":                    "success",
			"nativeLoweringMode":        string(local.NativeLoweringModeShadow),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(req.Query),
			"data":                      map[string]any{"resultType": resultType, "result": result},
			"plan":                      explain,
			"routing":                   routing,
			"shadow":                    shadowReport,
			"shadowSummary":             h.shadow.Summary(),
		}}, nil
	}
	return &httpapi.Response{Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessInstantValue(w, value)
	}}, nil
}

func (h *queryService) rangeQueryShadow(ctx context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	servedReq := req
	servedReq.NativeLoweringMode = string(local.NativeLoweringModeOff)
	planStart := time.Now()
	query, start, end, step, plan, analysis, apiErr := h.buildRangePlan(servedReq)
	servedPlanDuration := time.Since(planStart)
	if apiErr != nil {
		return nil, apiErr
	}
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, plan, local.EvalParams{Mode: local.EvalModeRange, Start: start, End: end, Step: step})
	servedEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	if err := enforceResponseLimits(value, h.opts); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForRange(query, start, end, step, local.NativeLoweringModeShadow, RoutingPolicyStrict, explain.Strategy)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &explain)
	shadowReport := h.shadow.RunRange(ctx, req, explain.Strategy, value, servedPlanDuration, servedEvalDuration)
	if req.Explain {
		resultType, result, err := httpapi.RenderRangeQueryValue(value)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NewExecutionErrorf("rendering range query response: %v", err))
		}
		return &httpapi.Response{StatusCode: http.StatusOK, Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
			"status":                    "success",
			"nativeLoweringMode":        string(local.NativeLoweringModeShadow),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(req.Query),
			"data":                      map[string]any{"resultType": resultType, "result": result},
			"plan":                      explain,
			"routing":                   routing,
			"shadow":                    shadowReport,
			"shadowSummary":             h.shadow.Summary(),
		}}, nil
	}
	return &httpapi.Response{Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessRangeValue(w, value)
	}}, nil
}

func (h *queryService) ExplainInstant(_ context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	policy, apiErr := h.routingPolicyForRequest(req.RoutingPolicy)
	if apiErr != nil {
		return nil, apiErr
	}
	planReq := req
	if mode == local.NativeLoweringModeShadow {
		planReq.NativeLoweringMode = string(local.NativeLoweringModeOff)
	}
	query, evaluationTime, plan, analysis, apiErr := h.buildInstantPlan(planReq)
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForInstant(query, evaluationTime, mode, policy, explain.Strategy)
	_, _, explain, routing = h.selectInstantPlanForRouting(req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &explain)
	return &httpapi.Response{StatusCode: http.StatusOK, Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
		"status": "success",
		"data": map[string]any{
			"mode":                      string(local.EvalModeInstant),
			"nativeLoweringMode":        string(mode),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(query),
			"query":                     query,
			"evaluationTime":            evaluationTime.UTC().Format(time.RFC3339Nano),
			"plan":                      explain,
			"routing":                   routing,
		},
	}}, nil
}

func (h *queryService) ExplainRange(_ context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return nil, apiErr
	}
	policy, apiErr := h.routingPolicyForRequest(req.RoutingPolicy)
	if apiErr != nil {
		return nil, apiErr
	}
	planReq := req
	if mode == local.NativeLoweringModeShadow {
		planReq.NativeLoweringMode = string(local.NativeLoweringModeOff)
	}
	query, start, end, step, plan, analysis, apiErr := h.buildRangePlan(planReq)
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForRange(query, start, end, step, mode, policy, explain.Strategy)
	_, _, explain, routing = h.selectRangePlanForRouting(req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &explain)
	return &httpapi.Response{StatusCode: http.StatusOK, Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
		"status": "success",
		"data": map[string]any{
			"mode":                      string(local.EvalModeRange),
			"nativeLoweringMode":        string(mode),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(query),
			"query":                     query,
			"start":                     start.UTC().Format(time.RFC3339Nano),
			"end":                       end.UTC().Format(time.RFC3339Nano),
			"step":                      step.String(),
			"plan":                      explain,
			"routing":                   routing,
		},
	}}, nil
}

func (h *queryService) Labels(ctx context.Context, req httpapi.MetadataRequest) (*httpapi.Response, *httpapi.APIError) {
	httpReq, apiErr := metadataHTTPRequest(ctx, req)
	if apiErr != nil {
		return nil, apiErr
	}
	sql, params, err := storage.BuildLabelsQuery(h.queryConfig(), httpReq)
	if err != nil {
		return nil, local.BadRequestHTTPError(err.Error())
	}
	var labels []string
	if h.client.TransportKind() == storage.TransportNative {
		labels, err = h.client.QueryStringRows(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeMetadataLabels})
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NormalizeInternalError(err))
		}
	} else {
		response, err := h.client.Execute(ctx, sql, params)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NormalizeInternalError(err))
		}
		defer func() { _ = response.Body.Close() }()
		var decErr *local.APIError
		labels, decErr = local.DecodeStringRows[local.LabelRow](response.Body, func(row local.LabelRow) string { return row.Label })
		if decErr != nil {
			return nil, local.ApiErrorPtr(local.ToHTTPAPIError(*decErr))
		}
	}
	if err := enforceMetadataItemLimit("label names", int64(len(labels)), h.opts.MaxMetadataItems); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success", "data": labels}}, nil
}

func (h *queryService) LabelValues(ctx context.Context, req httpapi.LabelValuesRequest) (*httpapi.Response, *httpapi.APIError) {
	httpReq, apiErr := metadataHTTPRequest(ctx, req.MetadataRequest)
	if apiErr != nil {
		return nil, apiErr
	}
	sql, params, err := storage.BuildLabelValuesQuery(h.queryConfig(), httpReq, req.Name)
	if err != nil {
		return nil, local.BadRequestHTTPError(err.Error())
	}
	var values []string
	if h.client.TransportKind() == storage.TransportNative {
		values, err = h.client.QueryStringRows(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeMetadataLabelValues})
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NormalizeInternalError(err))
		}
	} else {
		response, err := h.client.Execute(ctx, sql, params)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NormalizeInternalError(err))
		}
		defer func() { _ = response.Body.Close() }()
		var decErr *local.APIError
		values, decErr = local.DecodeStringRows[local.ValueRow](response.Body, func(row local.ValueRow) string { return row.Value })
		if decErr != nil {
			return nil, local.ApiErrorPtr(local.ToHTTPAPIError(*decErr))
		}
	}
	if err := enforceMetadataItemLimit("label values", int64(len(values)), h.opts.MaxMetadataItems); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success", "data": values}}, nil
}

func (h *queryService) Series(ctx context.Context, req httpapi.MetadataRequest) (*httpapi.Response, *httpapi.APIError) {
	httpReq, apiErr := metadataHTTPRequest(ctx, req)
	if apiErr != nil {
		return nil, apiErr
	}
	sql, params, err := storage.BuildSeriesQuery(h.queryConfig(), httpReq)
	if err != nil {
		return nil, local.BadRequestHTTPError(err.Error())
	}
	var rows []map[string]string
	if h.client.TransportKind() == storage.TransportNative {
		rows, err = h.client.QuerySeriesRows(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeMetadataSeries})
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NormalizeInternalError(err))
		}
	} else {
		response, err := h.client.Execute(ctx, sql, params)
		if err != nil {
			return nil, local.ApiErrorToHTTP(local.NormalizeInternalError(err))
		}
		defer func() { _ = response.Body.Close() }()
		var decErr *local.APIError
		rows, decErr = local.DecodeSeriesRows(response.Body)
		if decErr != nil {
			return nil, local.ApiErrorPtr(local.ToHTTPAPIError(*decErr))
		}
	}
	if err := enforceMetadataItemLimit("series", int64(len(rows)), h.opts.MaxResponseSeries); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	return &httpapi.Response{StatusCode: http.StatusOK, Body: map[string]any{"status": "success", "data": rows}}, nil
}

func enforceResponseLimits(value model.RuntimeValue, opts Options) error {
	stats, err := httpapi.RuntimeValueResponseStats(value)
	if err != nil {
		return local.NewExecutionErrorf("computing response stats: %v", err)
	}
	if opts.MaxResponseSeries > 0 && stats.Series > opts.MaxResponseSeries {
		return local.NewBadDataErrorf("query result would return %d series, exceeding configured limit %d", stats.Series, opts.MaxResponseSeries)
	}
	if opts.MaxResponsePoints > 0 && stats.Points > opts.MaxResponsePoints {
		return local.NewBadDataErrorf("query result would return %d points, exceeding configured limit %d", stats.Points, opts.MaxResponsePoints)
	}
	return nil
}

func enforceMetadataItemLimit(kind string, got, limit int64) error {
	if limit > 0 && got > limit {
		return local.NewBadDataErrorf("metadata result would return more than %d %s", limit, kind)
	}
	return nil
}

func (h *queryService) nativeLoweringModeForRequest(requestMode string) (local.NativeLoweringMode, *httpapi.APIError) {
	mode := h.opts.NativeLoweringMode
	if requestMode != "" {
		parsed, err := local.ParseNativeLoweringMode(requestMode)
		if err != nil {
			return "", local.BadRequestHTTPError(err.Error())
		}
		mode = parsed
	}
	return local.NormalizeNativeLoweringMode(mode), nil
}

func (h *queryService) routingPolicyForRequest(requestPolicy string) (RoutingPolicy, *httpapi.APIError) {
	policy := h.opts.RoutingPolicy
	if requestPolicy != "" {
		parsed, err := ParseRoutingPolicy(requestPolicy)
		if err != nil {
			return "", local.BadRequestHTTPError(err.Error())
		}
		policy = parsed
	}
	return NormalizeRoutingPolicy(policy), nil
}

func selectedCandidateMode(routing httpapi.RoutingInfo) (string, bool) {
	if routing.CandidateDecision == nil {
		return "", false
	}
	switch routing.CandidateDecision.SelectedCandidate {
	case string(cbeCandidateNativeSQL):
		return string(local.NativeLoweringModeForceSupported), true
	case string(cbeCandidateLocalPushdown):
		return string(local.NativeLoweringModeLocalPushdown), true
	case string(cbeCandidateFullLocal):
		return string(local.NativeLoweringModeOff), true
	default:
		return "", false
	}
}

func (h *queryService) selectInstantPlanForRouting(req httpapi.InstantQueryRequest, plan local.Plan, analysis *nativeplan.Analysis, routing httpapi.RoutingInfo) (local.Plan, *nativeplan.Analysis, local.ExplainNode, httpapi.RoutingInfo) {
	selectedPlan, selectedAnalysis := plan, analysis
	selectedExplain := local.ExplainPlanWithLowering(selectedPlan, selectedAnalysis.Root)
	if routing.Decision != "local_override" {
		return selectedPlan, selectedAnalysis, selectedExplain, routing
	}
	selectedMode, ok := selectedCandidateMode(routing)
	if !ok {
		routing.Decision = "strict_low_confidence"
		routing.Reason = "selected_candidate_not_executable"
		routing.SelectedStrategy = routing.StrictStrategy
		return selectedPlan, selectedAnalysis, selectedExplain, routing
	}
	candidateReq := req
	candidateReq.NativeLoweringMode = selectedMode
	_, _, candidatePlan, candidateAnalysis, candidateErr := h.buildInstantPlan(candidateReq)
	if candidateErr != nil {
		routing.Decision = "strict_low_confidence"
		routing.Reason = "local_plan_error"
		routing.SelectedStrategy = routing.StrictStrategy
		return selectedPlan, selectedAnalysis, selectedExplain, routing
	}
	selectedPlan, selectedAnalysis = candidatePlan, candidateAnalysis
	selectedExplain = local.ExplainPlanWithLowering(selectedPlan, selectedAnalysis.Root)
	return selectedPlan, selectedAnalysis, selectedExplain, routing
}

func (h *queryService) selectRangePlanForRouting(req httpapi.RangeQueryRequest, plan local.Plan, analysis *nativeplan.Analysis, routing httpapi.RoutingInfo) (local.Plan, *nativeplan.Analysis, local.ExplainNode, httpapi.RoutingInfo) {
	selectedPlan, selectedAnalysis := plan, analysis
	selectedExplain := local.ExplainPlanWithLowering(selectedPlan, selectedAnalysis.Root)
	if routing.Decision != "local_override" {
		return selectedPlan, selectedAnalysis, selectedExplain, routing
	}
	selectedMode, ok := selectedCandidateMode(routing)
	if !ok {
		routing.Decision = "strict_low_confidence"
		routing.Reason = "selected_candidate_not_executable"
		routing.SelectedStrategy = routing.StrictStrategy
		return selectedPlan, selectedAnalysis, selectedExplain, routing
	}
	candidateReq := req
	candidateReq.NativeLoweringMode = selectedMode
	_, _, _, _, candidatePlan, candidateAnalysis, candidateErr := h.buildRangePlan(candidateReq)
	if candidateErr != nil {
		routing.Decision = "strict_low_confidence"
		routing.Reason = "local_plan_error"
		routing.SelectedStrategy = routing.StrictStrategy
		return selectedPlan, selectedAnalysis, selectedExplain, routing
	}
	selectedPlan, selectedAnalysis = candidatePlan, candidateAnalysis
	selectedExplain = local.ExplainPlanWithLowering(selectedPlan, selectedAnalysis.Root)
	return selectedPlan, selectedAnalysis, selectedExplain, routing
}

func (h *queryService) routingInfoForInstant(query string, evaluationTime time.Time, mode local.NativeLoweringMode, policy RoutingPolicy, strictStrategy string) httpapi.RoutingInfo {
	timing := queryCostTiming{Endpoint: "query", Start: evaluationTime, End: evaluationTime}
	class := h.queryCostClass(query, timing, strictStrategy)
	h.maybeScheduleSelectorStatsProbes(query, timing, policy, class)
	return routingDecisionForStrict(policy, mode, class, strictStrategy, h.opts.CostRoutingLocalFamilies)
}

func (h *queryService) routingInfoForRange(query string, start, end time.Time, step time.Duration, mode local.NativeLoweringMode, policy RoutingPolicy, strictStrategy string) httpapi.RoutingInfo {
	timing := queryCostTiming{Endpoint: "query_range", Start: start, End: end, Step: step}
	class := h.queryCostClass(query, timing, strictStrategy)
	h.maybeScheduleSelectorStatsProbes(query, timing, policy, class)
	return routingDecisionForStrict(policy, mode, class, strictStrategy, h.opts.CostRoutingLocalFamilies)
}

func (h *queryService) queryCostClass(query string, timing queryCostTiming, strictStrategy string) httpapi.QueryCostClass {
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return httpapi.QueryCostClass{Endpoint: timing.Endpoint, Family: "unknown", RootStrategyStrict: strictStrategy, OutputKind: "unknown"}
	}
	class := classifyQueryCost(expr, timing, strictStrategy)
	return applyCachedSelectorEstimates(class, extractSelectorSignatures(expr, timing), h.selectorStats, time.Now().UTC())
}

func (h *queryService) entireQueryDelegationForQuery(query string) *local.DelegationClassifierResult {
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return nil
	}
	result := local.ClassifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion)
	return &result
}

func (h *queryService) buildInstantPlan(req httpapi.InstantQueryRequest) (string, time.Time, local.Plan, *nativeplan.Analysis, *httpapi.APIError) {
	query := req.Query
	if query == "" {
		return "", time.Time{}, nil, nil, local.BadRequestHTTPError("missing required parameter 'query'")
	}
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return "", time.Time{}, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	evaluationTime := time.Now().UTC()
	if req.Time != "" {
		evaluationTime, err = model.ParsePrometheusTimestamp(req.Time)
		if err != nil {
			return "", time.Time{}, nil, nil, local.BadRequestHTTPError(err.Error())
		}
	}
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return "", time.Time{}, nil, nil, apiErr
	}
	ctx := local.PlanContext{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime, ClickHouseVersion: h.opts.ClickHouseVersion, NativeLoweringMode: mode, PreferNativeAggregationPushdown: mode.EnablesNativePlanning(), EnableNativeGridFunctions: h.opts.NativeGridFunctions == "prefer", EnableCumulativeAvgOverTime: h.opts.CumulativeAvgOverTime == "prefer", MaxRangePointsPerSeries: h.opts.MaxRangePointsPerSeries, RangeChunkPointsPerSeries: h.opts.RangeChunkPointsPerSeries}
	delegation := local.ClassifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion)
	var queryPlan local.Plan
	var analysis *nativeplan.Analysis
	if mode != local.NativeLoweringModeOff && delegation.Eligible && !mode.ForcesNativeRoot() && !mode.ForcesLocalRoot() && !h.opts.DisableEntireQueryDelegation {
		queryPlan, analysis, err = local.BuildEntireQueryDelegatedPlan(expr)
		if err != nil {
			return "", time.Time{}, nil, nil, local.ApiErrorToHTTP(err)
		}
	} else {
		queryPlan, analysis, err = local.BuildPlanWithContextAndAnalysis(expr, ctx)
		if err != nil {
			return "", time.Time{}, nil, nil, local.ApiErrorToHTTP(err)
		}
	}
	if mode.ForcesNativeRoot() {
		explain := local.ExplainPlan(queryPlan)
		if explain.Strategy != "native_sql" {
			return "", time.Time{}, nil, nil, local.ApiErrorToHTTP(local.NewUnsupportedErrorf("native lowering mode %q requires a native_sql root plan for %q, got %s", mode, query, explain.Strategy))
		}
	}
	return query, evaluationTime, queryPlan, analysis, nil
}

func (h *queryService) buildRangePlan(req httpapi.RangeQueryRequest) (string, time.Time, time.Time, time.Duration, local.Plan, *nativeplan.Analysis, *httpapi.APIError) {
	query := req.Query
	if query == "" {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError("missing required parameter 'query'")
	}
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	if expr.Type() != parser.ValueTypeScalar && expr.Type() != parser.ValueTypeVector {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError(fmt.Sprintf("invalid expression type %q for range query, must be scalar or instant vector", expr.Type()))
	}
	start, err := model.ParsePrometheusTimestamp(req.Start)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	end, err := model.ParsePrometheusTimestamp(req.End)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	if end.Before(start) {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError("end must be greater than or equal to start")
	}
	step, err := model.ParsePrometheusDuration(req.Step)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	if step <= 0 {
		return "", time.Time{}, time.Time{}, 0, nil, nil, local.BadRequestHTTPError("step must be greater than zero")
	}
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, apiErr
	}
	ctx := local.PlanContext{Mode: local.EvalModeRange, Start: start, End: end, Step: step, ClickHouseVersion: h.opts.ClickHouseVersion, NativeLoweringMode: mode, PreferNativeAggregationPushdown: mode.EnablesNativePlanning(), EnableNativeGridFunctions: h.opts.NativeGridFunctions == "prefer", EnableCumulativeAvgOverTime: h.opts.CumulativeAvgOverTime == "prefer", MaxRangePointsPerSeries: h.opts.MaxRangePointsPerSeries, RangeChunkPointsPerSeries: h.opts.RangeChunkPointsPerSeries, NativeRangeChunkPointsPerSeries: h.opts.NativeRangeChunkPointsPerSeries, NativeRangeChunkMaxDuration: h.opts.NativeRangeChunkMaxDuration, NativeRangeChunkMaxChunks: h.opts.NativeRangeChunkMaxChunks}
	delegation := local.ClassifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion)
	var queryPlan local.Plan
	var analysis *nativeplan.Analysis
	if mode != local.NativeLoweringModeOff && delegation.Eligible && !mode.ForcesNativeRoot() && !mode.ForcesLocalRoot() && !h.opts.DisableEntireQueryDelegation {
		queryPlan, analysis, err = local.BuildEntireQueryDelegatedPlan(expr)
		if err != nil {
			return "", time.Time{}, time.Time{}, 0, nil, nil, local.ApiErrorToHTTP(err)
		}
	} else {
		queryPlan, analysis, err = local.BuildPlanWithContextAndAnalysis(expr, ctx)
		if err != nil {
			return "", time.Time{}, time.Time{}, 0, nil, nil, local.ApiErrorToHTTP(err)
		}
	}
	if mode.ForcesNativeRoot() {
		explain := local.ExplainPlan(queryPlan)
		if explain.Strategy != "native_sql" {
			return "", time.Time{}, time.Time{}, 0, nil, nil, local.ApiErrorToHTTP(local.NewUnsupportedErrorf("native lowering mode %q requires a native_sql root plan for %q, got %s", mode, query, explain.Strategy))
		}
	}
	return query, start, end, step, queryPlan, analysis, nil
}

func physicalDecisionSummary(explain local.ExplainNode) string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 8)
	var walk func(local.ExplainNode)
	walk = func(n local.ExplainNode) {
		for _, d := range n.PhysicalDecisions {
			kind := strings.TrimSpace(d.Kind)
			if kind == "" {
				continue
			}
			strategy := strings.TrimSpace(d.Strategy)
			entry := kind
			if strategy != "" {
				entry = kind + "=" + strategy
			}
			if _, ok := seen[entry]; ok {
				continue
			}
			seen[entry] = struct{}{}
			parts = append(parts, entry)
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(explain)
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
