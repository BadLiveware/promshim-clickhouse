package storage

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// fakeBody is an io.ReadCloser that streams a fixed payload and then returns a
// configurable terminal error, letting tests simulate a body that fails
// mid-stream after a 2xx response (io.EOF for normal completion, or a non-EOF
// error such as a timeout / connection reset).
type fakeBody struct {
	payload  []byte
	pos      int
	finalErr error // returned once payload is exhausted (io.EOF = clean finish)
	closeErr error
	closed   bool
}

func (b *fakeBody) Read(p []byte) (int, error) {
	if b.pos < len(b.payload) {
		n := copy(p, b.payload[b.pos:])
		b.pos += n
		return n, nil
	}
	return 0, b.finalErr
}

func (b *fakeBody) Close() error {
	b.closed = true
	return b.closeErr
}

func gatherHTTPStatus(t *testing.T, purpose QueryPurpose, status string) float64 {
	t.Helper()
	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	return metricCounterValue(families, "promshim_clickhouse_queries_total", map[string]string{
		"transport": "http",
		"purpose":   string(purpose),
		"status":    status,
	})
}

func drainAndClose(t *testing.T, rows *httpRows) error {
	t.Helper()
	buf := make([]byte, 4)
	for {
		_, err := rows.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// Streaming failure: stop reading, mirror how a real consumer bails.
			break
		}
	}
	return rows.Close()
}

// TestHTTPRowsRecordsSuccessOnCleanStream verifies the happy path: a body that
// streams to io.EOF and closes cleanly records status="success".
func TestHTTPRowsRecordsSuccessOnCleanStream(t *testing.T) {
	purpose := QueryPurpose("http_rows_success_test")
	rows := &httpRows{
		body:    &fakeBody{payload: []byte("row1\nrow2\n"), finalErr: io.EOF},
		ctx:     context.Background(),
		start:   time.Now(),
		purpose: purpose,
	}

	if err := drainAndClose(t, rows); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := gatherHTTPStatus(t, purpose, "success"); got != 1 {
		t.Fatalf("http success counter = %v, want 1", got)
	}
	if got := gatherHTTPStatus(t, purpose, "error"); got != 0 {
		t.Fatalf("http error counter = %v, want 0", got)
	}
}

// TestHTTPRowsRecordsErrorOnStreamFailure is the regression test for the
// finding: a 2xx response whose body fails mid-stream (non-EOF Read error) must
// be recorded as status="error", not a hardcoded success.
func TestHTTPRowsRecordsErrorOnStreamFailure(t *testing.T) {
	purpose := QueryPurpose("http_rows_stream_error_test")
	rows := &httpRows{
		body:    &fakeBody{payload: []byte("partial"), finalErr: errors.New("connection reset by peer")},
		ctx:     context.Background(),
		start:   time.Now(),
		purpose: purpose,
	}

	if err := drainAndClose(t, rows); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := gatherHTTPStatus(t, purpose, "error"); got != 1 {
		t.Fatalf("http error counter = %v, want 1 (mid-stream failure must not count as success)", got)
	}
	if got := gatherHTTPStatus(t, purpose, "success"); got != 0 {
		t.Fatalf("http success counter = %v, want 0", got)
	}
}

// TestHTTPRowsRecordsErrorOnCloseFailure verifies a Close error is recorded as a
// failure even when streaming itself finished cleanly (Close error takes
// precedence, matching the native transport's queryStatus convention).
func TestHTTPRowsRecordsErrorOnCloseFailure(t *testing.T) {
	purpose := QueryPurpose("http_rows_close_error_test")
	rows := &httpRows{
		body:    &fakeBody{payload: []byte("row\n"), finalErr: io.EOF, closeErr: errors.New("close failed")},
		ctx:     context.Background(),
		start:   time.Now(),
		purpose: purpose,
	}

	if err := drainAndClose(t, rows); err == nil {
		t.Fatal("Close error = nil, want close failure")
	}

	if got := gatherHTTPStatus(t, purpose, "error"); got != 1 {
		t.Fatalf("http error counter = %v, want 1", got)
	}
	if got := gatherHTTPStatus(t, purpose, "success"); got != 0 {
		t.Fatalf("http success counter = %v, want 0", got)
	}
}

// TestHTTPRowsObservesOnce ensures a double Close records the round-trip exactly
// once, mirroring the native wrapper's guard.
func TestHTTPRowsObservesOnce(t *testing.T) {
	purpose := QueryPurpose("http_rows_once_test")
	rows := &httpRows{
		body:    &fakeBody{payload: []byte("row\n"), finalErr: io.EOF},
		ctx:     context.Background(),
		start:   time.Now(),
		purpose: purpose,
	}

	if err := drainAndClose(t, rows); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if got := gatherHTTPStatus(t, purpose, "success"); got != 1 {
		t.Fatalf("http success counter = %v, want 1 (observe must fire once)", got)
	}
}
