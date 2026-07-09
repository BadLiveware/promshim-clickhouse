package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/obs"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// TestQueryNativeRowsObservesFullStreamingLifecycle proves the regression fix:
// with the native protocol conn.Query returns at dispatch/first-block time, so
// observing there under-reports heavy scans. The observed round-trip must span
// row iteration (where the scan actually streams), and must fire exactly once at
// Close — not at dispatch.
func TestQueryNativeRowsObservesFullStreamingLifecycle(t *testing.T) {
	const streamDelay = 40 * time.Millisecond
	transport := &NativeDriverTransport{conn: &fakeNativeConn{
		rows: &fakeNativeNextDelayRows{remaining: 2, nextDelay: streamDelay},
	}}

	ctx, metrics := obs.WithCHMetrics(context.Background())
	rows, err := transport.QueryNativeRows(ctx, QueryRequest{SQL: "SELECT 1", Purpose: QueryPurpose("native_rows_streaming_test")})
	if err != nil {
		t.Fatalf("QueryNativeRows: %v", err)
	}

	// Dispatch has returned but no streaming has happened yet: nothing observed.
	if got := metrics.Roundtrips(); got != 0 {
		t.Fatalf("roundtrips after dispatch = %d, want 0 (must not observe before streaming)", got)
	}

	// The scan streams here, while the caller iterates rows.
	for rows.Next() {
		if err := rows.Scan(); err != nil {
			t.Fatalf("Scan: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := metrics.Roundtrips(); got != 1 {
		t.Fatalf("roundtrips = %d, want 1", got)
	}
	// Two Next() calls each sleep streamDelay; the observed duration must include
	// that streaming time, not just the (near-zero) dispatch latency.
	if got := metrics.Millis(); got < (2 * streamDelay).Milliseconds() {
		t.Fatalf("millis = %d, want >= %d (streaming time must be counted, not just dispatch)", got, (2 * streamDelay).Milliseconds())
	}
}

// TestQueryNativeRowsObservesDispatchErrorImmediately covers the error branch:
// when conn.Query returns an error there are no rows to iterate, so the dispatch
// latency is observed right away and no rows wrapper is returned.
func TestQueryNativeRowsObservesDispatchErrorImmediately(t *testing.T) {
	dispatchErr := errors.New("dispatch failed")
	transport := &NativeDriverTransport{conn: &fakeNativeConn{queryErr: dispatchErr}}

	ctx, metrics := obs.WithCHMetrics(context.Background())
	rows, err := transport.QueryNativeRows(ctx, QueryRequest{SQL: "SELECT 1", Purpose: QueryPurpose("native_rows_dispatch_error_test")})
	if !errors.Is(err, dispatchErr) {
		t.Fatalf("QueryNativeRows error = %v, want dispatch error", err)
	}
	if rows != nil {
		t.Fatalf("rows = %v, want nil on dispatch error", rows)
	}
	if got := metrics.Roundtrips(); got != 1 {
		t.Fatalf("roundtrips = %d, want 1 (dispatch error observed immediately)", got)
	}
}

// TestNativeObservedRowsObservesOnce ensures a double Close (or Close after an
// iteration error) records the round-trip exactly once — no double counting.
func TestNativeObservedRowsObservesOnce(t *testing.T) {
	ctx, metrics := obs.WithCHMetrics(context.Background())
	rows := &nativeObservedRows{
		rows:    &fakeNativeNextDelayRows{remaining: 0, iterErr: errors.New("iteration failed")},
		ctx:     ctx,
		start:   time.Now(),
		purpose: QueryPurpose("native_rows_once_test"),
	}

	if err := rows.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := metrics.Roundtrips(); got != 1 {
		t.Fatalf("roundtrips = %d, want 1 (observe must fire once)", got)
	}
}

// TestNativeObservedRowsPassthrough exercises the wrapper's delegation methods.
func TestNativeObservedRowsPassthrough(t *testing.T) {
	totalsErr := errors.New("totals")
	structErr := errors.New("struct")
	fake := &fakeNativeNextDelayRows{
		remaining: 1,
		columns:   []string{"tags", "value"},
		totalsErr: totalsErr,
		structErr: structErr,
		hasData:   true,
	}
	rows := &nativeObservedRows{rows: fake, ctx: context.Background(), start: time.Now()}

	if !rows.Next() {
		t.Fatal("Next() = false, want true")
	}
	if err := rows.ScanStruct(nil); !errors.Is(err, structErr) {
		t.Fatalf("ScanStruct err = %v, want %v", err, structErr)
	}
	if err := rows.Totals(); !errors.Is(err, totalsErr) {
		t.Fatalf("Totals err = %v, want %v", err, totalsErr)
	}
	if got := rows.Columns(); len(got) != 2 || got[0] != "tags" {
		t.Fatalf("Columns = %v, want [tags value]", got)
	}
	if got := rows.ColumnTypes(); got != nil {
		t.Fatalf("ColumnTypes = %v, want nil", got)
	}
	if !rows.HasData() {
		t.Fatal("HasData() = false, want true")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

// fakeNativeConn is a chdriver.Conn stub whose Query returns a preconfigured
// rows value (or error). Only Query is used by these tests.
type fakeNativeConn struct {
	rows     chdriver.Rows
	queryErr error
}

func (c *fakeNativeConn) Query(ctx context.Context, query string, args ...any) (chdriver.Rows, error) {
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return c.rows, nil
}

func (c *fakeNativeConn) Contributors() []string                          { return nil }
func (c *fakeNativeConn) ServerVersion() (*chdriver.ServerVersion, error) { return nil, nil }
func (c *fakeNativeConn) Select(ctx context.Context, dest any, query string, args ...any) error {
	return nil
}
func (c *fakeNativeConn) QueryRow(ctx context.Context, query string, args ...any) chdriver.Row {
	return nil
}
func (c *fakeNativeConn) PrepareBatch(ctx context.Context, query string, opts ...chdriver.PrepareBatchOption) (chdriver.Batch, error) {
	return nil, nil
}
func (c *fakeNativeConn) Exec(ctx context.Context, query string, args ...any) error { return nil }
func (c *fakeNativeConn) AsyncInsert(ctx context.Context, query string, wait bool, args ...any) error {
	return nil
}
func (c *fakeNativeConn) Ping(context.Context) error { return nil }
func (c *fakeNativeConn) Stats() chdriver.Stats      { return chdriver.Stats{} }
func (c *fakeNativeConn) Close() error               { return nil }

// fakeNativeNextDelayRows is a chdriver.Rows stub whose Next() sleeps to
// simulate row streaming/scan time.
type fakeNativeNextDelayRows struct {
	remaining int
	nextDelay time.Duration
	iterErr   error
	totalsErr error
	structErr error
	columns   []string
	hasData   bool
}

func (r *fakeNativeNextDelayRows) Next() bool {
	if r.remaining <= 0 {
		return false
	}
	r.remaining--
	if r.nextDelay > 0 {
		time.Sleep(r.nextDelay)
	}
	return true
}
func (r *fakeNativeNextDelayRows) Scan(dest ...any) error             { return nil }
func (r *fakeNativeNextDelayRows) ScanStruct(dest any) error          { return r.structErr }
func (r *fakeNativeNextDelayRows) ColumnTypes() []chdriver.ColumnType { return nil }
func (r *fakeNativeNextDelayRows) Totals(dest ...any) error           { return r.totalsErr }
func (r *fakeNativeNextDelayRows) Columns() []string                  { return r.columns }
func (r *fakeNativeNextDelayRows) Close() error                       { return nil }
func (r *fakeNativeNextDelayRows) Err() error                         { return r.iterErr }
func (r *fakeNativeNextDelayRows) HasData() bool                      { return r.hasData }
