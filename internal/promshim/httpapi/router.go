package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
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
	Explain            bool
	NativeLoweringMode string
	RoutingPolicy      string
}

type RangeQueryRequest struct {
	Query              string
	Start              string
	End                string
	Step               string
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
}

type clickHouseTransporter interface {
	ClickHouseTransport() string
}

type Handler struct {
	service Service
	mux     *http.ServeMux
}

func NewHandler(service Service) http.Handler {
	h := &Handler{service: service, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("GET /-/healthy", h.handleHealthy)
	h.mux.HandleFunc("GET /-/ready", h.handleReady)
	h.mux.HandleFunc("GET /api/v1/query", h.handleQuery)
	h.mux.HandleFunc("GET /api/v1/query_range", h.handleQueryRange)
	h.mux.HandleFunc("GET /api/v1/query_explain", h.handleQueryExplain)
	h.mux.HandleFunc("GET /api/v1/query_range_explain", h.handleQueryRangeExplain)
	h.mux.HandleFunc("GET /api/v1/labels", h.handleLabels)
	h.mux.HandleFunc("GET /api/v1/label/{name}/values", h.handleLabelValues)
	h.mux.HandleFunc("GET /api/v1/series", h.handleSeries)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleHealthy(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ok")
}

func (h *Handler) handleReady(w http.ResponseWriter, _ *http.Request) {
	writePlain(w, http.StatusOK, "ready")
}

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query")))
	resp, apiErr := h.service.InstantQuery(ctx, InstantQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Time:               r.URL.Query().Get("time"),
		Explain:            wantsExplain(r),
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
		RoutingPolicy:      r.URL.Query().Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query_range")))
	resp, apiErr := h.service.RangeQuery(ctx, RangeQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Start:              r.URL.Query().Get("start"),
		End:                r.URL.Query().Get("end"),
		Step:               r.URL.Query().Get("step"),
		Explain:            wantsExplain(r),
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
		RoutingPolicy:      r.URL.Query().Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleQueryExplain(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query_explain")))
	resp, apiErr := h.service.ExplainInstant(ctx, InstantQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Time:               r.URL.Query().Get("time"),
		Explain:            true,
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
		RoutingPolicy:      r.URL.Query().Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleQueryRangeExplain(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "query_range_explain")))
	resp, apiErr := h.service.ExplainRange(ctx, RangeQueryRequest{
		Query:              r.URL.Query().Get("query"),
		Start:              r.URL.Query().Get("start"),
		End:                r.URL.Query().Get("end"),
		Step:               r.URL.Query().Get("step"),
		Explain:            true,
		NativeLoweringMode: r.URL.Query().Get("native_lowering_mode"),
		RoutingPolicy:      r.URL.Query().Get("routing_policy"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleLabels(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "labels")))
	resp, apiErr := h.service.Labels(ctx, MetadataRequest{
		Matchers: readMatchers(r),
		Start:    r.URL.Query().Get("start"),
		End:      r.URL.Query().Get("end"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "label_values")))
	resp, apiErr := h.service.LabelValues(ctx, LabelValuesRequest{
		Name: r.PathValue("name"),
		MetadataRequest: MetadataRequest{
			Matchers: readMatchers(r),
			Start:    r.URL.Query().Get("start"),
			End:      r.URL.Query().Get("end"),
		},
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
}

func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	ctx, metrics := obs.WithCHMetrics(obs.WithLogComment(r.Context(), requestLogComment(r, "series")))
	resp, apiErr := h.service.Series(ctx, MetadataRequest{
		Matchers: readMatchers(r),
		Start:    r.URL.Query().Get("start"),
		End:      r.URL.Query().Get("end"),
	})
	writeServiceResult(w, resp, apiErr, metrics, h.clickHouseTransport())
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

func readMatchers(r *http.Request) []string {
	matchers := r.URL.Query()["match[]"]
	if len(matchers) > 0 {
		return matchers
	}
	return r.URL.Query()["match"]
}

func wantsExplain(r *http.Request) bool {
	switch r.URL.Query().Get("explain") {
	case "1", "true", "TRUE", "True":
		return true
	default:
		return false
	}
}

func requestLogComment(r *http.Request, endpoint string) string {
	if header := r.Header.Get("X-Promshim-Log-Comment"); header != "" {
		return header
	}
	hashInput := endpoint + "\x00" + r.URL.RawQuery
	sum := sha256.Sum256([]byte(hashInput))
	queryHash := hex.EncodeToString(sum[:])[:16]
	mode := safeLogPart(r.URL.Query().Get("native_lowering_mode"), "default")
	policy := safeLogPart(r.URL.Query().Get("routing_policy"), "default")
	return "promshim endpoint=" + safeLogPart(endpoint, "unknown") + " query_hash=" + queryHash + " mode=" + mode + " policy=" + policy
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

func writePromError(w http.ResponseWriter, apiErr APIError) {
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
