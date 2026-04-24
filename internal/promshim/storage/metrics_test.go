package storage

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestClickHouseMetricsExposeTransportPurposeAndStatus(t *testing.T) {
	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)

	observeQuery(TransportNative, QueryPurposeInstant, "success", 5*time.Millisecond)
	observeDecode(TransportNative, QueryPurposeInstant, 3, 2*time.Millisecond, nil)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if !hasMetricFamily(families, "promshim_clickhouse_queries_total") {
		t.Fatalf("missing promshim_clickhouse_queries_total metric family")
	}
	if !hasMetricFamily(families, "promshim_clickhouse_rows_decoded_total") {
		t.Fatalf("missing promshim_clickhouse_rows_decoded_total metric family")
	}
}

func hasMetricFamily(families []*dto.MetricFamily, name string) bool {
	for _, family := range families {
		if family.GetName() == name {
			return true
		}
	}
	return false
}
