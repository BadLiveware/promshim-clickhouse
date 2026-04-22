package promshim

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/local"
)

const (
	defaultMaxResponseSeries int64 = 5000
	defaultMaxResponsePoints int64 = 500000
)

type Options struct {
	ClickHouseEndpoint        string
	Database                  string
	Table                     string
	Username                  string
	Password                  string
	RequestTimeout            time.Duration
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
		Database:                  getenv("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Table:                     getenv("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
		Username:                  getenv("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:                  getenv("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		RequestTimeout:            time.Second * time.Duration(getenvInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)),
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
