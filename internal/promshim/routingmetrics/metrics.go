package routingmetrics

import "github.com/prometheus/client_golang/prometheus"

var (
	Decisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_decisions_total",
		Help: "Total cost-routing decisions.",
	}, []string{"policy", "decision", "strict_strategy", "selected_strategy", "family", "reason"})
	ShadowRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_shadow_runs_total",
		Help: "Total cost-routing alternate shadow runs.",
	}, []string{"family", "strict_strategy", "alternate_strategy", "status"})
	ShadowDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "promshim_routing_shadow_duration_seconds",
		Help:    "Cost-routing alternate shadow duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"family", "candidate"})
	ShadowDivergences = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_shadow_divergences_total",
		Help: "Total cost-routing alternate shadow divergences.",
	}, []string{"family", "category"})
	ShadowCandidateOutcomes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_shadow_candidate_outcomes_total",
		Help: "Total cost-shadow outcomes by strict/selected/served and executed alternate candidate.",
	}, []string{"family", "strict_candidate", "selected_candidate", "served_candidate", "alternate_candidate", "status"})
	EstimateMissing = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_estimate_missing_total",
		Help: "Total missing cost-routing estimates by query family and field.",
	}, []string{"family", "field"})
	OverCap = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_over_cap_total",
		Help: "Total cost-routing decisions rejected by hard caps.",
	}, []string{"family", "cap"})
	PredictionError = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "promshim_routing_prediction_error_ratio",
		Help:    "Observed local/strict duration ratio for cost-routing shadow runs.",
		Buckets: []float64{0.1, 0.25, 0.5, 0.7, 1, 1.5, 2, 5, 10},
	}, []string{"family"})
	StatsProbes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_stats_probes_total",
		Help: "Total selector stats probes by query family and status.",
	}, []string{"family", "status"})
	EstimateCache = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_estimate_cache_total",
		Help: "Total cost-routing estimate cache observations by query family, source, and state.",
	}, []string{"family", "source", "state"})
	Candidates = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_routing_candidates_total",
		Help: "Total CBE candidates by policy, family, candidate, selected status, served status, and eligibility state.",
	}, []string{"policy", "family", "candidate", "selected", "served", "state"})
)

func RegisterMetrics(registry *prometheus.Registry) {
	if registry == nil {
		return
	}
	registry.MustRegister(Decisions, ShadowRuns, ShadowDuration, ShadowDivergences, ShadowCandidateOutcomes, EstimateMissing, OverCap, PredictionError, StatsProbes, EstimateCache, Candidates)
}

func ObserveDecision(policy, decision, strictStrategy, selectedStrategy, family, reason string) {
	Decisions.WithLabelValues(label(policy, "unknown"), label(decision, "unknown"), label(strictStrategy, "unknown"), label(selectedStrategy, "unknown"), label(family, "unknown"), label(reason, "unknown")).Inc()
}

func ObserveShadowRun(family, strictStrategy, alternateStrategy, status string) {
	ShadowRuns.WithLabelValues(label(family, "unknown"), label(strictStrategy, "unknown"), label(alternateStrategy, "unknown"), label(status, "unknown")).Inc()
}

func ObserveShadowDuration(family, candidate string, seconds float64) {
	ShadowDuration.WithLabelValues(label(family, "unknown"), label(candidate, "unknown")).Observe(seconds)
}

func ObserveShadowDivergence(family, category string) {
	ShadowDivergences.WithLabelValues(label(family, "unknown"), label(category, "unknown")).Inc()
}

func ObserveShadowCandidateOutcome(family, strictCandidate, selectedCandidate, servedCandidate, alternateCandidate, status string) {
	ShadowCandidateOutcomes.WithLabelValues(
		label(family, "unknown"),
		label(strictCandidate, "unknown"),
		label(selectedCandidate, "unknown"),
		label(servedCandidate, "unknown"),
		label(alternateCandidate, "unknown"),
		label(status, "unknown"),
	).Inc()
}

func ObserveOverCap(family, cap string) {
	OverCap.WithLabelValues(label(family, "unknown"), label(cap, "unknown")).Inc()
}

func ObservePredictionError(family string, ratio float64) {
	PredictionError.WithLabelValues(label(family, "unknown")).Observe(ratio)
}

func ObserveMissingEstimate(family, field string) {
	EstimateMissing.WithLabelValues(label(family, "unknown"), label(field, "unknown")).Inc()
}

func ObserveStatsProbe(family, status string) {
	StatsProbes.WithLabelValues(label(family, "unknown"), label(status, "unknown")).Inc()
}

func ObserveEstimateCache(family, source, state string) {
	EstimateCache.WithLabelValues(label(family, "unknown"), label(source, "unknown"), label(state, "unknown")).Inc()
}

func ObserveCandidate(policy, family, candidate, selected, served, state string) {
	Candidates.WithLabelValues(label(policy, "unknown"), label(family, "unknown"), label(candidate, "unknown"), label(selected, "false"), label(served, "false"), label(state, "unknown")).Inc()
}

func label(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
