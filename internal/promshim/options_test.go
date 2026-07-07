package promshim

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
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

func TestLoadOptionsFromEnvDefaultEvaluationInterval(t *testing.T) {
	t.Setenv("PROM_SHIM_DEFAULT_EVALUATION_INTERVAL_SECONDS", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv default: %v", err)
	}
	if opts.DefaultEvaluationInterval != local.DefaultEvaluationInterval {
		t.Fatalf("default DefaultEvaluationInterval = %v, want %v", opts.DefaultEvaluationInterval, local.DefaultEvaluationInterval)
	}

	t.Setenv("PROM_SHIM_DEFAULT_EVALUATION_INTERVAL_SECONDS", "15")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv 15s: %v", err)
	}
	if opts.DefaultEvaluationInterval != 15*time.Second {
		t.Fatalf("DefaultEvaluationInterval = %v, want %v", opts.DefaultEvaluationInterval, 15*time.Second)
	}

	// Invalid values fall back to the 1m Prometheus default.
	t.Setenv("PROM_SHIM_DEFAULT_EVALUATION_INTERVAL_SECONDS", "0")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatalf("LoadOptionsFromEnv zero: %v", err)
	}
	if opts.DefaultEvaluationInterval != local.DefaultEvaluationInterval {
		t.Fatalf("zero DefaultEvaluationInterval = %v, want fallback %v", opts.DefaultEvaluationInterval, local.DefaultEvaluationInterval)
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

func TestLoadOptionsFromEnvRequestRoutingOverrides(t *testing.T) {
	t.Setenv("PROM_SHIM_ALLOW_REQUEST_ROUTING_OVERRIDES", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.AllowRequestRoutingOverrides {
		t.Fatalf("AllowRequestRoutingOverrides = true, want default false")
	}
	t.Setenv("PROM_SHIM_ALLOW_REQUEST_ROUTING_OVERRIDES", "true")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AllowRequestRoutingOverrides {
		t.Fatalf("AllowRequestRoutingOverrides = false, want true")
	}
}

func TestLoadOptionsFromEnvLogPromQL(t *testing.T) {
	t.Setenv("PROM_SHIM_LOG_PROMQL", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.HidePromQL {
		t.Fatalf("HidePromQL = true, want default false")
	}

	t.Setenv("PROM_SHIM_LOG_PROMQL", "false")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.HidePromQL {
		t.Fatalf("HidePromQL = false, want true when PROM_SHIM_LOG_PROMQL=false")
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

func TestLoadOptionsFromEnvNativeTLS(t *testing.T) {
	t.Setenv("PROM_SHIM_CLICKHOUSE_NATIVE_SECURE", "true")
	t.Setenv("PROM_SHIM_CLICKHOUSE_TLS_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("PROM_SHIM_CLICKHOUSE_TLS_SERVER_NAME", "clickhouse.example.com")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.ClickHouseNativeSecure {
		t.Fatalf("ClickHouseNativeSecure = false, want true")
	}
	if !opts.ClickHouseTLSInsecureSkipVerify {
		t.Fatalf("ClickHouseTLSInsecureSkipVerify = false, want true")
	}
	if opts.ClickHouseTLSServerName != "clickhouse.example.com" {
		t.Fatalf("ClickHouseTLSServerName = %q", opts.ClickHouseTLSServerName)
	}
}

func TestLoadOptionsFromEnvNativeGridFunctions(t *testing.T) {
	t.Setenv("PROM_SHIM_NATIVE_GRID_FUNCTIONS", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.NativeGridFunctions != "prefer" {
		t.Fatalf("default NativeGridFunctions = %q, want prefer", opts.NativeGridFunctions)
	}

	t.Setenv("PROM_SHIM_NATIVE_GRID_FUNCTIONS", "off")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.NativeGridFunctions != "off" {
		t.Fatalf("NativeGridFunctions = %q, want off", opts.NativeGridFunctions)
	}

	t.Setenv("PROM_SHIM_NATIVE_GRID_FUNCTIONS", "prefer")
	opts, err = LoadOptionsFromEnv()
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

func TestLoadOptionsFromEnvCumulativeAvgOverTime(t *testing.T) {
	t.Setenv("PROM_SHIM_CUMULATIVE_AVG_OVER_TIME", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.CumulativeAvgOverTime != "prefer" {
		t.Fatalf("default CumulativeAvgOverTime = %q, want prefer", opts.CumulativeAvgOverTime)
	}

	t.Setenv("PROM_SHIM_CUMULATIVE_AVG_OVER_TIME", "off")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.CumulativeAvgOverTime != "off" {
		t.Fatalf("CumulativeAvgOverTime = %q, want off", opts.CumulativeAvgOverTime)
	}

	t.Setenv("PROM_SHIM_CUMULATIVE_AVG_OVER_TIME", "prefer")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.CumulativeAvgOverTime != "prefer" {
		t.Fatalf("CumulativeAvgOverTime = %q, want prefer", opts.CumulativeAvgOverTime)
	}

	t.Setenv("PROM_SHIM_CUMULATIVE_AVG_OVER_TIME", "surprise")
	if _, err := LoadOptionsFromEnv(); err == nil || !strings.Contains(err.Error(), "PROM_SHIM_CUMULATIVE_AVG_OVER_TIME") {
		t.Fatalf("LoadOptionsFromEnv invalid cumulative avg error = %v", err)
	}
}

func TestLoadOptionsFromEnvNativeRangeChunking(t *testing.T) {
	t.Setenv("PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES", "")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_SECONDS", "")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_CHUNKS", "")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_PREFLIGHT_SERIES_THRESHOLD", "")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_PREFLIGHT_TIMEOUT_MS", "")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_PREFLIGHT_MAX_MEMORY_BYTES", "")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.NativeRangeChunkPointsPerSeries != local.DefaultNativeRangeChunkPointsPerSeries {
		t.Fatalf("NativeRangeChunkPointsPerSeries = %d, want default %d", opts.NativeRangeChunkPointsPerSeries, local.DefaultNativeRangeChunkPointsPerSeries)
	}
	if opts.NativeRangeChunkMaxDuration != local.DefaultNativeRangeChunkMaxDuration {
		t.Fatalf("NativeRangeChunkMaxDuration = %s, want default %s", opts.NativeRangeChunkMaxDuration, local.DefaultNativeRangeChunkMaxDuration)
	}
	if opts.NativeRangeChunkMaxChunks != local.DefaultNativeRangeChunkMaxChunks {
		t.Fatalf("NativeRangeChunkMaxChunks = %d, want default %d", opts.NativeRangeChunkMaxChunks, local.DefaultNativeRangeChunkMaxChunks)
	}
	if opts.NativeRangePreflightSeriesThreshold != local.DefaultNativeRangePreflightSeriesThreshold {
		t.Fatalf("NativeRangePreflightSeriesThreshold = %d, want default %d", opts.NativeRangePreflightSeriesThreshold, local.DefaultNativeRangePreflightSeriesThreshold)
	}
	if opts.NativeRangePreflightTimeout != local.DefaultNativeRangePreflightTimeout {
		t.Fatalf("NativeRangePreflightTimeout = %s, want default %s", opts.NativeRangePreflightTimeout, local.DefaultNativeRangePreflightTimeout)
	}
	if opts.NativeRangePreflightMaxMemoryUsage != local.DefaultNativeRangePreflightMaxMemoryUsage {
		t.Fatalf("NativeRangePreflightMaxMemoryUsage = %d, want default %d", opts.NativeRangePreflightMaxMemoryUsage, local.DefaultNativeRangePreflightMaxMemoryUsage)
	}

	t.Setenv("PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES", "123")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_SECONDS", "43200")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_CHUNKS", "6")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_PREFLIGHT_SERIES_THRESHOLD", "456")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_PREFLIGHT_TIMEOUT_MS", "75")
	t.Setenv("PROM_SHIM_NATIVE_RANGE_PREFLIGHT_MAX_MEMORY_BYTES", "1048576")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.NativeRangeChunkPointsPerSeries != 123 {
		t.Fatalf("NativeRangeChunkPointsPerSeries = %d, want 123", opts.NativeRangeChunkPointsPerSeries)
	}
	if opts.NativeRangeChunkMaxDuration != 12*time.Hour {
		t.Fatalf("NativeRangeChunkMaxDuration = %s, want 12h", opts.NativeRangeChunkMaxDuration)
	}
	if opts.NativeRangeChunkMaxChunks != 6 {
		t.Fatalf("NativeRangeChunkMaxChunks = %d, want 6", opts.NativeRangeChunkMaxChunks)
	}
	if opts.NativeRangePreflightSeriesThreshold != 456 {
		t.Fatalf("NativeRangePreflightSeriesThreshold = %d, want 456", opts.NativeRangePreflightSeriesThreshold)
	}
	if opts.NativeRangePreflightTimeout != 75*time.Millisecond {
		t.Fatalf("NativeRangePreflightTimeout = %s, want 75ms", opts.NativeRangePreflightTimeout)
	}
	if opts.NativeRangePreflightMaxMemoryUsage != 1048576 {
		t.Fatalf("NativeRangePreflightMaxMemoryUsage = %d, want 1048576", opts.NativeRangePreflightMaxMemoryUsage)
	}
}

func TestLoadOptionsFromEnvMetadataLimit(t *testing.T) {
	t.Setenv("PROM_SHIM_MAX_METADATA_ITEMS", "123")
	opts, err := LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.MaxMetadataItems != 123 {
		t.Fatalf("MaxMetadataItems = %d, want 123", opts.MaxMetadataItems)
	}

	t.Setenv("PROM_SHIM_MAX_METADATA_ITEMS", "0")
	opts, err = LoadOptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if opts.MaxMetadataItems != defaultMaxMetadataItems {
		t.Fatalf("MaxMetadataItems = %d, want default %d", opts.MaxMetadataItems, defaultMaxMetadataItems)
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
