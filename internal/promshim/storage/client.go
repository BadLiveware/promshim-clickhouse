package storage

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type Config struct {
	Endpoint        string
	NativeAddr      string
	Database        string
	Username        string
	Password        string
	Compression     string
	RequestTimeout  time.Duration
	Transport       TransportKind
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type Client struct {
	transportKind TransportKind
	transport     Transport
	httpJSON      *HTTPJSONTransport
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
	transportKind, err := ParseTransportKind(string(cfg.Transport))
	if err != nil {
		return nil, err
	}

	switch transportKind {
	case TransportHTTP:
		transport, err := NewHTTPJSONTransport(HTTPJSONTransportConfig{
			Endpoint:       cfg.Endpoint,
			Username:       cfg.Username,
			Password:       cfg.Password,
			RequestTimeout: cfg.RequestTimeout,
		})
		if err != nil {
			return nil, err
		}
		return &Client{transportKind: TransportHTTP, transport: transport, httpJSON: transport}, nil
	case TransportNative:
		transport, err := NewNativeDriverTransport(NativeDriverTransportConfig{
			Addr:            cfg.NativeAddr,
			Database:        cfg.Database,
			Username:        cfg.Username,
			Password:        cfg.Password,
			Compression:     cfg.Compression,
			RequestTimeout:  cfg.RequestTimeout,
			MaxOpenConns:    cfg.MaxOpenConns,
			MaxIdleConns:    cfg.MaxIdleConns,
			ConnMaxLifetime: cfg.ConnMaxLifetime,
		})
		if err != nil {
			return nil, err
		}
		httpJSON, err := NewHTTPJSONTransport(HTTPJSONTransportConfig{
			Endpoint:       cfg.Endpoint,
			Username:       cfg.Username,
			Password:       cfg.Password,
			RequestTimeout: cfg.RequestTimeout,
		})
		if err != nil {
			_ = transport.Close()
			return nil, err
		}
		return &Client{transportKind: TransportNative, transport: transport, httpJSON: httpJSON}, nil
	default:
		return nil, fmt.Errorf("unsupported clickhouse transport %q", transportKind)
	}
}

func (c *Client) TransportKind() TransportKind {
	return c.transportKind
}

func (c *Client) Query(ctx context.Context, req QueryRequest) (Rows, error) {
	return c.transport.Query(ctx, req)
}

func (c *Client) Execute(ctx context.Context, sql string, params map[string]string) (*http.Response, error) {
	rows, err := c.Query(ctx, QueryRequest{SQL: sql, Params: params, Format: ResultFormatJSONEachRow})
	if err != nil {
		return nil, err
	}
	if httpRows, ok := rows.(*httpRows); ok {
		return httpRows.response, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: rows}, nil
}

func (c *Client) ExecuteHTTPJSON(ctx context.Context, sql string, params map[string]string) (*http.Response, error) {
	if c.httpJSON == nil {
		return nil, fmt.Errorf("HTTP JSON compatibility transport is not configured")
	}
	rows, err := c.httpJSON.Query(ctx, QueryRequest{SQL: sql, Params: params, Format: ResultFormatJSONEachRow})
	if err != nil {
		return nil, err
	}
	if httpRows, ok := rows.(*httpRows); ok {
		return httpRows.response, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: rows}, nil
}

func (c *Client) Close() error {
	err := c.transport.Close()
	if c.httpJSON != nil && c.httpJSON != c.transport {
		if httpErr := c.httpJSON.Close(); err == nil {
			err = httpErr
		}
	}
	return err
}
