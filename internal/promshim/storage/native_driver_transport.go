package storage

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
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
	Addr                  string
	Database              string
	Username              string
	Password              string
	Compression           string
	RequestTimeout        time.Duration
	MaxOpenConns          int
	MaxIdleConns          int
	ConnMaxLifetime       time.Duration
	Secure                bool
	TLSInsecureSkipVerify bool
	TLSServerName         string
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
	if cfg.Secure {
		options.TLS = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSInsecureSkipVerify, ServerName: cfg.TLSServerName}
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
	if req.Format != ResultFormatJSONEachRow {
		return nil, fmt.Errorf("%w for %s transport", ErrNativeRowsNeedTypedDecoder, TransportNative)
	}
	ctx = t.queryContext(ctx, req)
	start := time.Now()
	rows, err := t.conn.Query(ctx, req.SQL)
	if err != nil {
		observeQuery(TransportNative, req.Purpose, "error", time.Since(start))
		return nil, err
	}
	pr, pw := io.Pipe()
	enc := json.NewEncoder(pw)
	go func() {
		columns := rows.Columns()
		types := rows.ColumnTypes()
		// Wrap result in the {"data": [...]} structure the callers expect.
		_, _ = io.WriteString(pw, `{"data":[`)
		first := true
		for rows.Next() {
			vals := make([]any, len(columns))
			ptrs := make([]any, len(columns))
			rowVals := make([]any, len(columns))
			for i := range ptrs {
				ptrs[i] = scanTarget(types[i], &rowVals[i])
			}
			if scanErr := rows.Scan(ptrs...); scanErr != nil {
				_ = pw.CloseWithError(scanErr)
				_ = rows.Close()
				return
			}
			// Resolve scanned pointers to their concrete values.
			copy(vals, rowVals)
			if scanErr := rows.Scan(ptrs...); scanErr != nil {
				_ = pw.CloseWithError(scanErr)
				_ = rows.Close()
				return
			}
			row := make(map[string]any, len(columns))
			for i, col := range columns {
				row[col] = vals[i]
			}
			if !first {
				_, _ = io.WriteString(pw, ",")
			}
			first = false
			if encErr := enc.Encode(row); encErr != nil {
				_ = pw.CloseWithError(encErr)
				_ = rows.Close()
				return
			}
		}
		if scanErr := rows.Err(); scanErr != nil {
			_ = pw.CloseWithError(scanErr)
			_ = rows.Close()
			return
		}
		_ = rows.Close()
		_, _ = io.WriteString(pw, `]}`)
		_ = pw.Close()
	}()
	duration := time.Since(start)
	obs.FromContext(ctx).Observe(duration)
	observeQuery(TransportNative, req.Purpose, "success", duration)
	return &httpRows{response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
	}}, nil
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

// scanTarget allocates a properly-typed value for scanning a ClickHouse
// column. It returns a pointer suitable for rows.Scan and stores the
// dereferenced value back through out. Using *any (interface{}) fails
// for types like Datetime64 that the driver can't convert to interface{}.
func scanTarget(ct chdriver.ColumnType, out *any) any {
	st := ct.ScanType()
	if st == nil {
		return out // fall back to *any
	}
	v := reflect.New(st)
	*out = v.Interface()
	// Return the pointer (same as *out) — rows.Scan fills the pointed-to value.
	return *out
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
