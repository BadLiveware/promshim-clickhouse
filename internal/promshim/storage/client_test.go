package storage

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
)

func TestHTTPJSONTransportExecutePreservesHTTPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Basic dXNlcjpwYXNz" {
			t.Fatalf("authorization = %q", got)
		}
		query := r.URL.Query()
		if got := query.Get("allow_experimental_time_series_table"); got != "1" {
			t.Fatalf("allow_experimental_time_series_table = %q, want 1", got)
		}
		if got := query.Get("output_format_json_quote_denormals"); got != "1" {
			t.Fatalf("output_format_json_quote_denormals = %q, want 1", got)
		}
		if got := query.Get("log_comment"); got != "bench-a" {
			t.Fatalf("log_comment = %q, want bench-a", got)
		}
		if got := query.Get("max_execution_time"); got != "1" {
			t.Fatalf("max_execution_time = %q, want 1", got)
		}
		if got := query.Get("readonly"); got != "2" {
			t.Fatalf("readonly = %q, want 2", got)
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("parse content type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Fatalf("content type = %q, want multipart/form-data", mediaType)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got := r.FormValue("query"); got != "SELECT {s:String}" {
			t.Fatalf("query field = %q", got)
		}
		if got := r.FormValue("param_s"); got != "value" {
			t.Fatalf("param_s field = %q", got)
		}
		_, _ = io.WriteString(w, "{\"ok\":true}\n")
	}))
	defer server.Close()

	client, err := NewClient(Config{
		Endpoint:       server.URL,
		Username:       "user",
		Password:       "pass",
		RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := obs.WithLogComment(context.Background(), "bench-a")
	ctx, metrics := obs.WithCHMetrics(ctx)
	response, err := client.Execute(ctx, "SELECT {s:String}", map[string]string{"param_s": "value"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if got := string(payload); got != "{\"ok\":true}\n" {
		t.Fatalf("response body = %q", got)
	}
	if got := metrics.Roundtrips(); got != 1 {
		t.Fatalf("roundtrips = %d, want 1", got)
	}
}

func TestHTTPJSONTransportMapsHTTPStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		statusCode int
		wantStatus int
		wantType   string
	}{
		{name: "bad request", statusCode: http.StatusBadRequest, wantStatus: http.StatusBadRequest, wantType: "bad_data"},
		{name: "server error", statusCode: http.StatusInternalServerError, wantStatus: http.StatusBadGateway, wantType: "execution"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "clickhouse failed", tc.statusCode)
			}))
			defer server.Close()

			client, err := NewClient(Config{Endpoint: server.URL, RequestTimeout: time.Second})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer func() { _ = client.Close() }()

			_, err = client.Execute(context.Background(), "SELECT 1", nil)
			queryErr, ok := err.(*QueryError)
			if !ok {
				t.Fatalf("error = %T %v, want *QueryError", err, err)
			}
			if queryErr.StatusCode != tc.wantStatus || queryErr.ErrorType != tc.wantType {
				t.Fatalf("QueryError = status %d type %q, want status %d type %q", queryErr.StatusCode, queryErr.ErrorType, tc.wantStatus, tc.wantType)
			}
			if !strings.Contains(queryErr.Message, "clickhouse failed") {
				t.Fatalf("message = %q, want ClickHouse body", queryErr.Message)
			}
		})
	}
}

func TestNewClientRejectsUnknownTransport(t *testing.T) {
	_, err := NewClient(Config{Endpoint: "http://example.invalid", Transport: TransportKind("grpc")})
	if err == nil || !strings.Contains(err.Error(), "unknown clickhouse transport") {
		t.Fatalf("unknown transport error = %v, want unknown transport error", err)
	}
}

func TestNativeTransportExecuteRequiresTypedDecoder(t *testing.T) {
	client, err := NewClient(Config{Transport: TransportNative, NativeAddr: "127.0.0.1:1", RequestTimeout: time.Millisecond})
	if err != nil {
		t.Fatalf("NewClient native: %v", err)
	}
	defer func() { _ = client.Close() }()

	_, err = client.Execute(context.Background(), "SELECT 1", nil)
	if err == nil || !strings.Contains(err.Error(), ErrNativeRowsNeedTypedDecoder.Error()) {
		t.Fatalf("Execute native error = %v, want typed decoder error", err)
	}
}

func TestNewClientRejectsUnknownNativeCompression(t *testing.T) {
	_, err := NewClient(Config{Transport: TransportNative, Compression: "snappy"})
	if err == nil || !strings.Contains(err.Error(), "unknown clickhouse native compression") {
		t.Fatalf("native compression error = %v, want unknown compression error", err)
	}
}
