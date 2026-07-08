package promshim

import (
	"context"
	"log"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
)

// Execution-time fallback: in the adaptive native-lowering modes (prefer /
// explain) a committed native plan can still fail at ClickHouse execution —
// e.g. a render bug like issue #39's rejected filtered MATERIALIZED CTE
// reference. Instead of surfacing a hard 502, the query is retried exactly
// once against full local (tier-4) execution and the fallback is recorded
// (counter, log line, explain output) so render bugs stay visible instead of
// silently degrading.
//
// Hard rules:
//   - Never in force_supported: that mode exists to keep native gaps loudly
//     visible (the native-only compliance pass depends on it).
//   - Never in shadow: tier 4 already serves the response there; background
//     native errors are recorded as divergences by the shadow runner.
//   - Never for client-class errors (bad_data / unsupported): those keep
//     their 4xx status. See local.ExecutionFallbackEligible for the exact
//     execution-class vs client-class rule.
//   - Retry once, to local execution only — no re-render attempts — and only
//     when the local plan builds (i.e. tier 4 is a known-correct candidate).

const (
	executionFallbackReason           = "native_execution_error"
	executionFallbackOutcomeSuccess   = "success"
	executionFallbackOutcomeLocalErr  = "local_error"
	executionFallbackOutcomePlanError = "local_plan_error"
)

// nativeExecutionFallbackReport records one execution-time fallback for
// observability; it is embedded in explain responses as "executionFallback".
type nativeExecutionFallbackReport struct {
	FromStrategy    string `json:"fromStrategy"`
	Mode            string `json:"nativeLoweringMode"`
	ClickHouseError string `json:"clickHouseError"`
	Outcome         string `json:"outcome"`
}

// executionFallbackMode reports whether mode is adaptive (prefer / explain).
// force_supported must keep hard-failing and shadow already serves tier 4.
func executionFallbackMode(mode local.NativeLoweringMode) bool {
	switch local.NormalizeNativeLoweringMode(mode) {
	case local.NativeLoweringModePrefer, local.NativeLoweringModeExplain:
		return true
	default:
		return false
	}
}

// isClickHouseSideStrategy reports whether an ExplainNode.Strategy names a
// node executed on the ClickHouse side. The set is matched explicitly rather
// than by negating "local", because the local-family strategies are not just
// "local": a chunked range plan whose child runs locally reports the
// pure-local strategy "chunked_local" (see chunkedRangePlan.explain), and the
// pre-finalize/root nodes carry the empty strategy. Negating "local" alone
// misclassified "chunked_local" as ClickHouse-side, making a pure-local
// chunked range plan wrongly fallback-eligible: on a local execution failure
// it would run a redundant local retry and record a misleading
// native->local fallback.
//
// Fail-safe for unknown/future strategies: an unrecognized strategy is treated
// as NOT ClickHouse-side (not fallback-eligible). The alternative — treating
// unknown strategies as ClickHouse-side — would, on a genuinely-local
// execution failure, fabricate a native->local fallback that both masks the
// real error behind a local re-run and pollutes the native_execution_fallback
// metric that this feature exists to make render bugs visible on. Failing
// toward "not eligible" keeps such a failure a visible, honest error (the
// pre-feature 502) instead of silently degrading. A new ClickHouse-side
// strategy must be added here deliberately; TestIsClickHouseSideStrategy locks
// the set so the gap surfaces at development time rather than in production.
func isClickHouseSideStrategy(strategy string) bool {
	switch strategy {
	case "native_sql", "native_sql_expression", "delegated_promql", "chunked_native":
		return true
	default:
		// "local", "chunked_local", "" (root/pre-finalize) and any unknown
		// strategy are treated as local-family / not fallback-eligible.
		return false
	}
}

// explainHasClickHouseSideNode reports whether the plan tree contains any node
// executed on the ClickHouse side (see isClickHouseSideStrategy). A pure-local
// plan — including a pure-local chunked range plan — gains nothing from a
// local retry, so it is not a fallback candidate.
func explainHasClickHouseSideNode(node local.ExplainNode) bool {
	if isClickHouseSideStrategy(node.Strategy) {
		return true
	}
	for _, child := range node.Children {
		if explainHasClickHouseSideNode(child) {
			return true
		}
	}
	return false
}

// markRoutingServedLocal reconciles the routing report with what a successful
// execution fallback actually served. Routing selected native and committed a
// native plan; the fallback overrode that at execution time and served full
// local (tier-4). The served-facing field — CandidateDecision.ServedCandidate
// — is corrected to the local candidate so it no longer names the native plan
// that failed. The routing-decision fields (StrictStrategy, SelectedStrategy,
// WouldSelect, Decision, Reason and the strict/selected candidates) are left
// intact: they truthfully record why native was attempted, and the resulting
// selected-vs-served divergence is exactly the fallback signal. The served
// plan is further reflected by the already-correct top-level
// X-Promshim-Strategy=local / X-Promshim-Fallback-Reason headers and the
// explain executionFallback section. Metric labels are untouched.
func markRoutingServedLocal(routing *httpapi.RoutingInfo) {
	if routing == nil || routing.CandidateDecision == nil {
		return
	}
	// The fallback always runs full local (tier-4, off mode), so the served
	// candidate is the full-local candidate regardless of the request mode.
	routing.CandidateDecision.ServedCandidate = string(cbeCandidateForStrategyInMode("local", local.NativeLoweringModeOff))
}

func (h *queryService) executionFallbackEligible(ctx context.Context, mode local.NativeLoweringMode, servedExplain local.ExplainNode, evalErr error) bool {
	return executionFallbackMode(mode) &&
		explainHasClickHouseSideNode(servedExplain) &&
		local.ExecutionFallbackEligible(evalErr) &&
		ctx.Err() == nil
}

// recordExecutionFallback bumps the fallback counter and logs one line whose
// wording reflects the outcome: a plan-build failure never ran local
// execution, and a failed local retry logs both the native and local errors.
func (h *queryService) recordExecutionFallback(endpoint string, report *nativeExecutionFallbackReport, query string, nativeErr, localErr error) {
	routingmetrics.ObserveExecutionFallback(endpoint, report.Mode, report.FromStrategy, report.Outcome)
	switch report.Outcome {
	case executionFallbackOutcomePlanError:
		log.Printf("promshim: native execution fallback skipped, local plan build failed (endpoint=%s mode=%s from_strategy=%s outcome=%s query=%q): clickhouse error: %v",
			endpoint, report.Mode, report.FromStrategy, report.Outcome, query, nativeErr)
	case executionFallbackOutcomeLocalErr:
		log.Printf("promshim: native execution fallback to local failed (endpoint=%s mode=%s from_strategy=%s outcome=%s query=%q): clickhouse error: %v; local error: %v",
			endpoint, report.Mode, report.FromStrategy, report.Outcome, query, nativeErr, localErr)
	default:
		log.Printf("promshim: native execution fallback to local (endpoint=%s mode=%s from_strategy=%s outcome=%s query=%q): clickhouse error: %v",
			endpoint, report.Mode, report.FromStrategy, report.Outcome, query, nativeErr)
	}
}

// servedErrorAfterLocalFailure decides which error to surface when the local
// retry itself fails at evaluation. The local error is served only when it is
// itself execution-class; otherwise the original native error is re-served so
// a native execution bug is never masked by a client-class 4xx (a local
// bad_data / unsupported error must not replace the native 502).
func servedErrorAfterLocalFailure(nativeErr, localErr error) *httpapi.APIError {
	if local.ExecutionFallbackEligible(localErr) {
		return local.ApiErrorToHTTP(localErr)
	}
	return local.ApiErrorToHTTP(nativeErr)
}

// instantExecutionFallback retries an instant query once against full local
// execution after a committed native plan failed with an execution-class
// error. Returns (nil report, nil apiErr) when no fallback applies — the
// caller keeps the original error. When the local retry itself fails at
// evaluation, the local error is served only if it is execution-class;
// otherwise the original native error is re-served (see
// servedErrorAfterLocalFailure).
func (h *queryService) instantExecutionFallback(ctx context.Context, req httpapi.InstantQueryRequest, mode local.NativeLoweringMode, servedExplain local.ExplainNode, evaluationTime time.Time, evalErr error) (model.RuntimeValue, local.ExplainNode, *nativeExecutionFallbackReport, *httpapi.APIError) {
	if !h.executionFallbackEligible(ctx, mode, servedExplain, evalErr) {
		return nil, local.ExplainNode{}, nil, nil
	}
	report := &nativeExecutionFallbackReport{FromStrategy: servedExplain.Strategy, Mode: string(local.NormalizeNativeLoweringMode(mode)), ClickHouseError: evalErr.Error()}
	localReq := req
	// Pin the already-resolved evaluation time so the retry evaluates the
	// same instant even when the request omitted ?time=.
	localReq.Time = evaluationTime.UTC().Format(time.RFC3339Nano)
	localMode := local.NativeLoweringModeOff
	_, localEvaluationTime, localPlan, localAnalysis, _, apiErr := h.buildInstantPlanWithMode(localReq, &localMode)
	if apiErr != nil {
		report.Outcome = executionFallbackOutcomePlanError
		h.recordExecutionFallback("query", report, req.Query, evalErr, nil)
		return nil, local.ExplainNode{}, nil, nil
	}
	value, err := h.evaluator.Evaluate(ctx, localPlan, local.EvalParams{Mode: local.EvalModeInstant, EvaluationTime: localEvaluationTime})
	if err != nil {
		report.Outcome = executionFallbackOutcomeLocalErr
		h.recordExecutionFallback("query", report, req.Query, evalErr, err)
		return nil, local.ExplainNode{}, nil, servedErrorAfterLocalFailure(evalErr, err)
	}
	report.Outcome = executionFallbackOutcomeSuccess
	h.recordExecutionFallback("query", report, req.Query, evalErr, nil)
	explain := local.ExplainPlanWithLowering(localPlan, localAnalysis.Root)
	explain.FallbackReason = executionFallbackReason
	return value, explain, report, nil
}

// rangeExecutionFallback is the range-endpoint counterpart of
// instantExecutionFallback.
func (h *queryService) rangeExecutionFallback(ctx context.Context, req httpapi.RangeQueryRequest, mode local.NativeLoweringMode, servedExplain local.ExplainNode, start, end time.Time, step time.Duration, evalErr error) (model.RuntimeValue, local.ExplainNode, *nativeExecutionFallbackReport, *httpapi.APIError) {
	if !h.executionFallbackEligible(ctx, mode, servedExplain, evalErr) {
		return nil, local.ExplainNode{}, nil, nil
	}
	report := &nativeExecutionFallbackReport{FromStrategy: servedExplain.Strategy, Mode: string(local.NormalizeNativeLoweringMode(mode)), ClickHouseError: evalErr.Error()}
	localMode := local.NativeLoweringModeOff
	_, _, _, _, localPlan, localAnalysis, _, apiErr := h.buildRangePlanWithMode(ctx, req, false, &localMode)
	if apiErr != nil {
		report.Outcome = executionFallbackOutcomePlanError
		h.recordExecutionFallback("query_range", report, req.Query, evalErr, nil)
		return nil, local.ExplainNode{}, nil, nil
	}
	value, err := h.evaluator.Evaluate(ctx, localPlan, local.EvalParams{Mode: local.EvalModeRange, Start: start, End: end, Step: step})
	if err != nil {
		report.Outcome = executionFallbackOutcomeLocalErr
		h.recordExecutionFallback("query_range", report, req.Query, evalErr, err)
		return nil, local.ExplainNode{}, nil, servedErrorAfterLocalFailure(evalErr, err)
	}
	report.Outcome = executionFallbackOutcomeSuccess
	h.recordExecutionFallback("query_range", report, req.Query, evalErr, nil)
	explain := local.ExplainPlanWithLowering(localPlan, localAnalysis.Root)
	explain.FallbackReason = executionFallbackReason
	return value, explain, report, nil
}
