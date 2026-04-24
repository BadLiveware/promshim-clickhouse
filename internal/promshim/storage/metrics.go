package storage

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	clickHouseQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_clickhouse_queries_total",
		Help: "Total ClickHouse queries issued by promshim.",
	}, []string{"transport", "purpose", "status"})
	clickHouseQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "promshim_clickhouse_query_duration_seconds",
		Help:    "ClickHouse query round-trip duration observed by promshim.",
		Buckets: prometheus.DefBuckets,
	}, []string{"transport", "purpose"})
	clickHouseRowsDecoded = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_clickhouse_rows_decoded_total",
		Help: "Total ClickHouse result rows decoded by promshim.",
	}, []string{"transport", "purpose"})
	clickHouseDecodeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "promshim_clickhouse_decode_duration_seconds",
		Help:    "ClickHouse result decode duration observed by promshim.",
		Buckets: prometheus.DefBuckets,
	}, []string{"transport", "purpose"})
	clickHouseDecodeErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "promshim_clickhouse_decode_errors_total",
		Help: "Total ClickHouse result decode errors observed by promshim.",
	}, []string{"transport", "purpose", "reason"})
)

func RegisterMetrics(registry *prometheus.Registry) {
	if registry == nil {
		return
	}
	registry.MustRegister(
		clickHouseQueries,
		clickHouseQueryDuration,
		clickHouseRowsDecoded,
		clickHouseDecodeDuration,
		clickHouseDecodeErrors,
	)
}

func observeQuery(transport TransportKind, purpose QueryPurpose, status string, duration time.Duration) {
	clickHouseQueries.WithLabelValues(metricLabel(string(transport), "unknown"), metricLabel(string(purpose), "unspecified"), metricLabel(status, "unknown")).Inc()
	clickHouseQueryDuration.WithLabelValues(metricLabel(string(transport), "unknown"), metricLabel(string(purpose), "unspecified")).Observe(duration.Seconds())
}

func observeDecode(transport TransportKind, purpose QueryPurpose, rows int, duration time.Duration, err error) {
	transportLabel := metricLabel(string(transport), "unknown")
	purposeLabel := metricLabel(string(purpose), "unspecified")
	clickHouseDecodeDuration.WithLabelValues(transportLabel, purposeLabel).Observe(duration.Seconds())
	if rows > 0 {
		clickHouseRowsDecoded.WithLabelValues(transportLabel, purposeLabel).Add(float64(rows))
	}
	if err != nil {
		clickHouseDecodeErrors.WithLabelValues(transportLabel, purposeLabel, decodeErrorReason(err)).Inc()
	}
}

func metricLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func decodeErrorReason(err error) string {
	if err == nil {
		return "none"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "scan"):
		return "scan"
	case strings.Contains(message, "shape"):
		return "shape"
	case strings.Contains(message, "type"):
		return "type"
	default:
		return "other"
	}
}
