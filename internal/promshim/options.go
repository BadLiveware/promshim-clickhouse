package promshim

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

const (
	defaultMaxResponseSeries int64 = 5000
	defaultMaxResponsePoints int64 = 500000
)

type Options struct {
	ClickHouseEndpoint            string
	ClickHouseNativeAddr          string
	Database                      string
	Table                         string
	Username                      string
	Password                      string
	ClickHouseCompression         string
	RequestTimeout                time.Duration
	ClickHouseTransport           storage.TransportKind
	ClickHouseMaxOpenConns        int
	ClickHouseMaxIdleConns        int
	ClickHouseConnMaxLifetime     time.Duration
	ClickHouseVersion             string
	ClickHouseSettingsProfile     string
	ClickHouseMaxMemoryUsageBytes int64
	ClickHouseMaxRowsToRead       int64
	ClickHouseMaxResultRows       int64
	NativeLoweringMode            local.NativeLoweringMode
	RoutingPolicy                 RoutingPolicy
	CostRoutingLocalFamilies      []string
	MaxRangePointsPerSeries       int64
	RangeChunkPointsPerSeries     int64
	MaxResponseSeries             int64
	MaxResponsePoints             int64
	PromotedTagColumns            []string
	DiscoverPromotedTagColumns    bool
	NativeGridFunctions           string

	DisableEntireQueryDelegation bool
}

func LoadOptionsFromEnv() (Options, error) {
	opts := Options{
		ClickHouseEndpoint:            getenv("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:8123/"),
		ClickHouseNativeAddr:          getenv("PROM_SHIM_CLICKHOUSE_NATIVE_ADDR", "127.0.0.1:9000"),
		Database:                      getenv("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Table:                         getenv("PROM_SHIM_CLICKHOUSE_TABLE", "prometheus"),
		Username:                      getenv("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:                      getenv("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		ClickHouseCompression:         getenv("PROM_SHIM_CLICKHOUSE_COMPRESSION", "off"),
		RequestTimeout:                time.Second * time.Duration(getenvInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)),
		ClickHouseTransport:           storage.TransportKind(getenv("PROM_SHIM_CLICKHOUSE_TRANSPORT", string(storage.TransportNative))),
		ClickHouseMaxOpenConns:        getenvInt("PROM_SHIM_CLICKHOUSE_MAX_OPEN_CONNS", 10),
		ClickHouseMaxIdleConns:        getenvInt("PROM_SHIM_CLICKHOUSE_MAX_IDLE_CONNS", 10),
		ClickHouseConnMaxLifetime:     time.Second * time.Duration(getenvInt("PROM_SHIM_CLICKHOUSE_CONN_MAX_LIFETIME_SECONDS", 3600)),
		ClickHouseVersion:             getenv("PROM_SHIM_CLICKHOUSE_VERSION", "26.3"),
		ClickHouseSettingsProfile:     getenv("PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE", storage.SettingsProfileDefaultSafe),
		ClickHouseMaxMemoryUsageBytes: getenvInt64("PROM_SHIM_CLICKHOUSE_MAX_MEMORY_USAGE_BYTES", 0),
		ClickHouseMaxRowsToRead:       getenvInt64("PROM_SHIM_CLICKHOUSE_MAX_ROWS_TO_READ", 0),
		ClickHouseMaxResultRows:       getenvInt64("PROM_SHIM_CLICKHOUSE_MAX_RESULT_ROWS", 0),
		NativeLoweringMode:            local.NativeLoweringMode(getenv("PROM_SHIM_NATIVE_LOWERING_MODE", string(local.NativeLoweringModePrefer))),
		RoutingPolicy:                 RoutingPolicy(getenv("PROM_SHIM_ROUTING_POLICY", string(RoutingPolicyStrict))),
		CostRoutingLocalFamilies:      splitCSVEnv(getenv("PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES", "")),
		MaxRangePointsPerSeries:       getenvInt64("PROM_SHIM_MAX_RANGE_POINTS_PER_SERIES", local.DefaultMaxRangePointsPerSeries),
		RangeChunkPointsPerSeries:     getenvInt64("PROM_SHIM_RANGE_CHUNK_POINTS_PER_SERIES", local.DefaultRangeChunkPointsPerSeries),
		MaxResponseSeries:             getenvInt64("PROM_SHIM_MAX_RESPONSE_SERIES", defaultMaxResponseSeries),
		MaxResponsePoints:             getenvInt64("PROM_SHIM_MAX_RESPONSE_POINTS", defaultMaxResponsePoints),
		PromotedTagColumns:            splitCSVEnv(getenv("PROM_SHIM_PROMOTED_TAG_COLUMNS", "")),
		DiscoverPromotedTagColumns:    getenvBool("PROM_SHIM_DISCOVER_PROMOTED_TAG_COLUMNS", false),
		NativeGridFunctions:           getenv("PROM_SHIM_NATIVE_GRID_FUNCTIONS", "off"),
	}

	if _, err := local.ParseNativeLoweringMode(string(opts.NativeLoweringMode)); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_NATIVE_LOWERING_MODE: %w", err)
	}
	if _, err := ParseRoutingPolicy(string(opts.RoutingPolicy)); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_ROUTING_POLICY: %w", err)
	}
	if _, err := storage.ParseTransportKind(string(opts.ClickHouseTransport)); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_TRANSPORT: %w", err)
	}
	if err := storage.ValidateNativeCompression(opts.ClickHouseCompression); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_COMPRESSION: %w", err)
	}
	if _, err := storage.ParseSettingsProfileName(opts.ClickHouseSettingsProfile); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE: %w", err)
	}
	if normalized := normalizeNativeGridFunctionsMode(opts.NativeGridFunctions); normalized == "" {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_NATIVE_GRID_FUNCTIONS: %q", opts.NativeGridFunctions)
	}

	if _, err := url.Parse(opts.ClickHouseEndpoint); err != nil {
		return Options{}, fmt.Errorf("invalid PROM_SHIM_CLICKHOUSE_ENDPOINT: %w", err)
	}

	return normalizeOptions(opts), nil
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func splitCSVEnv(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func getenvBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
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

func normalizeNativeGridFunctionsMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off":
		return "off"
	case "prefer":
		return "prefer"
	default:
		return ""
	}
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
	opts.ClickHouseSettingsProfile = storage.NormalizeSettingsProfileName(opts.ClickHouseSettingsProfile)
	opts.NativeGridFunctions = normalizeNativeGridFunctionsMode(opts.NativeGridFunctions)
	opts.NativeLoweringMode = local.NormalizeNativeLoweringMode(opts.NativeLoweringMode)
	opts.RoutingPolicy = NormalizeRoutingPolicy(opts.RoutingPolicy)
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
