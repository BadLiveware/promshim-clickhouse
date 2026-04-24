package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
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

	requestURL := t.baseURL.String()
	if tag := obs.LogCommentFromContext(ctx); tag != "" {
		u := *t.baseURL
		q := u.Query()
		q.Set("log_comment", tag)
		u.RawQuery = q.Encode()
		requestURL = u.String()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, &body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", t.basicAuth)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	start := time.Now()
	response, err := t.httpClient.Do(request)
	obs.FromContext(ctx).Observe(time.Since(start))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		defer response.Body.Close()
		var payload bytes.Buffer
		_, _ = payload.ReadFrom(response.Body)
		message := strings.TrimSpace(payload.String())
		if message == "" {
			message = response.Status
		}
		if response.StatusCode < 500 {
			return nil, &QueryError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Message: message}
		}
		return nil, &QueryError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Message: message}
	}
	return &httpRows{response: response}, nil
}

func (t *HTTPJSONTransport) Close() error {
	t.httpClient.CloseIdleConnections()
	return nil
}

type httpRows struct {
	response *http.Response
}

func (r *httpRows) Read(p []byte) (int, error) {
	return r.response.Body.Read(p)
}

func (r *httpRows) Close() error {
	return r.response.Body.Close()
}
