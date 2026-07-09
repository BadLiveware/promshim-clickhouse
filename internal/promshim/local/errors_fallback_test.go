package local

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/ClickHouse/clickhouse-go/v2"
)

// TestExecutionFallbackEligible pins the execution-class vs client-class
// boundary for the execution-time local fallback with representative
// ClickHouse error shapes from both transports.
func TestExecutionFallbackEligible(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		// — execution-class (ClickHouse/server-side): eligible —
		{
			name: "ch_http_500_materialized_cte_rejection",
			err:  &storage.QueryError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Message: "Code: 344. DB::Exception: Reference to materialized CTE cannot be used in this context"},
			want: true,
		},
		{
			name: "ch_http_500_memory_limit",
			err:  &storage.QueryError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Message: "Code: 241. DB::Exception: Memory limit (for query) exceeded"},
			want: true,
		},
		{
			name: "ch_native_exception_unknown_table",
			err:  &clickhouse.Exception{Code: 60, Name: "UNKNOWN_TABLE", Message: "Table observability.prometheus does not exist"},
			want: true,
		},
		{
			name: "wrapped_ch_native_exception",
			err:  fmt.Errorf("executing native sql: %w", &clickhouse.Exception{Code: 160, Name: "TOO_SLOW", Message: "estimated query execution time is too long"}),
			want: true,
		},
		{
			name: "explicit_execution_error",
			err:  NewExecutionErrorf("decoding rows: unexpected EOF"),
			want: true,
		},
		{
			name: "unknown_error_defaults_to_execution",
			err:  fmt.Errorf("connection refused"),
			want: true,
		},
		// — client-class: never eligible, must keep 4xx —
		{
			name: "ch_http_4xx_bad_data_syntax_error",
			err:  &storage.QueryError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Message: "Code: 62. DB::Exception: Syntax error: failed at position 1"},
			want: false,
		},
		{
			name: "ch_http_4xx_bad_data_illegal_argument",
			err:  &storage.QueryError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Message: "Code: 43. DB::Exception: Illegal type of argument"},
			want: false,
		},
		{
			name: "duplicate_series_normalized_to_bad_data",
			err:  &storage.QueryError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Message: "Code: 395. DB::Exception: found duplicate series for the match group on the right side"},
			want: false,
		},
		{
			name: "bad_data_error",
			err:  NewBadDataErrorf("query result would return 10 series, exceeding configured limit 1"),
			want: false,
		},
		{
			name: "unsupported_error",
			err:  NewUnsupportedErrorf("native lowering does not support this shape"),
			want: false,
		},
		// — context cancellation / deadline: never eligible —
		{
			name: "context_canceled",
			err:  fmt.Errorf("executing query: %w", context.Canceled),
			want: false,
		},
		{
			name: "context_deadline",
			err:  fmt.Errorf("executing query: %w", context.DeadlineExceeded),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExecutionFallbackEligible(tc.err); got != tc.want {
				t.Fatalf("ExecutionFallbackEligible(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestExecutionFallbackEligibleContextThroughShimWrapping reproduces the exact
// wrapping the evaluation path applies to a context error and asserts the
// sentinel identity survives it, so a canceled / timed-out evaluation is never
// misclassified as retryable execution-class. The chunked range executor wraps
// child errors via WithInternalContext (see chunkedRangePlan.evaluate), which
// runs NormalizeInternalError first — the path that previously erased the
// context.Canceled / context.DeadlineExceeded sentinel by copying only its
// message into a fresh promshimError with no Unwrap chain.
func TestExecutionFallbackEligibleContextThroughShimWrapping(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	}
	for _, sentinel := range sentinels {
		t.Run(sentinel.name, func(t *testing.T) {
			// NormalizeInternalError alone must preserve the sentinel.
			normalized := NormalizeInternalError(sentinel.err)
			if !errors.Is(normalized, sentinel.err) {
				t.Fatalf("NormalizeInternalError erased the %s sentinel: %v", sentinel.name, normalized)
			}
			if ExecutionFallbackEligible(normalized) {
				t.Fatalf("normalized %s must not be fallback-eligible", sentinel.name)
			}

			// The chunked-executor wrapping shape: WithInternalContext (which
			// normalizes internally) applied to the raw context error.
			wrapped := WithInternalContext(sentinel.err, "executing chunked range subquery start=%s end=%s", "t0", "t1")
			if !errors.Is(wrapped, sentinel.err) {
				t.Fatalf("WithInternalContext erased the %s sentinel: %v", sentinel.name, wrapped)
			}
			if ExecutionFallbackEligible(wrapped) {
				t.Fatalf("WithInternalContext-wrapped %s must not be fallback-eligible", sentinel.name)
			}

			// Doubly wrapped, as nested evaluation contexts would produce.
			doubleWrapped := WithInternalContext(wrapped, "merging chunked range subquery")
			if !errors.Is(doubleWrapped, sentinel.err) {
				t.Fatalf("doubly-wrapped %s lost its sentinel: %v", sentinel.name, doubleWrapped)
			}
			if ExecutionFallbackEligible(doubleWrapped) {
				t.Fatalf("doubly-wrapped %s must not be fallback-eligible", sentinel.name)
			}
		})
	}
}
