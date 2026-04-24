package promshim_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim"
)

type fixture struct {
	server *httptest.Server
	client *http.Client
}

var (
	fixtureOnce sync.Once
	shared      *fixture
	sharedErr   error
)

func requireFixture(t *testing.T) *fixture {
	t.Helper()
	if os.Getenv("PROM_SHIM_RUN_INTEGRATION_TESTS") == "" {
		t.Skip("set PROM_SHIM_RUN_INTEGRATION_TESTS=1 with ClickHouse port-forwarded on 127.0.0.1:8123")
	}

	fixtureOnce.Do(func() {
		opts := promshim.Options{
			ClickHouseEndpoint: envOr("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:8123/"),
			Database:           envOr("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
			Table:              envOr("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
			Username:           envOr("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
			Password:           envOr("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
			RequestTimeout:     30 * time.Second,
		}
		handler, err := promshim.NewHandler(opts)
		if err != nil {
			sharedErr = err
			return
		}
		server := httptest.NewServer(handler)
		client := server.Client()
		client.Timeout = 30 * time.Second
		shared = &fixture{server: server, client: client}

		body, err := shared.getJSON("/api/v1/query?query=up")
		if err != nil {
			sharedErr = err
			shared.server.Close()
			shared = nil
			return
		}
		if body["status"] != "success" {
			sharedErr = fmt.Errorf("prerequisite probe failed: %+v", body)
			shared.server.Close()
			shared = nil
		}
	})

	if sharedErr != nil {
		t.Skipf("integration fixture unavailable: %v", sharedErr)
	}
	return shared
}

func (f *fixture) getJSON(path string) (map[string]any, error) {
	response, err := f.client.Get(f.server.URL + path)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
