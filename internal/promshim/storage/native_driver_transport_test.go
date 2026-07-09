package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestNativeObservedRowReportsScanErrors(t *testing.T) {
	purpose := QueryPurpose("query_native_row_scan_error_test")
	row := &nativeObservedRow{
		row:     fakeNativeRow{scanErr: errors.New("scan failed")},
		ctx:     context.Background(),
		start:   time.Now(),
		purpose: purpose,
	}

	if err := row.Scan(new(int)); err == nil {
		t.Fatalf("Scan error = nil, want scan failure")
	}

	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := metricCounterValue(families, "promshim_clickhouse_queries_total", map[string]string{"transport": "native", "purpose": string(purpose), "status": "error"}); got < 1 {
		t.Fatalf("native QueryRow scan error counter = %v, want at least 1", got)
	}
	if got := metricCounterValue(families, "promshim_clickhouse_queries_total", map[string]string{"transport": "native", "purpose": string(purpose), "status": "success"}); got != 0 {
		t.Fatalf("native QueryRow scan success counter = %v, want 0", got)
	}
}

func TestNativeObservedRowObservesOnce(t *testing.T) {
	purpose := QueryPurpose("query_native_row_once_test")
	row := &nativeObservedRow{
		row:     fakeNativeRow{},
		ctx:     context.Background(),
		start:   time.Now(),
		purpose: purpose,
	}

	// Delta rather than absolute: the counter is a package-global whose state
	// persists across invocations in the same process (e.g. under -count=2).
	before := gatherNativeSuccess(t, purpose)
	if err := row.Scan(new(int)); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if err := row.Scan(new(int)); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	after := gatherNativeSuccess(t, purpose)

	if got := after - before; got != 1 {
		t.Fatalf("native QueryRow success delta = %v, want 1 (observe must fire once)", got)
	}
}

func gatherNativeSuccess(t *testing.T, purpose QueryPurpose) float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	return metricCounterValue(families, "promshim_clickhouse_queries_total", map[string]string{"transport": "native", "purpose": string(purpose), "status": "success"})
}

func TestDriverParametersStripsHTTPParamPrefix(t *testing.T) {
	params := driverParameters(map[string]string{"param_evaluation_ms": "1", "plain": "two"})
	if params["evaluation_ms"] != "1" {
		t.Fatalf("evaluation_ms = %q, want 1", params["evaluation_ms"])
	}
	if params["plain"] != "two" {
		t.Fatalf("plain = %q, want two", params["plain"])
	}
	if _, ok := params["param_evaluation_ms"]; ok {
		t.Fatalf("driver parameters retained HTTP param_ prefix: %#v", params)
	}
}

type fakeNativeRow struct {
	err     error
	scanErr error
}

func (r fakeNativeRow) Err() error {
	return r.err
}

func (r fakeNativeRow) Scan(dest ...any) error {
	return r.scanErr
}

func (r fakeNativeRow) ScanStruct(dest any) error {
	return r.scanErr
}

func metricCounterValue(families []*dto.MetricFamily, name string, labels map[string]string) float64 {
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricHasLabels(metric, labels) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func metricHasLabels(metric *dto.Metric, labels map[string]string) bool {
	matched := 0
	for _, pair := range metric.GetLabel() {
		if want, ok := labels[pair.GetName()]; ok && pair.GetValue() == want {
			matched++
		}
	}
	return matched == len(labels)
}

func TestDriverSQLStripsJSONEachRowFormatOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
		want string
	}{
		{
			name: "format only suffix",
			sql:  "SELECT label FROM labels\nFORMAT JSONEachRow\n",
			want: "SELECT label FROM labels",
		},
		{
			name: "settings and format suffix",
			sql:  "SELECT value FROM samples\nSETTINGS allow_experimental_time_series_table = 1\nFORMAT JSONEachRow\n",
			want: "SELECT value FROM samples\nSETTINGS allow_experimental_time_series_table = 1",
		},
		{
			name: "no format suffix",
			sql:  "SELECT 1",
			want: "SELECT 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := driverSQL(tc.sql); got != tc.want {
				t.Fatalf("driverSQL() = %q, want %q", got, tc.want)
			}
		})
	}
}
