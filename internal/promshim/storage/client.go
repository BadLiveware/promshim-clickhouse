package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/obs"
)

type Config struct {
	Endpoint       string
	Username       string
	Password       string
	RequestTimeout time.Duration
}

type Client struct {
	baseURL    *url.URL
	basicAuth  string
	httpClient *http.Client
}

type QueryError struct {
	StatusCode int
	ErrorType  string
	Message    string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("%s: %s", e.ErrorType, e.Message)
}

func NewClient(cfg Config) (*Client, error) {
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	query := parsed.Query()
	query.Set("allow_experimental_time_series_table", "1")
	query.Set("output_format_json_quote_denormals", "1")
	parsed.RawQuery = query.Encode()

	credentials := base64.StdEncoding.EncodeToString([]byte(cfg.Username + ":" + cfg.Password))
	return &Client{
		baseURL:   parsed,
		basicAuth: "Basic " + credentials,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
	}, nil
}

func (c *Client) Execute(ctx context.Context, sql string, params map[string]string) (*http.Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("query", sql); err != nil {
		return nil, err
	}
	for key, value := range params {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	requestURL := c.baseURL.String()
	if tag := obs.LogCommentFromContext(ctx); tag != "" {
		u := *c.baseURL
		q := u.Query()
		q.Set("log_comment", tag)
		u.RawQuery = q.Encode()
		requestURL = u.String()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, &body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", c.basicAuth)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	start := time.Now()
	response, err := c.httpClient.Do(request)
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
	return response, nil
}
