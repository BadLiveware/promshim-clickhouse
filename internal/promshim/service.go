package promshim

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/rules"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/shadow"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type queryService struct {
	opts                          Options
	client                        *storage.Client
	evaluator                     *local.Evaluator
	promotedTagColumns            map[string]struct{}
	timeSeriesIDType              string
	shadow                        *shadow.Runner
	selectorStats                 *selectorStatsCache
	selectorProbeSem              chan struct{}
	recordingRules                atomic.Pointer[rules.Registry]
	recordingRuleMode             rules.Mode
	recordingRuleNextReload       atomic.Int64
	recordingRuleReloadMu         sync.Mutex
	recordingRuleReloadErrors     atomic.Uint64
	recordingRuleReloadSuccess    atomic.Uint64
	recordingRuleExpansionMetrics atomic.Pointer[rules.ExpansionMetrics]
}

func parseMaterializeRuleSet(raw string) (map[string]bool, bool) {
	if raw == "" || raw == "off" {
		return nil, false
	}
	if raw == "all" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	set := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			set[part] = true
		}
	}
	return set, false
}

func (h *queryService) ClickHouseTransport() string {
	return string(h.opts.ClickHouseTransport)
}

func (h *queryService) Ready(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("clickhouse client is not initialized")
	}
	if h.opts.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.opts.RequestTimeout)
		defer cancel()
	}
	return h.client.Ping(ctx)
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
		MaxQuerySizeBytes:   opts.ClickHouseMaxQuerySizeBytes,
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
		Endpoint:              opts.ClickHouseEndpoint,
		NativeAddr:            opts.ClickHouseNativeAddr,
		Database:              opts.Database,
		Username:              opts.Username,
		Password:              opts.Password,
		Compression:           opts.ClickHouseCompression,
		RequestTimeout:        opts.RequestTimeout,
		Transport:             opts.ClickHouseTransport,
		MaxOpenConns:          opts.ClickHouseMaxOpenConns,
		MaxIdleConns:          opts.ClickHouseMaxIdleConns,
		ConnMaxLifetime:       opts.ClickHouseConnMaxLifetime,
		NativeSecure:          opts.ClickHouseNativeSecure,
		TLSInsecureSkipVerify: opts.ClickHouseTLSInsecureSkipVerify,
		TLSServerName:         opts.ClickHouseTLSServerName,
		SettingsProfile:       hSettingsProfileConfig(opts),
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
	ruleMode, err := rules.ParseMode(opts.RecordingRuleMode)
	if err != nil {
		return nil, err
	}
	ruleRegistry, err := loadRecordingRuleRegistry(ruleMode, opts.RecordingRuleFiles)
	if err != nil {
		return nil, err
	}
	service := &queryService{
		opts:               opts,
		client:             client,
		evaluator:          local.NewEvaluator(opts.Database, opts.Table, client).WithPromotedTagColumns(promotedTagColumns).WithNativeGridFunctions(opts.NativeGridFunctions == "prefer").WithCumulativeAvgOverTime(opts.CumulativeAvgOverTime == "prefer"),
		promotedTagColumns: promotedTagColumns,
		timeSeriesIDType:   timeSeriesIDType,
		selectorStats:      newSelectorStatsCache(5 * time.Minute),
		selectorProbeSem:   make(chan struct{}, 2),
		recordingRuleMode:  ruleMode,
	}
	service.recordingRules.Store(ruleRegistry)

	// Materialized recording rules live in a separate MergeTree table because
	// ClickHouse 26.3 (and earlier) doesn't support INSERT into TimeSeries.
	// The resolver routes leaf queries for materialized metrics to that table.
	service.evaluator.WithResolveTableOverride(func(name string) string {
		reg := service.currentRecordingRules()
		if opts.MaterializedRuleTable != "" && reg.IsMaterialized(name) {
			return opts.MaterializedRuleTable
		}
		return ""
	})

	service.scheduleNextRecordingRuleReload(time.Now())
	service.shadow = shadow.NewRunner(service)
	recordMetrics := rules.NewExpansionMetrics(service.shadow.Registry())
	ruleRegistry.SetExpansionMetrics(recordMetrics)
	service.recordingRuleExpansionMetrics.Store(recordMetrics)
	// Start materializer if configured.
	if opts.MaterializeRecordingRules != "" && opts.MaterializeRecordingRules != "off" {
		ruleSet, all := parseMaterializeRuleSet(opts.MaterializeRecordingRules)
		ruleRegistry.SetMaterializedRules(ruleSet, all)
		materializer := rules.NewMaterializer(ruleRegistry, func() *rules.Registry { return service.currentRecordingRules() }, func() error { return service.reloadRecordingRulesOnce() }, client, opts.Database, opts.Table, opts.MaterializedRuleTable, ruleSet)
		go materializer.Start(context.Background())
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", service.shadow.MetricsHandler())
	mux.Handle("/", httpapi.NewHandlerWithOptions(service, httpapi.HandlerOptions{HidePromQL: opts.HidePromQL}))
	return mux, nil
}

func (h *queryService) InstantQuery(ctx context.Context, req httpapi.InstantQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	ctx, cancel, apiErr := contextWithRequestTimeout(ctx, req.Timeout)
	if apiErr != nil {
		return nil, apiErr
	}
	defer cancel()
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
	query, evaluationTime, plan, analysis, recordingExpansions, apiErr := h.buildInstantPlan(req)
	_ = recordingExpansions
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForInstant(query, evaluationTime, mode, policy, explain.Strategy)
	selectedPlan, _, selectedExplain, routing := h.selectInstantPlanForRouting(req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &selectedExplain)
	if err := enforceEstimatedResponseLimits(routing, h.opts); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, selectedPlan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime})
	strictEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	value, apiErr = applyQueryLimit(value, req.Limit)
	if apiErr != nil {
		return nil, apiErr
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
			"data":                      map[string]any{"resultType": resultType, "result": result, "recordingRules": recordingExpansions},
			"plan":                      selectedExplain,
			"routing":                   routing,
		}}, nil
	}
	return &httpapi.Response{Strategy: selectedExplain.Strategy, FallbackReason: selectedExplain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, PhysicalDecisions: physicalDecisionSummary(selectedExplain), Stream: func(w http.ResponseWriter) error {
		return httpapi.WritePromSuccessInstantValue(w, value)
	}}, nil
}

func (h *queryService) RangeQuery(ctx context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
	ctx, cancel, apiErr := contextWithRequestTimeout(ctx, req.Timeout)
	if apiErr != nil {
		return nil, apiErr
	}
	defer cancel()
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
	query, start, end, step, plan, analysis, recordingExpansions, apiErr := h.buildRangePlan(ctx, req, true)
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForRange(query, start, end, step, mode, policy, explain.Strategy)
	selectedPlan, _, selectedExplain, routing := h.selectRangePlanForRouting(ctx, req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &selectedExplain)
	if err := enforceEstimatedResponseLimits(routing, h.opts); err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	evalStart := time.Now()
	value, err := h.evaluator.Evaluate(ctx, selectedPlan, local.EvalParams{Mode: local.EvalModeRange, Start: start, End: end, Step: step})
	strictEvalDuration := time.Since(evalStart)
	if err != nil {
		return nil, local.ApiErrorToHTTP(err)
	}
	value, apiErr = applyQueryLimit(value, req.Limit)
	if apiErr != nil {
		return nil, apiErr
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
			"data":                      map[string]any{"resultType": resultType, "result": result, "recordingRules": recordingExpansions},
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
	query, evaluationTime, plan, analysis, recordingExpansions, apiErr := h.buildInstantPlan(servedReq)
	_ = recordingExpansions
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
	value, apiErr = applyQueryLimit(value, req.Limit)
	if apiErr != nil {
		return nil, apiErr
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
	query, start, end, step, plan, analysis, recordingExpansions, apiErr := h.buildRangePlan(ctx, servedReq, false)
	_ = recordingExpansions
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
	value, apiErr = applyQueryLimit(value, req.Limit)
	if apiErr != nil {
		return nil, apiErr
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
	query, evaluationTime, plan, analysis, recordingExpansions, apiErr := h.buildInstantPlan(planReq)
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
			"recordingRules":            recordingExpansions,
			"query":                     query,
			"evaluationTime":            evaluationTime.UTC().Format(time.RFC3339Nano),
			"plan":                      explain,
			"routing":                   routing,
		},
	}}, nil
}

func (h *queryService) ExplainRange(ctx context.Context, req httpapi.RangeQueryRequest) (*httpapi.Response, *httpapi.APIError) {
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
	query, start, end, step, plan, analysis, recordingExpansions, apiErr := h.buildRangePlan(ctx, planReq, true)
	if apiErr != nil {
		return nil, apiErr
	}
	explain := local.ExplainPlanWithLowering(plan, analysis.Root)
	routing := h.routingInfoForRange(query, start, end, step, mode, policy, explain.Strategy)
	_, _, explain, routing = h.selectRangePlanForRouting(ctx, req, plan, analysis, routing)
	settingsProfile := h.applySettingsProfileProvenance(&routing, &explain)
	return &httpapi.Response{StatusCode: http.StatusOK, Strategy: explain.Strategy, FallbackReason: explain.FallbackReason, SettingsProfile: settingsProfile.Name, Routing: &routing, Body: map[string]any{
		"status": "success",
		"data": map[string]any{
			"mode":                      string(local.EvalModeRange),
			"nativeLoweringMode":        string(mode),
			"clickHouseTransport":       h.ClickHouseTransport(),
			"clickHouseSettingsProfile": settingsProfile,
			"entireQueryDelegation":     h.entireQueryDelegationForQuery(query),
			"recordingRules":            recordingExpansions,
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

	var selectorMetadata []recordingRuleSelector
	if h.recordingRuleMode == rules.ModeVirtual {
		selectorMetadata, apiErr = parseRecordingRuleMetadataSelectors(req.Matchers)
		if apiErr != nil {
			return nil, apiErr
		}
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

	virtualValues := h.recordingRuleLabelValues(req.Name, selectorMetadata)
	if len(virtualValues) > 0 {
		values = mergeMetadataValues(values, virtualValues)
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

type recordingRuleSelector struct {
	metricName string
	matchers   []*labels.Matcher
}

func (h *queryService) recordingRuleLabelValues(name string, selectors []recordingRuleSelector) []string {
	if h.recordingRuleMode != rules.ModeVirtual {
		return nil
	}
	registry := h.currentRecordingRules()
	if registry == nil || registry.Empty() {
		return nil
	}
	if len(selectors) == 0 {
		selectors = []recordingRuleSelector{{}}
	}
	seen := make(map[string]struct{})
	for _, selector := range selectors {
		candidates := registry.AllRules()
		if selector.metricName != "" {
			candidates = registry.Candidates(selector.metricName)
		}
		for _, rule := range candidates {
			if !recordingRuleSelectorMatches(rule, selector.matchers) {
				continue
			}
			if name == "__name__" {
				seen[rule.Name] = struct{}{}
				continue
			}
			if value, ok := mergedRuleLabels(rule)[name]; ok {
				seen[value] = struct{}{}
			}
		}
	}
	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func mergeMetadataValues(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, value := range base {
		seen[value] = struct{}{}
	}
	for _, value := range extra {
		seen[value] = struct{}{}
	}
	merged := make([]string, 0, len(seen))
	for value := range seen {
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return merged
}

func parseRecordingRuleMetadataSelectors(rawMatchers []string) ([]recordingRuleSelector, *httpapi.APIError) {
	if len(rawMatchers) == 0 {
		return nil, nil
	}
	selectors := make([]recordingRuleSelector, 0, len(rawMatchers))
	parserOpts := parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}
	for _, raw := range rawMatchers {
		expr, err := parser.NewParser(parserOpts).ParseExpr(raw)
		if err != nil {
			return nil, local.BadRequestHTTPError(err.Error())
		}
		vector, ok := expr.(*parser.VectorSelector)
		if !ok {
			return nil, local.BadRequestHTTPError(fmt.Sprintf("invalid metric selector %q", raw))
		}
		selectors = append(selectors, recordingRuleSelector{
			metricName: selectorMetadataMetricName(vector),
			matchers:   vector.LabelMatchers,
		})
	}
	return selectors, nil
}

func selectorMetadataMetricName(selector *parser.VectorSelector) string {
	if selector.Name != "" {
		return selector.Name
	}
	for _, matcher := range selector.LabelMatchers {
		if matcher != nil && matcher.Name == "__name__" && matcher.Type == labels.MatchEqual {
			return matcher.Value
		}
	}
	return ""
}

func recordingRuleSelectorMatches(rule rules.RecordingRule, matchers []*labels.Matcher) bool {
	staticLabels := mergedRuleLabels(rule)
	staticLabels["__name__"] = rule.Name
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		if value, ok := staticLabels[matcher.Name]; ok {
			if !matcher.Matches(value) {
				return false
			}
		}
	}
	return true
}

func mergedRuleLabels(rule rules.RecordingRule) map[string]string {
	out := map[string]string{}
	for k, v := range rule.GroupLabels {
		out[k] = v
	}
	for k, v := range rule.Labels {
		out[k] = v
	}
	return out
}

func enforceEstimatedResponseLimits(routing httpapi.RoutingInfo, opts Options) error {
	if !routing.EstimatesAvailable {
		return nil
	}
	if opts.MaxResponseSeries > 0 && routing.Class.EstimatedSeries > opts.MaxResponseSeries {
		return local.NewBadDataErrorf("query result is estimated to return %d series, exceeding configured limit %d", routing.Class.EstimatedSeries, opts.MaxResponseSeries)
	}
	if opts.MaxResponsePoints > 0 && routing.Class.EstimatedOutputPoints > opts.MaxResponsePoints {
		return local.NewBadDataErrorf("query result is estimated to return %d points, exceeding configured limit %d", routing.Class.EstimatedOutputPoints, opts.MaxResponsePoints)
	}
	return nil
}

func contextWithRequestTimeout(ctx context.Context, rawTimeout string) (context.Context, context.CancelFunc, *httpapi.APIError) {
	if rawTimeout == "" {
		return ctx, func() {}, nil
	}
	timeout, err := model.ParsePrometheusDuration(rawTimeout)
	if err != nil {
		return nil, nil, local.BadRequestHTTPError(fmt.Sprintf("invalid parameter \"timeout\": %v", err))
	}
	if timeout <= 0 {
		return nil, nil, local.BadRequestHTTPError("invalid parameter \"timeout\": timeout must be greater than zero")
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	return child, cancel, nil
}

func applyQueryLimit(value model.RuntimeValue, rawLimit string) (model.RuntimeValue, *httpapi.APIError) {
	if rawLimit == "" || rawLimit == "0" {
		return value, nil
	}
	limit, err := strconv.ParseInt(rawLimit, 10, 64)
	if err != nil || limit < 0 {
		return nil, local.BadRequestHTTPError("invalid parameter \"limit\": expected non-negative integer")
	}
	if limit == 0 {
		return value, nil
	}
	switch typed := value.(type) {
	case model.VectorValue:
		if int64(len(typed.Samples)) > limit {
			typed.Samples = typed.Samples[:int(limit)]
		}
		return typed, nil
	case model.MatrixValue:
		if int64(len(typed.Series)) > limit {
			typed.Series = typed.Series[:int(limit)]
		}
		return typed, nil
	default:
		return value, nil
	}
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
		if !h.opts.AllowRequestRoutingOverrides {
			return "", local.BadRequestHTTPError("native_lowering_mode request override is disabled")
		}
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
		if !h.opts.AllowRequestRoutingOverrides {
			return "", local.BadRequestHTTPError("routing_policy request override is disabled")
		}
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
	_, _, candidatePlan, candidateAnalysis, _, candidateErr := h.buildInstantPlan(candidateReq)
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

func (h *queryService) selectRangePlanForRouting(ctx context.Context, req httpapi.RangeQueryRequest, plan local.Plan, analysis *nativeplan.Analysis, routing httpapi.RoutingInfo) (local.Plan, *nativeplan.Analysis, local.ExplainNode, httpapi.RoutingInfo) {
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
	_, _, _, _, candidatePlan, candidateAnalysis, _, candidateErr := h.buildRangePlan(ctx, candidateReq, false)
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
	result := local.ClassifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion, h.recordingRuleMetricNames())
	return &result
}

// recordingRuleMetricNames returns the set of all recording rule metric names.
// Queries that reference these names must not be delegated to ClickHouse's
// PromQL endpoint: virtual rules need expansion, materialized rules query
// a separate MergeTree table that ClickHouse PromQL cannot see.
func (h *queryService) recordingRuleMetricNames() map[string]bool {
	if h.recordingRuleMode != rules.ModeVirtual {
		return nil
	}
	registry := h.currentRecordingRules()
	if registry.Empty() {
		return nil
	}
	all := registry.Rules()
	names := make(map[string]bool, len(all))
	for name := range all {
		names[name] = true
	}
	return names
}

func loadRecordingRuleRegistry(mode rules.Mode, patterns []string) (*rules.Registry, error) {
	if mode != rules.ModeVirtual || len(patterns) == 0 {
		return rules.EmptyRegistry(), nil
	}
	files, err := expandRecordingRuleFilePatterns(patterns)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return rules.EmptyRegistry(), nil
	}
	loadedRegistry, err := rules.LoadFiles(files)
	if err != nil {
		return nil, err
	}
	if len(loadedRegistry.Errors()) > 0 {
		return nil, fmt.Errorf("loading recording rules: %v", loadedRegistry.Errors())
	}
	return loadedRegistry, nil
}

func expandRecordingRuleFilePatterns(patterns []string) ([]string, error) {
	var files []string
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid recording rule file glob %q: %w", pattern, err)
		}
		if len(matches) == 0 && !hasGlobMeta(pattern) {
			return nil, fmt.Errorf("recording rule file %q does not exist", pattern)
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	return files, nil
}

func hasGlobMeta(pattern string) bool {
	for _, r := range pattern {
		switch r {
		case '*', '?', '[':
			return true
		}
	}
	return false
}

func (h *queryService) currentRecordingRules() *rules.Registry {
	if h == nil {
		return nil
	}
	return h.recordingRules.Load()
}

func (h *queryService) scheduleNextRecordingRuleReload(now time.Time) {
	if h.recordingRuleMode != rules.ModeVirtual || len(h.opts.RecordingRuleFiles) == 0 || h.opts.RecordingRuleReloadInterval <= 0 {
		return
	}
	h.recordingRuleNextReload.Store(now.Add(h.opts.RecordingRuleReloadInterval).UnixNano())
}

func (h *queryService) reloadRecordingRulesOnce() error {
	registry, err := loadRecordingRuleRegistry(h.recordingRuleMode, h.opts.RecordingRuleFiles)
	if err != nil {
		h.recordingRuleReloadErrors.Add(1)
		return err
	}
	// Reapply materialization flags to the new registry so query-time bypass
	// and metrics survive rule reloads.
	if h.opts.MaterializeRecordingRules != "" && h.opts.MaterializeRecordingRules != "off" {
		ruleSet, all := parseMaterializeRuleSet(h.opts.MaterializeRecordingRules)
		registry.SetMaterializedRules(ruleSet, all)
	}
	if metrics := h.recordingRuleExpansionMetrics.Load(); metrics != nil {
		registry.SetExpansionMetrics(metrics)
	}
	h.recordingRules.Store(registry)
	h.recordingRuleReloadSuccess.Add(1)
	return nil
}

func (h *queryService) maybeReloadRecordingRules(now time.Time) {
	next := h.recordingRuleNextReload.Load()
	if next == 0 || now.UnixNano() < next {
		return
	}
	h.recordingRuleReloadMu.Lock()
	defer h.recordingRuleReloadMu.Unlock()
	next = h.recordingRuleNextReload.Load()
	if next == 0 || now.UnixNano() < next {
		return
	}
	if err := h.reloadRecordingRulesOnce(); err != nil {
		h.scheduleNextRecordingRuleReload(now)
		log.Printf("promshim: recording rule reload failed; keeping previous registry: %v (failures=%d)", err, h.recordingRuleReloadErrors.Load())
		return
	}
	h.scheduleNextRecordingRuleReload(now)
	registry := h.currentRecordingRules()
	log.Printf("promshim: recording rule reload succeeded: %d rules (successes=%d)", registry.Len(), h.recordingRuleReloadSuccess.Load())
}

func (h *queryService) expandRecordingRules(expr parser.Expr) (parser.Expr, []rules.Expansion, *httpapi.APIError) {
	h.maybeReloadRecordingRules(time.Now())
	registry := h.currentRecordingRules()
	if h.recordingRuleMode != rules.ModeVirtual || registry.Empty() {
		return expr, nil, nil
	}
	result, err := rules.ExpandExpr(expr, registry)
	if err != nil {
		return nil, nil, local.BadRequestHTTPError(err.Error())
	}
	return result.Expr, result.Expansions, nil
}

func (h *queryService) buildInstantPlan(req httpapi.InstantQueryRequest) (string, time.Time, local.Plan, *nativeplan.Analysis, []rules.Expansion, *httpapi.APIError) {
	query := req.Query
	if query == "" {
		return "", time.Time{}, nil, nil, nil, local.BadRequestHTTPError("missing required parameter 'query'")
	}
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return "", time.Time{}, nil, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	expr, recordingExpansions, apiErr := h.expandRecordingRules(expr)
	if apiErr != nil {
		return "", time.Time{}, nil, nil, nil, apiErr
	}
	evaluationTime := time.Now().UTC()
	if req.Time != "" {
		evaluationTime, err = model.ParsePrometheusTimestamp(req.Time)
		if err != nil {
			return "", time.Time{}, nil, nil, nil, local.BadRequestHTTPError(err.Error())
		}
	}
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return "", time.Time{}, nil, nil, nil, apiErr
	}
	ctx := local.PlanContext{Mode: local.EvalModeInstant, EvaluationTime: evaluationTime, ClickHouseVersion: h.opts.ClickHouseVersion, NativeLoweringMode: mode, PreferNativeAggregationPushdown: mode.EnablesNativePlanning(), EnableNativeGridFunctions: h.opts.NativeGridFunctions == "prefer", EnableCumulativeAvgOverTime: h.opts.CumulativeAvgOverTime == "prefer", MaxRangePointsPerSeries: h.opts.MaxRangePointsPerSeries, RangeChunkPointsPerSeries: h.opts.RangeChunkPointsPerSeries}
	delegation := local.ClassifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion, h.recordingRuleMetricNames())
	// Recording rules must never be delegated: virtual rules need expansion,
	// materialized rules live in a separate MergeTree table CH can't see.
	if delegation.Eligible {
		if names := h.recordingRuleMetricNames(); len(names) > 0 {
			parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
				if vs, ok := node.(*parser.VectorSelector); ok && names[vs.Name] {
					delegation = local.DelegationClassifierResult{Eligible: false, Reason: "query references recording rule(s)", ClickHouseVersion: h.opts.ClickHouseVersion}
				}
				return nil
			})
		}
	}
	var queryPlan local.Plan
	var analysis *nativeplan.Analysis
	if mode != local.NativeLoweringModeOff && delegation.Eligible && !mode.ForcesNativeRoot() && !mode.ForcesLocalRoot() && !h.opts.DisableEntireQueryDelegation {
		queryPlan, analysis, err = local.BuildEntireQueryDelegatedPlan(expr)
		if err != nil {
			return "", time.Time{}, nil, nil, nil, local.ApiErrorToHTTP(err)
		}
	} else {
		queryPlan, analysis, err = local.BuildPlanWithContextAndAnalysis(expr, ctx)
		if err != nil {
			return "", time.Time{}, nil, nil, nil, local.ApiErrorToHTTP(err)
		}
	}
	if mode.ForcesNativeRoot() {
		explain := local.ExplainPlan(queryPlan)
		if !nativeSQLRootStrategy(explain.Strategy) {
			return "", time.Time{}, nil, nil, nil, local.ApiErrorToHTTP(local.NewUnsupportedErrorf("native lowering mode %q requires a native_sql root plan for %q, got %s", mode, query, explain.Strategy))
		}
	}
	return query, evaluationTime, queryPlan, analysis, recordingExpansions, nil
}

func (h *queryService) buildRangePlan(ctx context.Context, req httpapi.RangeQueryRequest, applyPreflight bool) (string, time.Time, time.Time, time.Duration, local.Plan, *nativeplan.Analysis, []rules.Expansion, *httpapi.APIError) {
	query := req.Query
	if query == "" {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError("missing required parameter 'query'")
	}
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	expr, recordingExpansions, apiErr := h.expandRecordingRules(expr)
	if apiErr != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, apiErr
	}
	if expr.Type() != parser.ValueTypeScalar && expr.Type() != parser.ValueTypeVector {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError(fmt.Sprintf("invalid expression type %q for range query, must be scalar or instant vector", expr.Type()))
	}
	start, err := model.ParsePrometheusTimestamp(req.Start)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	end, err := model.ParsePrometheusTimestamp(req.End)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	if end.Before(start) {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError("end must be greater than or equal to start")
	}
	step, err := model.ParsePrometheusDuration(req.Step)
	if err != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError(err.Error())
	}
	if step <= 0 {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.BadRequestHTTPError("step must be greater than zero")
	}
	mode, apiErr := h.nativeLoweringModeForRequest(req.NativeLoweringMode)
	if apiErr != nil {
		return "", time.Time{}, time.Time{}, 0, nil, nil, nil, apiErr
	}
	planCtx := local.PlanContext{Mode: local.EvalModeRange, Start: start, End: end, Step: step, ClickHouseVersion: h.opts.ClickHouseVersion, NativeLoweringMode: mode, PreferNativeAggregationPushdown: mode.EnablesNativePlanning(), EnableNativeGridFunctions: h.opts.NativeGridFunctions == "prefer", EnableCumulativeAvgOverTime: h.opts.CumulativeAvgOverTime == "prefer", MaxRangePointsPerSeries: h.opts.MaxRangePointsPerSeries, RangeChunkPointsPerSeries: h.opts.RangeChunkPointsPerSeries, NativeRangeChunkPointsPerSeries: h.opts.NativeRangeChunkPointsPerSeries, NativeRangeChunkMaxDuration: h.opts.NativeRangeChunkMaxDuration, NativeRangeChunkMaxChunks: h.opts.NativeRangeChunkMaxChunks, NativeRangePreflightSeriesThreshold: h.opts.NativeRangePreflightSeriesThreshold, NativeRangePreflightTimeout: h.opts.NativeRangePreflightTimeout, NativeRangePreflightMaxMemoryUsage: h.opts.NativeRangePreflightMaxMemoryUsage}
	delegation := local.ClassifyEntireQueryDelegation(expr, h.opts.ClickHouseVersion, h.recordingRuleMetricNames())
	var queryPlan local.Plan
	var analysis *nativeplan.Analysis
	if mode != local.NativeLoweringModeOff && delegation.Eligible && !mode.ForcesNativeRoot() && !mode.ForcesLocalRoot() && !h.opts.DisableEntireQueryDelegation {
		queryPlan, analysis, err = local.BuildEntireQueryDelegatedPlan(expr)
		if err != nil {
			return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.ApiErrorToHTTP(err)
		}
	} else {
		queryPlan, analysis, err = local.BuildPlanWithContextAndAnalysis(expr, planCtx)
		if err != nil {
			return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.ApiErrorToHTTP(err)
		}
	}
	if applyPreflight {
		queryPlan = local.ApplyNativeRangePreflight(ctx, h.client, h.queryConfig(), queryPlan)
	}
	if mode.ForcesNativeRoot() {
		explain := local.ExplainPlan(queryPlan)
		if !nativeSQLRootStrategy(explain.Strategy) {
			return "", time.Time{}, time.Time{}, 0, nil, nil, nil, local.ApiErrorToHTTP(local.NewUnsupportedErrorf("native lowering mode %q requires a native_sql root plan for %q, got %s", mode, query, explain.Strategy))
		}
	}
	return query, start, end, step, queryPlan, analysis, recordingExpansions, nil
}

func nativeSQLRootStrategy(strategy string) bool {
	return strategy == "native_sql" || strategy == "chunked_native"
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
