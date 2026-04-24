package promshim

import (
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func TestLoadOptionsFromEnvClickHouseTransportDefaultAndHTTP(t *testing.T) {
	t.Setenv("PROM_SHIM_CLICKHOUSE_TRANSPORT", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv default: %v", err)
	}
	if opts.ClickHouseTransport != storage.TransportHTTP {
		t.Fatalf("default ClickHouseTransport = %q, want %q", opts.ClickHouseTransport, storage.TransportHTTP)
	}

	t.Setenv("PROM_SHIM_CLICKHOUSE_TRANSPORT", "http")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv http: %v", err)
	}
	if opts.ClickHouseTransport != storage.TransportHTTP {
		t.Fatalf("ClickHouseTransport = %q, want %q", opts.ClickHouseTransport, storage.TransportHTTP)
	}
}

func TestLoadOptionsFromEnvClickHouseTransportNative(t *testing.T) {
	t.Setenv("PROM_SHIM_CLICKHOUSE_TRANSPORT", "native")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv native: %v", err)
	}
	if opts.ClickHouseTransport != storage.TransportNative {
		t.Fatalf("ClickHouseTransport = %q, want %q", opts.ClickHouseTransport, storage.TransportNative)
	}
}

func TestLoadOptionsFromEnvRejectsUnknownClickHouseTransport(t *testing.T) {
	t.Setenv("PROM_SHIM_CLICKHOUSE_TRANSPORT", "grpc")
	_, err := LoadOptionsFromEnv()
	if err == nil || !strings.Contains(err.Error(), "PROM_SHIM_CLICKHOUSE_TRANSPORT") {
		t.Fatalf("LoadOptionsFromEnv unknown transport error = %v, want transport env error", err)
	}
}

func TestLoadOptionsFromEnvRejectsUnknownClickHouseCompression(t *testing.T) {
	t.Setenv("PROM_SHIM_CLICKHOUSE_COMPRESSION", "snappy")
	_, err := LoadOptionsFromEnv()
	if err == nil || !strings.Contains(err.Error(), "PROM_SHIM_CLICKHOUSE_COMPRESSION") {
		t.Fatalf("LoadOptionsFromEnv unknown compression error = %v, want compression env error", err)
	}
}
