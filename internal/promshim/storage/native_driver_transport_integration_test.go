package storage

import (
	"context"
	"math"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
)

func TestNativeDriverTransportIntegrationSettingsAndParameters(t *testing.T) {
	transport := requireNativeDriverTransport(t)
	defer func() { _ = transport.Close() }()

	ctx := obs.WithLogComment(context.Background(), "native-integration")
	ctx, metrics := obs.WithCHMetrics(ctx)
	row := transport.QueryNativeRow(ctx, QueryRequest{
		SQL: `
			SELECT
				{s:String} AS s,
				{i:Int64} AS i,
				{f:Float64} AS f,
				fromUnixTimestamp64Milli({ts:Int64}) AS ts,
				toString(toUInt8(getSetting('allow_experimental_time_series_table'))) AS allow_ts,
				toString(getSetting('max_threads')) AS max_threads,
				getSetting('log_comment') AS log_comment
		`,
		Params: map[string]string{
			"s":  "hello",
			"i":  "42",
			"f":  "3.5",
			"ts": "1700000123456",
		},
		Settings: map[string]any{"max_threads": 1},
	})

	var (
		gotString     string
		gotInt        int64
		gotFloat      float64
		gotTime       time.Time
		allowTS       string
		maxThreads    string
		gotLogComment string
	)
	if err := row.Scan(&gotString, &gotInt, &gotFloat, &gotTime, &allowTS, &maxThreads, &gotLogComment); err != nil {
		t.Fatalf("scan settings/parameters row: %v", err)
	}
	if gotString != "hello" || gotInt != 42 || gotFloat != 3.5 {
		t.Fatalf("parameter values = (%q, %d, %v), want (hello, 42, 3.5)", gotString, gotInt, gotFloat)
	}
	if got := gotTime.UnixMilli(); got != 1700000123456 {
		t.Fatalf("timestamp millis = %d, want 1700000123456", got)
	}
	if allowTS != "1" {
		t.Fatalf("allow_experimental_time_series_table = %q, want 1", allowTS)
	}
	if maxThreads != "1" {
		t.Fatalf("max_threads = %q, want 1", maxThreads)
	}
	if gotLogComment != "native-integration" {
		t.Fatalf("log_comment = %q, want native-integration", gotLogComment)
	}
	if got := metrics.Roundtrips(); got != 1 {
		t.Fatalf("roundtrips = %d, want 1", got)
	}
}

func TestNativeDriverTransportIntegrationValueDecoders(t *testing.T) {
	transport := requireNativeDriverTransport(t)
	defer func() { _ = transport.Close() }()

	client := &Client{transportKind: TransportNative, transport: transport}
	instant, err := client.QueryInstantSamples(context.Background(), QueryRequest{SQL: "SELECT [tuple('__name__', 'up')] AS tags, fromUnixTimestamp64Milli(1700000123456) AS timestamp, toFloat64('nan') AS value\nFORMAT JSONEachRow"})
	if err != nil {
		t.Fatalf("QueryInstantSamples: %v", err)
	}
	if len(instant) != 1 || instant[0].Metric["__name__"] != "up" || instant[0].Timestamp != 1700000123.456 || !math.IsNaN(instant[0].Value) {
		t.Fatalf("instant samples = %#v, want one up sample at 1700000123.456 with NaN", instant)
	}

	rangeSeries, err := client.QueryRangeSeries(context.Background(), QueryRequest{SQL: "SELECT [tuple('__name__', 'up')] AS tags, [(fromUnixTimestamp64Milli(1700000000000), toFloat64(1)), (fromUnixTimestamp64Milli(1700000060000), toFloat64('inf'))] AS time_series\nFORMAT JSONEachRow"})
	if err != nil {
		t.Fatalf("QueryRangeSeries: %v", err)
	}
	if len(rangeSeries) != 1 || rangeSeries[0].Metric["__name__"] != "up" || len(rangeSeries[0].Values) != 2 || rangeSeries[0].Values[0].Timestamp != 1700000000 || rangeSeries[0].Values[0].Value != 1 || !math.IsInf(rangeSeries[0].Values[1].Value, 1) {
		t.Fatalf("range series = %#v, want two up points", rangeSeries)
	}
}

func TestNativeDriverTransportIntegrationMetadataDecoders(t *testing.T) {
	transport := requireNativeDriverTransport(t)
	defer func() { _ = transport.Close() }()

	client := &Client{transportKind: TransportNative, transport: transport}
	stringRows, err := client.QueryStringRows(context.Background(), QueryRequest{SQL: "SELECT 'alpha' AS label UNION ALL SELECT 'beta' AS label\nFORMAT JSONEachRow"})
	if err != nil {
		t.Fatalf("QueryStringRows: %v", err)
	}
	sort.Strings(stringRows)
	if len(stringRows) != 2 || stringRows[0] != "alpha" || stringRows[1] != "beta" {
		t.Fatalf("string rows = %#v, want [alpha beta]", stringRows)
	}

	series, err := client.QuerySeriesRows(context.Background(), QueryRequest{SQL: "SELECT [tuple('__name__', 'up'), tuple('job', 'prometheus')] AS tags\nFORMAT JSONEachRow"})
	if err != nil {
		t.Fatalf("QuerySeriesRows: %v", err)
	}
	if len(series) != 1 || series[0]["__name__"] != "up" || series[0]["job"] != "prometheus" {
		t.Fatalf("series rows = %#v, want metric labels", series)
	}
}

func TestNativeDriverTransportIntegrationDenormalFloats(t *testing.T) {
	transport := requireNativeDriverTransport(t)
	defer func() { _ = transport.Close() }()

	row := transport.QueryNativeRow(context.Background(), QueryRequest{SQL: "SELECT toFloat64('nan'), toFloat64('inf'), toFloat64('-inf')"})
	var nan, posInf, negInf float64
	if err := row.Scan(&nan, &posInf, &negInf); err != nil {
		t.Fatalf("scan denormal floats: %v", err)
	}
	if !math.IsNaN(nan) {
		t.Fatalf("nan value = %v, want NaN", nan)
	}
	if !math.IsInf(posInf, 1) {
		t.Fatalf("positive infinity = %v, want +Inf", posInf)
	}
	if !math.IsInf(negInf, -1) {
		t.Fatalf("negative infinity = %v, want -Inf", negInf)
	}
}

func requireNativeDriverTransport(t *testing.T) *NativeDriverTransport {
	t.Helper()
	if os.Getenv("PROM_SHIM_RUN_INTEGRATION_TESTS") == "" && os.Getenv("PROM_SHIM_CLICKHOUSE_TRANSPORT") != string(TransportNative) && os.Getenv("PROM_SHIM_CLICKHOUSE_NATIVE_ADDR") == "" {
		t.Skip("set PROM_SHIM_RUN_INTEGRATION_TESTS=1, PROM_SHIM_CLICKHOUSE_TRANSPORT=native, or PROM_SHIM_CLICKHOUSE_NATIVE_ADDR with ClickHouse native TCP reachable")
	}

	transport, err := NewNativeDriverTransport(NativeDriverTransportConfig{
		Addr:            envOr("PROM_SHIM_CLICKHOUSE_NATIVE_ADDR", "127.0.0.1:9000"),
		Database:        envOr("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Username:        envOr("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:        envOr("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		Compression:     envOr("PROM_SHIM_CLICKHOUSE_COMPRESSION", "off"),
		RequestTimeout:  time.Duration(envInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)) * time.Second,
		MaxOpenConns:    envInt("PROM_SHIM_CLICKHOUSE_MAX_OPEN_CONNS", 10),
		MaxIdleConns:    envInt("PROM_SHIM_CLICKHOUSE_MAX_IDLE_CONNS", 10),
		ConnMaxLifetime: time.Duration(envInt("PROM_SHIM_CLICKHOUSE_CONN_MAX_LIFETIME_SECONDS", 3600)) * time.Second,
	})
	if err != nil {
		t.Fatalf("NewNativeDriverTransport: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := transport.Ping(ctx); err != nil {
		_ = transport.Close()
		t.Skipf("ClickHouse native integration fixture unavailable: %v", err)
	}
	return transport
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
