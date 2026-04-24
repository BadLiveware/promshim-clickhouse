package promshim

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

const (
	defaultMaxResponseSeries int64 = 5000
	defaultMaxResponsePoints int64 = 500000
)

type Options struct {
	ClickHouseEndpoint        string
	ClickHouseNativeAddr      string
	Database                  string
	Table                     string
	Username                  string
	Password                  string
	ClickHouseCompression     string
	RequestTimeout            time.Duration
	ClickHouseTransport       storage.TransportKind
	ClickHouseMaxOpenConns    int
	ClickHouseMaxIdleConns    int
	ClickHouseConnMaxLifetime time.Duration
	ClickHouseVersion         string
	NativeLoweringMode        local.NativeLoweringMode
	MaxRangePointsPerSeries   int64
	RangeChunkPointsPerSeries int64
	MaxResponseSeries         int64
	MaxResponsePoints         int64

	DisableEntireQueryDelegation bool
}

func LoadOptionsFromEnv() (Options, error) {
	opts := Options{
		ClickHouseEndpoint:        getenv("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:8123/"),
		ClickHouseNativeAddr:      getenv("PROM_SHIM_CLICKHOUSE_NATIVE_ADDR", "127.0.0.1:9000"),
		Database:                  getenv("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Table:                     getenv("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
		Username:                  getenv("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:                  getenv("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		ClickHouseCompression:     getenv("PROM_SHIM_CLICKHOUSE_COMPRESSION", "off"),
		RequestTimeout:            time.Second * time.Duration(getenvInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)),
		ClickHouseTransport:       storage.TransportKind(getenv("PROM_SHIM_CLICKHOUSE_TRANSPORT", string(storage.TransportHTTP))),
		ClickHouseMaxOpenConns:    getenvInt("PROM_SHIM_CLICKHOUSE_MAX_OPEN_CONNS", 10),
		ClickHouseMaxIdleConns:    getenvInt("PROM_SHIM_CLICKHOUSE_MAX_IDLE_CONNS", 10),
		ClickHouseConnMaxLifetime: time.Second * time.Duration(getenvInt("PROM_SHIM_CLICKHOUSE_CONN_MAX_LIFETIME_SECONDS", 3600)),
		ClickHouseVersion:         getenv("PROM_SHIM_CLICKHOUSE_VERSION", "26.3"),
		NativeLoweringMode:        local.NativeLoweringMode(getenv("PROM_SHIM_NATIVE_LOWERING_MODE", string(local.NativeLoweringModePrefer))),
		MaxRangePointsPerSeries:   getenvInt64("PROM_SHIM_MAX_RANGE_POINTS_PER_SERIES", local.DefaultMaxRangePointsPerSeries),
		RangeChunkPointsPerSeries: getenvInt64("PROM_SHIM_RANGE_CHUNK_POINTS_PER_SERIES", local.DefaultRangeChunkPointsPerSeries),
		MaxResponseSeries:         getenvInt64("PROM_SHIM_MAX_RESPONSE_SERIES", defaultMaxResponseSeries),
		MaxResponsePoints:         getenvInt64("PROM_SHIM_MAX_RESPONSE_POINTS", defaultMaxResponsePoints),
	}

	opts = normalizeOptions(opts)
	if _, err := local.ParseNativeLoweringMode(string(opts.NativeLoweringMode)); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_NATIVE_LOWERING_MODE: %w", err)
	}
	if _, err := storage.ParseTransportKind(string(opts.ClickHouseTransport)); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_TRANSPORT: %w", err)
	}
	if err := storage.ValidateNativeCompression(opts.ClickHouseCompression); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_COMPRESSION: %w", err)
	}

	if _, err := url.Parse(opts.ClickHouseEndpoint); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_ENDPOINT: %w", err)
	}

	return opts, nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
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

func getenvInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func normalizeOptions(opts Options) Options {
	opts.ClickHouseVersion = local.NormalizeClickHouseVersion(opts.ClickHouseVersion)
	if opts.ClickHouseNativeAddr == "" {
		opts.ClickHouseNativeAddr = "127.0.0.1:9000"
	}
	if opts.ClickHouseCompression == "" {
		opts.ClickHouseCompression = "off"
	}
	if transportKind, err := storage.ParseTransportKind(string(opts.ClickHouseTransport)); err == nil {
		opts.ClickHouseTransport = transportKind
	}
	if opts.ClickHouseMaxOpenConns <= 0 {
		opts.ClickHouseMaxOpenConns = 10
	}
	if opts.ClickHouseMaxIdleConns <= 0 {
		opts.ClickHouseMaxIdleConns = 10
	}
	if opts.ClickHouseConnMaxLifetime <= 0 {
		opts.ClickHouseConnMaxLifetime = time.Hour
	}
	opts.NativeLoweringMode = local.NormalizeNativeLoweringMode(opts.NativeLoweringMode)
	if opts.MaxRangePointsPerSeries <= 0 {
		opts.MaxRangePointsPerSeries = local.DefaultMaxRangePointsPerSeries
	}
	if opts.RangeChunkPointsPerSeries <= 0 {
		opts.RangeChunkPointsPerSeries = local.DefaultRangeChunkPointsPerSeries
	}
	if opts.MaxResponseSeries <= 0 {
		opts.MaxResponseSeries = defaultMaxResponseSeries
	}
	if opts.MaxResponsePoints <= 0 {
		opts.MaxResponsePoints = defaultMaxResponsePoints
	}
	return opts
}
