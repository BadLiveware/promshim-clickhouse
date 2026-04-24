package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/ClickHouse/clickhouse-go/v2"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	defaultNativeAddr            = "127.0.0.1:9000"
	defaultNativeMaxOpenConns    = 10
	defaultNativeMaxIdleConns    = 10
	defaultNativeConnMaxLifetime = time.Hour
	defaultNativeRequestTimeout  = 30 * time.Second
	nativeTimeSeriesSetting      = "allow_experimental_time_series_table"
	nativeQuoteDenormalsSetting  = "output_format_json_quote_denormals"
	nativeLogCommentSetting      = "log_comment"
)

var ErrNativeRowsNeedTypedDecoder = errors.New("native driver transport requires typed row decoding")

type NativeDriverTransportConfig struct {
	Addr            string
	Database        string
	Username        string
	Password        string
	Compression     string
	RequestTimeout  time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// NativeDriverTransport owns a ClickHouse native-protocol connection pool.
// Callers can smoke-test typed driver rows through QueryNativeRows/QueryNativeRow,
// while promshim's JSONEachRow Execute adapter remains on HTTP until typed
// decoders are introduced.
type NativeDriverTransport struct {
	conn clickhouse.Conn
}

func NewNativeDriverTransport(cfg NativeDriverTransportConfig) (*NativeDriverTransport, error) {
	cfg = normalizeNativeDriverTransportConfig(cfg)
	compression, err := nativeCompression(cfg.Compression)
	if err != nil {
		return nil, err
	}
	options := &clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		Settings: clickhouse.Settings{
			nativeTimeSeriesSetting:     1,
			nativeQuoteDenormalsSetting: 1,
		},
		DialTimeout:      cfg.RequestTimeout,
		MaxOpenConns:     cfg.MaxOpenConns,
		MaxIdleConns:     cfg.MaxIdleConns,
		ConnMaxLifetime:  cfg.ConnMaxLifetime,
		ConnOpenStrategy: clickhouse.ConnOpenInOrder,
	}
	if compression != nil {
		options.Compression = compression
	}
	conn, err := clickhouse.Open(options)
	if err != nil {
		return nil, err
	}
	return &NativeDriverTransport{conn: conn}, nil
}

func (t *NativeDriverTransport) Ping(ctx context.Context) error {
	return t.conn.Ping(ctx)
}

func (t *NativeDriverTransport) Query(ctx context.Context, req QueryRequest) (Rows, error) {
	return nil, fmt.Errorf("%w for %s transport", ErrNativeRowsNeedTypedDecoder, TransportNative)
}

func (t *NativeDriverTransport) QueryNativeRows(ctx context.Context, req QueryRequest) (chdriver.Rows, error) {
	ctx = t.queryContext(ctx, req)
	start := time.Now()
	rows, err := t.conn.Query(ctx, driverSQL(req.SQL))
	duration := time.Since(start)
	obs.FromContext(ctx).Observe(duration)
	observeQuery(TransportNative, req.Purpose, queryStatus(err), duration)
	return rows, err
}

func (t *NativeDriverTransport) QueryNativeRow(ctx context.Context, req QueryRequest) chdriver.Row {
	ctx = t.queryContext(ctx, req)
	start := time.Now()
	return &nativeObservedRow{
		row:     t.conn.QueryRow(ctx, driverSQL(req.SQL)),
		ctx:     ctx,
		start:   start,
		purpose: req.Purpose,
	}
}

type nativeObservedRow struct {
	row     chdriver.Row
	ctx     context.Context
	start   time.Time
	purpose QueryPurpose
	once    sync.Once
}

func (r *nativeObservedRow) Err() error {
	err := r.row.Err()
	if err != nil {
		r.observe(err)
	}
	return err
}

func (r *nativeObservedRow) Scan(dest ...any) error {
	err := r.row.Scan(dest...)
	r.observe(err)
	return err
}

func (r *nativeObservedRow) ScanStruct(dest any) error {
	err := r.row.ScanStruct(dest)
	r.observe(err)
	return err
}

func (r *nativeObservedRow) observe(err error) {
	r.once.Do(func() {
		duration := time.Since(r.start)
		obs.FromContext(r.ctx).Observe(duration)
		observeQuery(TransportNative, r.purpose, queryStatus(err), duration)
	})
}

func (t *NativeDriverTransport) Exec(ctx context.Context, req QueryRequest) error {
	ctx = t.queryContext(ctx, req)
	start := time.Now()
	err := t.conn.Exec(ctx, driverSQL(req.SQL))
	duration := time.Since(start)
	obs.FromContext(ctx).Observe(duration)
	observeQuery(TransportNative, req.Purpose, queryStatus(err), duration)
	return err
}

func (t *NativeDriverTransport) Close() error {
	return t.conn.Close()
}

func (t *NativeDriverTransport) queryContext(ctx context.Context, req QueryRequest) context.Context {
	settings := clickhouse.Settings{
		nativeTimeSeriesSetting:     1,
		nativeQuoteDenormalsSetting: 1,
	}
	for key, value := range req.Settings {
		settings[key] = value
	}
	if tag := obs.LogCommentFromContext(ctx); tag != "" {
		settings[nativeLogCommentSetting] = tag
	}
	options := []clickhouse.QueryOption{clickhouse.WithSettings(settings)}
	if len(req.Params) > 0 {
		options = append(options, clickhouse.WithParameters(driverParameters(req.Params)))
	}
	return clickhouse.Context(ctx, options...)
}

func normalizeNativeDriverTransportConfig(cfg NativeDriverTransportConfig) NativeDriverTransportConfig {
	if cfg.Addr == "" {
		cfg.Addr = defaultNativeAddr
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultNativeRequestTimeout
	}
	if cfg.MaxOpenConns <= 0 {
		cfg.MaxOpenConns = defaultNativeMaxOpenConns
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = defaultNativeMaxIdleConns
	}
	if cfg.ConnMaxLifetime <= 0 {
		cfg.ConnMaxLifetime = defaultNativeConnMaxLifetime
	}
	return cfg
}

func queryStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func driverParameters(params map[string]string) clickhouse.Parameters {
	adapted := make(clickhouse.Parameters, len(params))
	for key, value := range params {
		adapted[strings.TrimPrefix(key, "param_")] = value
	}
	return adapted
}

func driverSQL(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if idx := strings.LastIndex(trimmed, schema.FormatLine); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return trimmed
}

func nativeCompression(value string) (*clickhouse.Compression, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "none":
		return nil, nil
	case "lz4":
		return &clickhouse.Compression{Method: clickhouse.CompressionLZ4}, nil
	case "zstd":
		return &clickhouse.Compression{Method: clickhouse.CompressionZSTD}, nil
	default:
		return nil, fmt.Errorf("unknown clickhouse native compression %q (supported: off, lz4, zstd)", value)
	}
}

func ValidateNativeCompression(value string) error {
	_, err := nativeCompression(value)
	return err
}
