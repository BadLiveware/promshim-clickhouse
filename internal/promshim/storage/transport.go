package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// TransportKind identifies the ClickHouse wire protocol used for query
// execution.
type TransportKind string

const (
	TransportHTTP   TransportKind = "http"
	TransportNative TransportKind = "native"
)

// ParseTransportKind parses a configured ClickHouse transport name.
func ParseTransportKind(value string) (TransportKind, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "", string(TransportHTTP):
		return TransportHTTP, nil
	case string(TransportNative):
		return TransportNative, nil
	default:
		return "", fmt.Errorf("unknown clickhouse transport %q (supported: %s, %s)", value, TransportHTTP, TransportNative)
	}
}

// ResultFormat describes the row encoding expected by the caller.
type ResultFormat string

const (
	ResultFormatJSONEachRow ResultFormat = "JSONEachRow"
)

// QueryPurpose classifies storage queries for future settings, metrics, and
// explain output. Existing Execute adapter call sites leave it unset.
type QueryPurpose string

const QueryPurposeSchemaDiscovery QueryPurpose = "schema_discovery"

// QueryRequest is the transport-neutral representation of a ClickHouse query.
type QueryRequest struct {
	SQL      string
	Params   map[string]string
	Settings map[string]any
	Format   ResultFormat
	Purpose  QueryPurpose
}

// Rows is the transport-neutral result stream returned to existing JSON
// decoders. Native driver implementations can provide a typed implementation in
// later phases while Client.Execute keeps the current response-body adapter.
type Rows interface {
	io.ReadCloser
}

// Transport executes ClickHouse queries and owns any protocol-specific
// connection resources.
type Transport interface {
	Query(ctx context.Context, req QueryRequest) (Rows, error)
	Close() error
}
