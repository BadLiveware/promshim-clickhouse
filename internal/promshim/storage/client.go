package storage

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type Config struct {
	Endpoint              string
	NativeAddr            string
	Database              string
	Username              string
	Password              string
	Compression           string
	RequestTimeout        time.Duration
	Transport             TransportKind
	MaxOpenConns          int
	MaxIdleConns          int
	ConnMaxLifetime       time.Duration
	NativeSecure          bool
	TLSInsecureSkipVerify bool
	TLSServerName         string
	SettingsProfile       SettingsProfileConfig
}

type Client struct {
	transportKind   TransportKind
	transport       Transport
	settingsProfile SettingsProfileConfig
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
		return &Client{transportKind: TransportHTTP, transport: transport, settingsProfile: normalizeClientSettingsProfile(cfg)}, nil
	case TransportNative:
		transport, err := NewNativeDriverTransport(NativeDriverTransportConfig{
			Addr:                  cfg.NativeAddr,
			Database:              cfg.Database,
			Username:              cfg.Username,
			Password:              cfg.Password,
			Compression:           cfg.Compression,
			RequestTimeout:        cfg.RequestTimeout,
			MaxOpenConns:          cfg.MaxOpenConns,
			MaxIdleConns:          cfg.MaxIdleConns,
			ConnMaxLifetime:       cfg.ConnMaxLifetime,
			Secure:                cfg.NativeSecure,
			TLSInsecureSkipVerify: cfg.TLSInsecureSkipVerify,
			TLSServerName:         cfg.TLSServerName,
		})
		if err != nil {
			return nil, err
		}
		return &Client{transportKind: TransportNative, transport: transport, settingsProfile: normalizeClientSettingsProfile(cfg)}, nil
	default:
		return nil, fmt.Errorf("unsupported clickhouse transport %q", transportKind)
	}
}

func (c *Client) TransportKind() TransportKind {
	return c.transportKind
}

func (c *Client) Query(ctx context.Context, req QueryRequest) (Rows, error) {
	prepared, err := c.prepareQueryRequest(req)
	if err != nil {
		return nil, err
	}
	return c.transport.Query(ctx, prepared)
}

func (c *Client) Ping(ctx context.Context) error {
	if transport, ok := c.transport.(interface{ Ping(context.Context) error }); ok {
		return transport.Ping(ctx)
	}
	rows, err := c.Query(ctx, QueryRequest{SQL: "SELECT 1", Format: ResultFormatJSONEachRow})
	if err != nil {
		return err
	}
	return rows.Close()
}

func (c *Client) Execute(ctx context.Context, sql string, params map[string]string) (*http.Response, error) {
	return c.ExecuteWithSettings(ctx, sql, params, nil)
}

func (c *Client) ExecuteWithSettings(ctx context.Context, sql string, params map[string]string, settings map[string]any) (*http.Response, error) {
	rows, err := c.Query(ctx, QueryRequest{SQL: sql, Params: params, Settings: settings, Format: ResultFormatJSONEachRow})
	if err != nil {
		return nil, err
	}
	if httpRows, ok := rows.(*httpRows); ok {
		return httpRows.response, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: rows}, nil
}

func (c *Client) Close() error {
	return c.transport.Close()
}

func (c *Client) prepareQueryRequest(req QueryRequest) (QueryRequest, error) {
	resolution := ResolveSettingsProfile(c.settingsProfile, req.Purpose, "", "")
	settings, err := MergeProfileSettings(resolution.Settings, req.Settings)
	if err != nil {
		return QueryRequest{}, err
	}
	req.Settings = settings
	return req, nil
}

func normalizeClientSettingsProfile(cfg Config) SettingsProfileConfig {
	profile := cfg.SettingsProfile
	profile.Name = NormalizeSettingsProfileName(profile.Name)
	if profile.ClickHouseVersion == "" {
		profile.ClickHouseVersion = "26.3"
	}
	if profile.RequestTimeout <= 0 {
		profile.RequestTimeout = cfg.RequestTimeout
	}
	return profile
}
