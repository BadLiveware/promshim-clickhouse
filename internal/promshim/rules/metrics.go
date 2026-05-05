package rules

import (
	"github.com/prometheus/client_golang/prometheus"
)

type ExpansionMetrics struct {
	Registry        *prometheus.Registry
	ExpansionsTotal *prometheus.CounterVec
	ExpansionErrors *prometheus.CounterVec
	QueryDuration   *prometheus.HistogramVec
}

func NewExpansionMetrics(registry *prometheus.Registry) *ExpansionMetrics {
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	m := &ExpansionMetrics{
		Registry: registry,
		ExpansionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "promshim_recording_rule_expansions_total",
			Help: "Total number of recording rule expansions by record and mode.",
		}, []string{"record", "mode"}),
		ExpansionErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "promshim_recording_rule_expansion_errors_total",
			Help: "Total number of recording rule expansion errors by record and reason.",
		}, []string{"record", "reason"}),
		QueryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "promshim_recording_rule_query_duration_seconds",
			Help:    "Duration of recording-rule-expanded queries in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"record", "mode"}),
	}
	registry.MustRegister(m.ExpansionsTotal)
	registry.MustRegister(m.ExpansionErrors)
	registry.MustRegister(m.QueryDuration)
	return m
}
