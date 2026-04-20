package promshim

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

type Options struct {
	ClickHouseEndpoint string
	Database           string
	Table              string
	Username           string
	Password           string
	RequestTimeout     time.Duration
}

func LoadOptionsFromEnv() (Options, error) {
	opts := Options{
		ClickHouseEndpoint: getenv("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:8123/"),
		Database:           getenv("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Table:              getenv("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
		Username:           getenv("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:           getenv("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		RequestTimeout:     time.Second * time.Duration(getenvInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)),
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
