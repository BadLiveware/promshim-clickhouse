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
	if opts.ClickHouseTransport != storage.TransportNative {
		t.Fatalf("default ClickHouseTransport = %q, want %q", opts.ClickHouseTransport, storage.TransportNative)
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

func TestLoadOptionsFromEnvRoutingPolicy(t *testing.T) {
	t.Setenv("PROM_SHIM_ROUTING_POLICY", "cost_shadow")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv cost_shadow: %v", err)
	}
	if opts.RoutingPolicy != RoutingPolicyCostShadow {
		t.Fatalf("RoutingPolicy = %q, want %q", opts.RoutingPolicy, RoutingPolicyCostShadow)
	}
}

func TestLoadOptionsFromEnvRejectsUnknownRoutingPolicy(t *testing.T) {
	t.Setenv("PROM_SHIM_ROUTING_POLICY", "surprise")
	_, err := LoadOptionsFromEnv()
	if err == nil || !strings.Contains(err.Error(), "PROM_SHIM_ROUTING_POLICY") {
		t.Fatalf("LoadOptionsFromEnv unknown routing policy error = %v, want routing policy env error", err)
	}
}

func TestLoadOptionsFromEnvCostRoutingLocalFamilies(t *testing.T) {
	t.Setenv("PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES", "selector_instant, rate_instant")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.CostRoutingLocalFamilies) != 2 || opts.CostRoutingLocalFamilies[0] != "selector_instant" || opts.CostRoutingLocalFamilies[1] != "rate_instant" {
		t.Fatalf("CostRoutingLocalFamilies = %+v", opts.CostRoutingLocalFamilies)
	}
}

func TestLoadOptionsFromEnvNativeGridFunctions(t *testing.T) {
	t.Setenv("PROM_SHIM_NATIVE_GRID_FUNCTIONS", "prefer")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.NativeGridFunctions != "prefer" {
		t.Fatalf("NativeGridFunctions = %q, want prefer", opts.NativeGridFunctions)
	}

	t.Setenv("PROM_SHIM_NATIVE_GRID_FUNCTIONS", "surprise")
	if _, err := LoadOptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "PROM_SHIM_NATIVE_GRID_FUNCTIONS") {
		t.Fatalf("LoadOptionsFromEnv invalid native grid functions error = %v", err)
	}
}

func TestLoadOptionsFromEnvPromotedTagColumns(t *testing.T) {
	t.Setenv("PROM_SHIM_PROMOTED_TAG_COLUMNS", "instance, pod ,node")
	t.Setenv("PROM_SHIM_DISCOVER_PROMOTED_TAG_COLUMNS", "true")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.PromotedTagColumns) != 3 || opts.PromotedTagColumns[0] != "instance" || opts.PromotedTagColumns[1] != "pod" || opts.PromotedTagColumns[2] != "node" {
		t.Fatalf("PromotedTagColumns = %+v", opts.PromotedTagColumns)
	}
	if !opts.DiscoverPromotedTagColumns {
		t.Fatalf("DiscoverPromotedTagColumns = false, want true")
	}
}
