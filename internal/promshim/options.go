package promshim

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

const (
	defaultMaxRangePointsPerSeries   int64 = 50000
	defaultRangeChunkPointsPerSeries int64 = 5000
)

type Options struct {
	ClickHouseEndpoint        string
	Database                  string
	Table                     string
	Username                  string
	Password                  string
	RequestTimeout            time.Duration
	MaxRangePointsPerSeries   int64
	RangeChunkPointsPerSeries int64
}

func LoadOptionsFromEnv() (Options, error) {
	opts := Options{
		ClickHouseEndpoint:        getenv("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:8123/"),
		Database:                  getenv("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Table:                     getenv("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
		Username:                  getenv("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:                  getenv("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		RequestTimeout:            time.Second * time.Duration(getenvInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)),
		MaxRangePointsPerSeries:   getenvInt64("PROM_SHIM_MAX_RANGE_POINTS_PER_SERIES", defaultMaxRangePointsPerSeries),
		RangeChunkPointsPerSeries: getenvInt64("PROM_SHIM_RANGE_CHUNK_POINTS_PER_SERIES", defaultRangeChunkPointsPerSeries),
	}

	opts = normalizeOptions(opts)

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
	if opts.MaxRangePointsPerSeries <= 0 {
		opts.MaxRangePointsPerSeries = defaultMaxRangePointsPerSeries
	}
	if opts.RangeChunkPointsPerSeries <= 0 {
		opts.RangeChunkPointsPerSeries = defaultRangeChunkPointsPerSeries
	}
	return opts
}
