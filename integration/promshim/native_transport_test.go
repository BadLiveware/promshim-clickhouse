package promshim_test

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func TestNativeTransportSyntheticQueryIntegration(t *testing.T) {
	if os.Getenv("PROM_SHIM_RUN_INTEGRATION_TESTS") == "" && os.Getenv("PROM_SHIM_CLICKHOUSE_TRANSPORT") != string(storage.TransportNative) && os.Getenv("PROM_SHIM_CLICKHOUSE_NATIVE_ADDR") == "" {
		t.Skip("set PROM_SHIM_RUN_INTEGRATION_TESTS=1, PROM_SHIM_CLICKHOUSE_TRANSPORT=native, or PROM_SHIM_CLICKHOUSE_NATIVE_ADDR with ClickHouse native TCP reachable")
	}

	handler, err := promshim.NewHandler(promshim.Options{
		ClickHouseEndpoint:           envOr("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:28123/"),
		ClickHouseNativeAddr:         envOr("PROM_SHIM_CLICKHOUSE_NATIVE_ADDR", "127.0.0.1:29000"),
		Database:                     envOr("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Table:                        envOr("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
		Username:                     envOr("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:                     envOr("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		ClickHouseTransport:          storage.TransportNative,
		RequestTimeout:               30 * time.Second,
		DisableEntireQueryDelegation: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	fixture := &fixture{server: server, client: server.Client()}
	fixture.client.Timeout = 30 * time.Second

	instant, err := fixture.getJSON("/api/v1/query?query=vector(1)&time=2024-01-01T00:00:00Z")
	if err != nil {
		t.Skipf("native transport fixture unavailable: %v", err)
	}
	if instant["status"] != "success" {
		t.Fatalf("instant status = %#v, want success", instant)
	}

	rangeBody, err := fixture.getJSON("/api/v1/query_range?query=vector(1)&start=2024-01-01T00:00:00Z&end=2024-01-01T00:02:00Z&step=60s")
	if err != nil {
		t.Fatalf("native range query: %v", err)
	}
	if rangeBody["status"] != "success" {
		t.Fatalf("range status = %#v, want success", rangeBody)
	}
}
