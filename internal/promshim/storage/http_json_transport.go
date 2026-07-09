package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
)

type HTTPJSONTransportConfig struct {
	Endpoint       string
	Username       string
	Password       string
	RequestTimeout time.Duration
}

// HTTPJSONTransport preserves the original HTTP multipart query path and
// JSONEachRow response handling behind the transport boundary.
type HTTPJSONTransport struct {
	baseURL    *url.URL
	basicAuth  string
	httpClient *http.Client
}

func NewHTTPJSONTransport(cfg HTTPJSONTransportConfig) (*HTTPJSONTransport, error) {
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	query := parsed.Query()
	query.Set("allow_experimental_time_series_table", "1")
	query.Set("output_format_json_quote_denormals", "1")
	parsed.RawQuery = query.Encode()

	credentials := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
	return &HTTPJSONTransport{
		baseURL:   parsed,
		basicAuth: "Basic " + credentials,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}, nil
}

func (t *HTTPJSONTransport) Query(ctx context.Context, req QueryRequest) (Rows, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("query", req.SQL); err != nil {
		return nil, err
	}
	for key, value := range req.Params {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	u := *t.baseURL
	q := u.Query()
	for key, value := range req.Settings {
		q.Set(key, fmt.Sprint(value))
	}
	if tag := obs.LogCommentFromContext(ctx); tag != "" {
		q.Set("log_comment", tag)
	}
	u.RawQuery = q.Encode()
	requestURL := u.String()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, &body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", t.basicAuth)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	start := time.Now()
	response, err := t.httpClient.Do(request)
	if err != nil {
		// No response body to stream; observe the dispatch latency now.
		duration := time.Since(start)
		obs.FromContext(ctx).Observe(duration)
		observeQuery(TransportHTTP, req.Purpose, "error", duration)
		return nil, err
	}
	if response.StatusCode >= 400 {
		// The error body is read inline here, so time.Since spans it fully.
		defer func() { _ = response.Body.Close() }()
		var payload bytes.Buffer
		_, _ = payload.ReadFrom(response.Body)
		duration := time.Since(start)
		obs.FromContext(ctx).Observe(duration)
		observeQuery(TransportHTTP, req.Purpose, "error", duration)
		message := strings.TrimSpace(payload.String())
		if message == "" {
			message = response.Status
		}
		if response.StatusCode < 500 {
			return nil, &QueryError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Message: message}
		}
		return nil, &QueryError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Message: message}
	}
	// Do returns as soon as the response headers arrive, but ClickHouse streams
	// the result while the caller reads the body. Wrap the body so the round-trip
	// is observed once at Close, making ch_millis span the full query lifecycle
	// including body streaming. The wrapper is installed on response.Body so
	// closing the body observes regardless of whether the caller takes the Rows
	// (Client.Query) or the unwrapped *http.Response (Client.Execute).
	rows := &httpRows{response: response, body: response.Body, ctx: ctx, start: start, purpose: req.Purpose}
	response.Body = rows
	return rows, nil
}

func (t *HTTPJSONTransport) Close() error {
	t.httpClient.CloseIdleConnections()
	return nil
}

type httpRows struct {
	response *http.Response
	body     io.ReadCloser
	ctx      context.Context
	start    time.Time
	purpose  QueryPurpose
	once     sync.Once
	// readErr holds the last non-EOF error seen while streaming the body. A 2xx
	// response can still fail mid-stream (timeout, reset, truncated body); io.EOF
	// is normal completion and is not recorded as a failure.
	readErr error
}

func (r *httpRows) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}
	return n, err
}

func (r *httpRows) Close() error {
	closeErr := r.body.Close()
	// Mirror the native transport's queryStatus convention: a Close error takes
	// precedence, otherwise fall back to the streaming (Read) error, so a
	// mid-stream failure after a 2xx response is recorded as an error rather than
	// a hardcoded success.
	observedErr := closeErr
	if observedErr == nil {
		observedErr = r.readErr
	}
	r.observe(observedErr)
	return closeErr
}

func (r *httpRows) observe(err error) {
	r.once.Do(func() {
		duration := time.Since(r.start)
		obs.FromContext(r.ctx).Observe(duration)
		observeQuery(TransportHTTP, r.purpose, queryStatus(err), duration)
	})
}
