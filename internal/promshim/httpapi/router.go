package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type APIError struct {
	StatusCode int    `json:"-"`
	ErrorType  string `json:"errorType"`
	Error      string `json:"error"`
}

type Response struct {
	StatusCode int
	Body       any
	Stream     func(http.ResponseWriter) error
	// Strategy and FallbackReason surface the root plan strategy on the
	// successful response as X-Promshim-Strategy / X-Promshim-Fallback-Reason
	// headers. They are advisory — producers may leave them unset on endpoints
	// where the concept does not apply (labels, series, label_values).
	Strategy        string
	FallbackReason  string
	SettingsProfile string
	Routing         *RoutingInfo
	// PhysicalDecisions is a compact strategy-decision summary emitted as
	// X-Promshim-Physical-Decisions for bench/evidence workflows.
	PhysicalDecisions string
}

type EstimateState struct {
	Source        string `json:"source"`
	Fresh         bool   `json:"fresh"`
	GeneratedAt   string `json:"generatedAt,omitempty"`
	TTLSeconds    int64  `json:"ttlSeconds,omitempty"`
	SelectorCount int    `json:"selectorCount,omitempty"`
	Missing       int    `json:"missing,omitempty"`
	Stale         int    `json:"stale,omitempty"`
}

type QueryCostClass struct {
	Endpoint                     string        `json:"endpoint"`
	Family                       string        `json:"family"`
	RootStrategyStrict           string        `json:"rootStrategyStrict"`
	OutputKind                   string        `json:"outputKind"`
	EstimateState                EstimateState `json:"estimateState"`
	HasAggregation               bool          `json:"hasAggregation"`
	HasRangeFunction             bool          `json:"hasRangeFunction"`
	HasRepeatedRangeFunc         bool          `json:"hasRepeatedRangeFunction"`
	HasVectorJoin                bool          `json:"hasVectorJoin"`
	HasHistogram                 bool          `json:"hasHistogram"`
	HasSubquery                  bool          `json:"hasSubquery"`
	HasLabelMutation             bool          `json:"hasLabelMutation"`
	HasSelectionAgg              bool          `json:"hasSelectionAgg"`
	DropsAllLabels               bool          `json:"dropsAllLabels"`
	HistogramChildGroupingLabels []string      `json:"histogramChildGroupingLabels,omitempty"`
	HistogramChildGroupsByLeOnly bool          `json:"histogramChildGroupsByLeOnly,omitempty"`
	SelectorCount                int           `json:"selectorCount"`
	EstimatedSeries              int64         `json:"estimatedSeries"`
	EstimatedInputSamples        int64         `json:"estimatedInputSamples"`
	EstimatedOutputPoints        int64         `json:"estimatedOutputPoints"`
	RangePointsPerSeries         int64         `json:"rangePointsPerSeries"`
	LookbackMS                   int64         `json:"lookbackMs"`
	StepMS                       int64         `json:"stepMs"`
	SubqueryRangeMS              int64         `json:"subqueryRangeMs,omitempty"`
	SubqueryStepMS               int64         `json:"subqueryStepMs,omitempty"`
	SubqueryPointsPerEval        int64         `json:"subqueryPointsPerEval,omitempty"`
	SubqueryOverlapSlots         float64       `json:"subqueryOverlapSlots,omitempty"`
	SubqueryWorkUnits            int64         `json:"subqueryWorkUnits,omitempty"`
	SubqueryTemporalFanout       int64         `json:"subqueryTemporalFanout,omitempty"`
	SubqueryComplexityBand       string        `json:"subqueryComplexityBand,omitempty"`
	OverlapSlots                 float64       `json:"overlapSlots"`
	NativeRoundTrips             int           `json:"nativeRoundTrips"`
	LocalRoundTrips              int           `json:"localRoundTrips"`
}

type RoutingCost struct {
	Native float64 `json:"native"`
	Local  float64 `json:"local"`
	Unit   string  `json:"unit"`
}

type RoutingCapEvaluation struct {
	Name     string  `json:"name"`
	Estimate int64   `json:"estimate"`
	Limit    int64   `json:"limit"`
	Exceeded bool    `json:"exceeded"`
	OverBy   int64   `json:"overBy,omitempty"`
	Usage    float64 `json:"usage"`
	Unit     string  `json:"unit"`
	Scope    string  `json:"scope,omitempty"`
}

type CandidateEstimate struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
	Unit  string `json:"unit"`
	Scope string `json:"scope,omitempty"`
}

type CandidateCost struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type ExecutionCandidate struct {
	ID                 string                 `json:"id"`
	Tier               string                 `json:"tier"`
	Strategy           string                 `json:"strategy"`
	Family             string                 `json:"family"`
	Strict             bool                   `json:"strict"`
	Selected           bool                   `json:"selected"`
	Served             bool                   `json:"served"`
	Supported          bool                   `json:"supported"`
	KnownCorrect       bool                   `json:"knownCorrect"`
	Eligible           bool                   `json:"eligible"`
	RejectReasons      []string               `json:"rejectReasons,omitempty"`
	EstimatesAvailable bool                   `json:"estimatesAvailable"`
	EstimatedCost      *CandidateCost         `json:"estimatedCost,omitempty"`
	Estimates          []CandidateEstimate    `json:"estimates,omitempty"`
	CapEvaluations     []RoutingCapEvaluation `json:"capEvaluations,omitempty"`
	Advisory           []string               `json:"advisory,omitempty"`
	SettingsProfile    string                 `json:"settingsProfile,omitempty"`
}

type CandidateDecision struct {
	StrictCandidate   string `json:"strictCandidate"`
	SelectedCandidate string `json:"selectedCandidate"`
	ServedCandidate   string `json:"servedCandidate"`
}

type RoutingInfo struct {
	Policy             string                 `json:"policy"`
	StrictStrategy     string                 `json:"strictStrategy"`
	SelectedStrategy   string                 `json:"selectedStrategy"`
	WouldSelect        string                 `json:"wouldSelect"`
	Decision           string                 `json:"decision"`
	Reason             string                 `json:"reason"`
	EstimatesAvailable bool                   `json:"estimatesAvailable"`
	MissingEstimates   []string               `json:"missingEstimates,omitempty"`
	Cost               *RoutingCost           `json:"cost,omitempty"`
	Caps               map[string]int64       `json:"caps,omitempty"`
	CapHits            []string               `json:"capHits,omitempty"`
	CapEvaluations     []RoutingCapEvaluation `json:"capEvaluations,omitempty"`
	EnabledFamilies    []string               `json:"enabledFamilies,omitempty"`
	CandidateDecision  *CandidateDecision     `json:"candidateDecision,omitempty"`
	Candidates         []ExecutionCandidate   `json:"candidates,omitempty"`
	Class              QueryCostClass         `json:"class"`
	Advisory           []string               `json:"advisory,omitempty"`
	SettingsProfile    string                 `json:"settingsProfile,omitempty"`
}

type InstantQueryRequest struct {
	Query              string
	Time               string
	Limit              string
	Timeout            string
	Stats              string
	LookbackDelta      string
	Explain            bool
	NativeLoweringMode string
	RoutingPolicy      string
}

type RangeQueryRequest struct {
	Query              string
	Start              string
	End                string
	Step               string
	Limit              string
	Timeout            string
	Stats              string
	LookbackDelta      string
	Explain            bool
	NativeLoweringMode string
	RoutingPolicy      string
}

type MetadataRequest struct {
	Matchers []string
	Start    string
	End      string
}

type LabelValuesRequest struct {
	Name string
	MetadataRequest
}

type Service interface {
	InstantQuery(context.Context, InstantQueryRequest) (*Response, *APIError)
	RangeQuery(context.Context, RangeQueryRequest) (*Response, *APIError)
	ExplainInstant(context.Context, InstantQueryRequest) (*Response, *APIError)
	ExplainRange(context.Context, RangeQueryRequest) (*Response, *APIError)
	Labels(context.Context, MetadataRequest) (*Response, *APIError)
	LabelValues(context.Context, LabelValuesRequest) (*Response, *APIError)
	Series(context.Context, MetadataRequest) (*Response, *APIError)
	Ready(context.Context) error
}

type clickHouseTransporter interface {
	ClickHouseTransport() string
}

const maxRequestLogCommentLen = 256

type HandlerOptions struct {
	HidePromQL bool
}

type Handler struct {
	service    Service
	mux        *http.ServeMux
	hidePromQL bool
}

func NewHandler(service Service) http.Handler {
	return NewHandlerWithOptions(service, HandlerOptions{})
}

func NewHandlerWithOptions(service Service, opts HandlerOptions) http.Handler {
	h := &Handler{service: service, mux: http.NewServeMux(), hidePromQL: opts.HidePromQL}
	h.mux.HandleFunc("OPTIONS /", h.handleOptions)
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /-/healthy", h.handleHealthy)
	h.mux.HandleFunc("GET /-/ready", h.handleReady)
	h.mux.HandleFunc("GET /api/v1/query", h.handleQuery)
	h.mux.HandleFunc("POST /api/v1/query", h.handleQuery)
	h.mux.HandleFunc("GET /api/v1/query_range", h.handleQueryRange)
	h.mux.HandleFunc("POST /api/v1/query_range", h.handleQueryRange)
	h.mux.HandleFunc("GET /api/v1/query_explain", h.handleQueryExplain)
	h.mux.HandleFunc("POST /api/v1/query_explain", h.handleQueryExplain)
	h.mux.HandleFunc("GET /api/v1/query_range_explain", h.handleQueryRangeExplain)
	h.mux.HandleFunc("POST /api/v1/query_range_explain", h.handleQueryRangeExplain)
	h.mux.HandleFunc("GET /api/v1/labels", h.handleLabels)
	h.mux.HandleFunc("POST /api/v1/labels", h.handleLabels)
	h.mux.HandleFunc("GET /api/v1/label/{name}/values", h.handleLabelValues)
	h.mux.HandleFunc("GET /api/v1/series", h.handleSeries)
	h.mux.HandleFunc("POST /api/v1/series", h.handleSeries)
	h.mux.HandleFunc("GET /api/v1/metadata", h.handleMetadata)
	h.mux.HandleFunc("GET /api/v1/targets", h.handleTargets)
	h.mux.HandleFunc("GET /api/v1/rules", h.handleRules)
	h.mux.HandleFunc("GET /api/v1/alerts", h.handleAlerts)
	h.mux.HandleFunc("GET /api/v1/format_query", h.handleFormatQuery)
	h.mux.HandleFunc("POST /api/v1/format_query", h.handleFormatQuery)
	h.mux.HandleFunc("GET /api/v1/parse_query", h.handleParseQuery)
	h.mux.HandleFunc("POST /api/v1/parse_query", h.handleParseQuery)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.serveHTTP(w, r)
}

func (h *Handler) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", allowedMethodsForPath(r.URL.Path))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleHealthy(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Ready(r.Context()); err != nil {
		writePlain(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	writePlain(w, http.StatusOK, "ready")
}

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return
	}
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query", values)))
	resp, apiErr := h.service.InstantQuery(ctx, InstantQueryRequest{
		Query:              values.Get("query"),
		Time:               values.Get("time"),
		Limit:              values.Get("limit"),
		Timeout:            values.Get("timeout"),
		Stats:              values.Get("stats"),
		LookbackDelta:      values.Get("lookback_delta"),
		Explain:            wantsExplain(values),
		NativeLoweringMode: values.Get("native_lowering_mode"),
		RoutingPolicy:      values.Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return
	}
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query_range", values)))
	resp, apiErr := h.service.RangeQuery(ctx, RangeQueryRequest{
		Query:              values.Get("query"),
		Start:              values.Get("start"),
		End:                values.Get("end"),
		Step:               values.Get("step"),
		Limit:              values.Get("limit"),
		Timeout:            values.Get("timeout"),
		Stats:              values.Get("stats"),
		LookbackDelta:      values.Get("lookback_delta"),
		Explain:            wantsExplain(values),
		NativeLoweringMode: values.Get("native_lowering_mode"),
		RoutingPolicy:      values.Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleQueryExplain(w http.ResponseWriter, r *http.Request) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return
	}
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query_explain", values)))
	resp, apiErr := h.service.ExplainInstant(ctx, InstantQueryRequest{
		Query:              values.Get("query"),
		Time:               values.Get("time"),
		Limit:              values.Get("limit"),
		Timeout:            values.Get("timeout"),
		Stats:              values.Get("stats"),
		LookbackDelta:      values.Get("lookback_delta"),
		Explain:            true,
		NativeLoweringMode: values.Get("native_lowering_mode"),
		RoutingPolicy:      values.Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleQueryRangeExplain(w http.ResponseWriter, r *http.Request) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return
	}
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query_range_explain", values)))
	resp, apiErr := h.service.ExplainRange(ctx, RangeQueryRequest{
		Query:              values.Get("query"),
		Start:              values.Get("start"),
		End:                values.Get("end"),
		Step:               values.Get("step"),
		Limit:              values.Get("limit"),
		Timeout:            values.Get("timeout"),
		Stats:              values.Get("stats"),
		LookbackDelta:      values.Get("lookback_delta"),
		Explain:            true,
		NativeLoweringMode: values.Get("native_lowering_mode"),
		RoutingPolicy:      values.Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleLabels(w http.ResponseWriter, r *http.Request) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return
	}
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "labels", values)))
	resp, apiErr := h.service.Labels(ctx, metadataRequestFromValues(values))
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "label_values", values)))
	resp, apiErr := h.service.LabelValues(ctx, LabelValuesRequest{
		Name:            r.PathValue("name"),
		MetadataRequest: metadataRequestFromValues(values),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return
	}
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "series", values)))
	resp, apiErr := h.service.Series(ctx, metadataRequestFromValues(values))
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleMetadata(w http.ResponseWriter, _ *http.Request) {
	writePromSuccess(w, map[string]any{})
}

func (h *Handler) handleTargets(w http.ResponseWriter, _ *http.Request) {
	writePromSuccess(w, map[string]any{"activeTargets": []any{}, "droppedTargets": []any{}, "droppedTargetCounts": map[string]int{}})
}

func (h *Handler) handleRules(w http.ResponseWriter, _ *http.Request) {
	writePromSuccess(w, map[string]any{"groups": []any{}})
}

func (h *Handler) handleAlerts(w http.ResponseWriter, _ *http.Request) {
	writePromSuccess(w, map[string]any{"alerts": []any{}})
}

func (h *Handler) handleFormatQuery(w http.ResponseWriter, r *http.Request) {
	expr, ok := parsePromQLRequestExpression(w, r)
	if !ok {
		return
	}
	writePromSuccess(w, expr.Pretty(0))
}

func (h *Handler) handleParseQuery(w http.ResponseWriter, r *http.Request) {
	expr, ok := parsePromQLRequestExpression(w, r)
	if !ok {
		return
	}
	writePromSuccess(w, translatePromQLAST(expr))
}

func (h *Handler) clickHouseTransport() string {
	if provider, ok := h.service.(clickHouseTransporter); ok {
		return provider.ClickHouseTransport()
	}
	return ""
}

func writeServiceResult(w http.ResponseWriter, resp *Response, apiErr *APIError, metrics *obs.CHMetrics, clickHouseTransport string) {
	if apiErr != nil {
		writePromError(w, *apiErr)
		return
	}
	if resp == nil {
		writePromError(w, APIError{StatusCode: http.StatusInternalServerError, ErrorType: "execution", Error: "service returned no response"})
		return
	}
	setPromshimHeaders(w, resp, metrics, clickHouseTransport)
	if resp.Stream != nil {
		if err := resp.Stream(w); err != nil {
			writePromError(w, APIError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()})
		}
		return
	}
	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	writeJSON(w, statusCode, resp.Body)
}

func setPromshimHeaders(w http.ResponseWriter, resp *Response, metrics *obs.CHMetrics, clickHouseTransport string) {
	if resp.Strategy != "" {
		w.Header().Set("X-Promshim-Strategy", resp.Strategy)
	}
	if resp.FallbackReason != "" {
		w.Header().Set("X-Promshim-Fallback-Reason", resp.FallbackReason)
	}
	if resp.SettingsProfile != "" {
		w.Header().Set("X-Promshim-Settings-Profile", resp.SettingsProfile)
	}
	if resp.PhysicalDecisions != "" {
		w.Header().Set("X-Promshim-Physical-Decisions", resp.PhysicalDecisions)
	}
	if resp.Routing != nil {
		w.Header().Set("X-Promshim-Routing-Policy", resp.Routing.Policy)
		w.Header().Set("X-Promshim-Routing-Decision", resp.Routing.Decision)
		w.Header().Set("X-Promshim-Strict-Strategy", resp.Routing.StrictStrategy)
		w.Header().Set("X-Promshim-Selected-Strategy", resp.Routing.SelectedStrategy)
		w.Header().Set("X-Promshim-Routing-Reason", resp.Routing.Reason)
		if resp.Routing.CandidateDecision != nil {
			w.Header().Set("X-Promshim-Strict-Candidate", resp.Routing.CandidateDecision.StrictCandidate)
			w.Header().Set("X-Promshim-Selected-Candidate", resp.Routing.CandidateDecision.SelectedCandidate)
			w.Header().Set("X-Promshim-Served-Candidate", resp.Routing.CandidateDecision.ServedCandidate)
		}
		if resp.Routing.Class.Family != "" {
			w.Header().Set("X-Promshim-Cost-Family", resp.Routing.Class.Family)
		}
	}
	if clickHouseTransport != "" {
		w.Header().Set("X-Promshim-CH-Transport", clickHouseTransport)
	}
	if metrics != nil {
		w.Header().Set("X-Promshim-CH-Roundtrips", strconv.FormatInt(metrics.Roundtrips(), 10))
		w.Header().Set("X-Promshim-CH-Millis", strconv.FormatInt(metrics.Millis(), 10))
	}
}

func metadataRequestFromValues(values url.Values) MetadataRequest {
	return MetadataRequest{
		Matchers: readMatchers(values),
		Start:    values.Get("start"),
		End:      values.Get("end"),
	}
}

func readMatchers(values url.Values) []string {
	matchers := values["match[]"]
	if len(matchers) > 0 {
		return matchers
	}
	return values["match"]
}

func parseRequestValues(_ http.ResponseWriter, r *http.Request) (url.Values, bool) {
	// Prometheus reads request parameters with http.Request.FormValue, whose
	// ParseForm call intentionally ignores malformed query/form errors. Preserve
	// that compatibility so unused malformed parameters do not reject a request.
	_ = r.ParseForm()
	return r.Form, true
}

func parsePromQLRequestExpression(w http.ResponseWriter, r *http.Request) (parser.Expr, bool) {
	values, ok := parseRequestValues(w, r)
	if !ok {
		return nil, false
	}
	query := values.Get("query")
	if query == "" {
		writePromError(w, APIError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: "missing required parameter 'query'"})
		return nil, false
	}
	expr, err := promQLAPIParser().ParseExpr(query)
	if err != nil {
		writePromError(w, APIError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: err.Error()})
		return nil, false
	}
	return expr, true
}

func promQLAPIParser() parser.Parser {
	return parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true})
}

func allowedMethodsForPath(path string) string {
	if path == "/api/v1/query" || path == "/api/v1/query_range" || path == "/api/v1/query_explain" || path == "/api/v1/query_range_explain" || path == "/api/v1/labels" || path == "/api/v1/series" || path == "/api/v1/format_query" || path == "/api/v1/parse_query" {
		return "GET, HEAD, OPTIONS, POST"
	}
	if path == "/health" || path == "/-/healthy" || path == "/-/ready" || path == "/api/v1/metadata" || path == "/api/v1/targets" || path == "/api/v1/rules" || path == "/api/v1/alerts" || (strings.HasPrefix(path, "/api/v1/label/") && strings.HasSuffix(path, "/values")) {
		return "GET, HEAD, OPTIONS"
	}
	return "OPTIONS"
}

func wantsExplain(values url.Values) bool {
	switch values.Get("explain") {
	case "1", "true", "TRUE", "True":
		return true
	default:
		return false
	}
}

func requestLogComment(r *http.Request, endpoint string, values url.Values) string {
	if header := r.Header.Get("X-Promshim-Log-Comment"); header != "" {
		return truncateLogPart(safeLogPart(header, "request"), maxRequestLogCommentLen)
	}
	if values == nil {
		values = r.URL.Query()
	}
	mode := safeLogPart(values.Get("native_lowering_mode"), "default")
	policy := safeLogPart(values.Get("routing_policy"), "default")
	return "promshim endpoint=" + safeLogPart(endpoint, "unknown") + " query_hash=" + requestLogHash(endpoint, values) + " mode=" + mode + " policy=" + policy
}

func requestLogHash(endpoint string, values url.Values) string {
	if values == nil {
		values = url.Values{}
	}
	hashInput := endpoint + "\x00" + values.Encode()
	sum := sha256.Sum256([]byte(hashInput))
	return hex.EncodeToString(sum[:])[:16]
}

func translatePromQLAST(expr parser.Expr) any {
	if expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *parser.AggregateExpr:
		return map[string]any{"type": "aggregation", "op": n.Op.String(), "expr": translatePromQLAST(n.Expr), "param": translatePromQLAST(n.Param), "grouping": sanitizeStringList(n.Grouping), "without": n.Without}
	case *parser.BinaryExpr:
		var matching any
		if m := n.VectorMatching; m != nil {
			matching = map[string]any{"card": m.Card.String(), "labels": sanitizeStringList(m.MatchingLabels), "on": m.On, "include": sanitizeStringList(m.Include), "fillValues": map[string]*float64{"lhs": m.FillValues.LHS, "rhs": m.FillValues.RHS}}
		}
		return map[string]any{"type": "binaryExpr", "op": n.Op.String(), "lhs": translatePromQLAST(n.LHS), "rhs": translatePromQLAST(n.RHS), "matching": matching, "bool": n.ReturnBool}
	case *parser.Call:
		args := make([]any, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, translatePromQLAST(arg))
		}
		return map[string]any{"type": "call", "func": map[string]any{"name": n.Func.Name, "argTypes": n.Func.ArgTypes, "variadic": n.Func.Variadic, "returnType": n.Func.ReturnType}, "args": args}
	case *parser.MatrixSelector:
		vs := n.VectorSelector.(*parser.VectorSelector)
		return map[string]any{"type": "matrixSelector", "name": vs.Name, "range": n.Range.Milliseconds(), "offset": vs.OriginalOffset.Milliseconds(), "matchers": translateMatchers(vs.LabelMatchers), "timestamp": vs.Timestamp, "startOrEnd": startOrEndString(vs.StartOrEnd), "anchored": vs.Anchored, "smoothed": vs.Smoothed}
	case *parser.SubqueryExpr:
		return map[string]any{"type": "subquery", "expr": translatePromQLAST(n.Expr), "range": n.Range.Milliseconds(), "offset": n.OriginalOffset.Milliseconds(), "step": n.Step.Milliseconds(), "timestamp": n.Timestamp, "startOrEnd": startOrEndString(n.StartOrEnd)}
	case *parser.NumberLiteral:
		return map[string]any{"type": "numberLiteral", "val": strconv.FormatFloat(n.Val, 'f', -1, 64)}
	case *parser.ParenExpr:
		return map[string]any{"type": "parenExpr", "expr": translatePromQLAST(n.Expr)}
	case *parser.StringLiteral:
		return map[string]any{"type": "stringLiteral", "val": n.Val}
	case *parser.UnaryExpr:
		return map[string]any{"type": "unaryExpr", "op": n.Op.String(), "expr": translatePromQLAST(n.Expr)}
	case *parser.VectorSelector:
		return map[string]any{"type": "vectorSelector", "name": n.Name, "offset": n.OriginalOffset.Milliseconds(), "matchers": translateMatchers(n.LabelMatchers), "timestamp": n.Timestamp, "startOrEnd": startOrEndString(n.StartOrEnd), "anchored": n.Anchored, "smoothed": n.Smoothed}
	default:
		return map[string]any{"type": "unsupported", "expr": expr.String()}
	}
}

func sanitizeStringList(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func translateMatchers(matchers []*labels.Matcher) []map[string]any {
	out := make([]map[string]any, 0, len(matchers))
	for _, matcher := range matchers {
		out = append(out, map[string]any{"name": matcher.Name, "value": matcher.Value, "type": matcher.Type.String()})
	}
	return out
}

func startOrEndString(value parser.ItemType) any {
	if value == 0 {
		return nil
	}
	return value.String()
}

func truncateLogPart(value string, maxLen int) string {
	if maxLen > 0 && len(value) > maxLen {
		return value[:maxLen]
	}
	return value
}

func safeLogPart(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			return r
		}
		return '_'
	}, value)
	return value
}

func writePromSuccess(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "success", "data": data})
}

func writePromError(w http.ResponseWriter, apiErr APIError) {
	if apiErr.ErrorType != "" {
		w.Header().Set("X-Promshim-Error-Type", apiErr.ErrorType)
	}
	writeJSON(w, apiErr.StatusCode, map[string]any{
		"status":    "error",
		"errorType": apiErr.ErrorType,
		"error":     apiErr.Error,
	})
}

func writePlain(w http.ResponseWriter, statusCode int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(body))
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
