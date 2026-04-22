package shadow

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry    *prometheus.Registry
	comparisons *prometheus.CounterVec
	durations   *prometheus.HistogramVec
}

func newMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	comparisons := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_shadow_comparisons_total",
		Help: "Total number of promshim shadow comparisons partitioned by rollout outcome.",
	}, []string{"status", "category", "compare_mode"})
	durations := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "promshim_shadow_duration_milliseconds",
		Help:    "Observed promshim shadow planning and evaluation durations in milliseconds.",
		Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
	}, []string{"path", "phase", "status", "compare_mode"})
	registry.MustRegister(comparisons, durations)
	return &Metrics{registry: registry, comparisons: comparisons, durations: durations}
}

func (m *Metrics) handler() http.Handler {
	if m == nil || m.registry == nil {
		return promhttp.Handler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) record(report *Comparison) {
	if m == nil || report == nil {
		return
	}
	cat := comparisonCategory(report)
	m.comparisons.WithLabelValues(report.Status, cat, report.CompareMode).Inc()
	m.observeDuration("served", "plan", report.Status, report.CompareMode, report.ServedPlanMillis)
	m.observeDuration("served", "eval", report.Status, report.CompareMode, report.ServedEvalMillis)
	m.observeDuration("shadow", "plan", report.Status, report.CompareMode, report.ShadowPlanMillis)
	m.observeDuration("shadow", "eval", report.Status, report.CompareMode, report.ShadowEvalMillis)
}

func (m *Metrics) observeDuration(path, phase, status, compareMode string, millis int64) {
	if m == nil {
		return
	}
	m.durations.WithLabelValues(path, phase, status, compareMode).Observe(float64(millis))
}
