package promshim

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

type ClickHouseClient struct {
	baseURL    *url.URL
	basicAuth  string
	httpClient *http.Client
}

func NewClickHouseClient(opts Options) (*ClickHouseClient, error) {
	parsed, err := url.Parse(opts.ClickHouseEndpoint)
	if err != nil {
		return nil, err
	}

	query := parsed.Query()
	query.Set("allow_experimental_time_series_table", "1")
	parsed.RawQuery = query.Encode()

	credentials := base64.StdEncoding.EncodeToString([]byte(opts.Username + ":" + opts.Password))
	return &ClickHouseClient{
		baseURL:   parsed,
		basicAuth: "Basic " + credentials,
		httpClient: &http.Client{
			Timeout: opts.RequestTimeout,
		},
	}, nil
}

func (c *ClickHouseClient) Execute(ctx context.Context, sql string, params map[string]string) (*http.Response, error) {
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

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL.String(), &body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", c.basicAuth)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	response, err := c.httpClient.Do(request)
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

type QueryError struct {
	StatusCode int
	ErrorType  string
	Message    string
}

func (e *QueryError) Error() string {
	return fmt.Sprintf("%s: %s", e.ErrorType, e.Message)
}
