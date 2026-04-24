# ClickHouse driver transport migration plan

## Purpose

Promshim currently talks to ClickHouse through HTTP multipart requests and asks
ClickHouse to return `FORMAT JSONEachRow`. That was useful for early correctness
work because the SQL was easy to inspect and every row could be decoded with
standard JSON tooling. It is no longer the right baseline for a Prometheus API
compatibility layer that repeatedly executes structured, generated SQL with known
schemas.

This plan adds a first-class ClickHouse driver transport, validates it against
the existing HTTP transport, then makes the driver the default for promshim query
execution. The goals are lower bytes in flight, lower promshim CPU/allocation
pressure, better connection pooling, cleaner per-query settings, and a better
foundation for cost-based execution (CBE).

## Requirements

1. Preserve Prometheus-compatible query results exactly, subject only to the
   existing documented compliance deviations.
2. Keep the existing HTTP transport available during migration for side-by-side
   validation and rollback.
3. Do not change PromQL planning, native SQL semantics, or fallback semantics as
   part of the transport migration.
4. Preserve `force_supported`: native-only visibility must still mean a
   `native_sql` root, independent of transport.
5. Preserve ClickHouse observability: `log_comment`, round-trip count, query
   duration accounting, and explain/debug behavior must continue to work.
6. Support per-query ClickHouse settings through the new transport so future CBE
   can choose settings such as `max_threads`, cache usage, and compression.
7. Keep generated SQL reviewable. The SQL text used by the driver must still be
   visible in explain/profile artifacts.
8. Make the migration measurable with before/after benchmark artifacts for short
   and long-range corpora.

## Non-goals

- Do not implement CBE in this migration.
- Do not tune ClickHouse server settings in the same change, except for settings
  required for protocol parity such as `allow_experimental_time_series_table`.
- Do not remove HTTP support until driver parity and benchmark evidence are in
  place.
- Do not change the ClickHouse `TimeSeries` schema or introduce helper tables.
- Do not switch the harness remote-write ingestion path; ClickHouse remote-write
  remains HTTP.

## Current state

Important current code paths in the open-source cleanup repo:

- `internal/promshim/storage/client.go` owns the HTTP multipart client.
- `internal/promshim/storage/schema/schema.go` appends
  `SETTINGS allow_experimental_time_series_table = 1` and
  `FORMAT JSONEachRow`.
- `internal/promshim/service.go`, `internal/promshim/local/planner.go`, and
  `internal/promshim/local/native_subtree.go` call `client.Execute(...)` and
  decode response bodies.
- SQL builders use ClickHouse query parameters in `{name:Type}` form and pass
  `map[string]string` values to the HTTP client.
- `obs.LogCommentFromContext(ctx)` is propagated as an HTTP query parameter.
- Response headers expose promshim-observed ClickHouse round trips and elapsed
  time.

The native driver migration should first isolate those transport assumptions
instead of editing every planner/renderer path directly.

## Target architecture

Introduce a storage transport boundary:

```go
type Transport interface {
    Query(ctx context.Context, req QueryRequest) (Rows, error)
    Close() error
}

type QueryRequest struct {
    SQL      string
    Params   map[string]string
    Settings map[string]any
    Format   ResultFormat
    Purpose  QueryPurpose
}
```

Then provide two implementations:

- `HTTPJSONTransport` — current behavior, retained for parity and rollback.
- `NativeDriverTransport` — ClickHouse Go driver over the native protocol.

`storage.Client` can remain the public dependency initially, but internally it
should delegate to the selected transport. That limits call-site churn.

Configuration:

```text
PROM_SHIM_CLICKHOUSE_TRANSPORT=http|native
PROM_SHIM_CLICKHOUSE_ENDPOINT=http://127.0.0.1:8123/
PROM_SHIM_CLICKHOUSE_NATIVE_ADDR=127.0.0.1:9000
PROM_SHIM_CLICKHOUSE_DATABASE=observability
PROM_SHIM_CLICKHOUSE_USERNAME=default
PROM_SHIM_CLICKHOUSE_PASSWORD=otel
PROM_SHIM_CLICKHOUSE_COMPRESSION=off|lz4|zstd
PROM_SHIM_CLICKHOUSE_MAX_OPEN_CONNS=10
PROM_SHIM_CLICKHOUSE_MAX_IDLE_CONNS=10
PROM_SHIM_CLICKHOUSE_CONN_MAX_LIFETIME_SECONDS=3600
```

Default migration sequence:

1. initial PR: default `http`;
2. parity/bench PR: default still `http`, CI/harness exercises both;
3. flip PR: default `native` after artifacts show parity.

## Result formats

The immediate driver goal is not to introduce a custom binary protocol parser;
the driver already returns typed columnar blocks. Keep the logical result schema
stable and adapt decoding behind the transport boundary.

Important row families:

| Row family | Current representation | Driver target |
|---|---|---|
| Instant vector rows | JSON fields for `tags`, `timestamp`, `value` | typed scan into tags array/map, timestamp, float64 |
| Range matrix rows | JSON fields for `tags`, `timestamps`, `values` or row-expanded forms | typed scan into arrays/columns matching existing SQL result shape |
| Metadata label rows | JSON string/array fields | typed string/array scan |
| Whole-query delegation rows | ClickHouse `prometheusQuery*` JSON-like output rows | typed scan if possible; otherwise keep HTTP fallback until decoded |

The first driver implementation can still ask ClickHouse for `FORMAT
JSONEachRow` only if needed for a hard-to-type delegation path, but native SQL
and metadata paths should move to typed driver rows. Any temporary mixed-mode
path must be explicit in explain/metrics.

## Parameter handling

The migration must verify the ClickHouse Go driver's support for the current
`{name:Type}` parameter syntax.

Plan:

1. Add a small integration test that runs:

   ```sql
   SELECT {s:String}, {i:Int64}, fromUnixTimestamp64Milli({ts:Int64})
   ```

   through the driver with named parameters.
2. If the driver accepts `{name:Type}` directly, keep existing SQL unchanged and
   convert `map[string]string` to driver named parameters.
3. If not, add a storage-local parameter adapter that rewrites placeholders to
   the driver's named-parameter form and preserves type information.
4. Convert parameter values according to the ClickHouse type in the placeholder
   rather than relying on server-side HTTP string parsing forever.

Do not hand-edit renderer SQL to a driver-specific syntax unless the adapter
approach is proven infeasible.

## Error handling

Map driver errors to the same public error categories as HTTP:

| Source | Public Prometheus error type |
|---|---|
| invalid query / ClickHouse bad request | `bad_data` |
| timeout, connection refused, server unavailable | `execution` / bad gateway equivalent |
| context cancellation | request cancellation / timeout behavior matching current HTTP path |
| driver scan/type mismatch | internal execution error with query purpose and transport context |

Keep ClickHouse's original message in internal context, but avoid leaking
sensitive DSN/password details.

## Observability

Preserve and extend current observations:

- `obs.FromContext(ctx).Observe(...)` still measures ClickHouse request time.
- Round-trip count increments once per driver query.
- `log_comment` must be set through driver query settings/context.
- Add transport labels to metrics and explain output:

```text
promshim_clickhouse_queries_total{transport,purpose,status}
promshim_clickhouse_query_duration_seconds{transport,purpose}
promshim_clickhouse_rows_decoded_total{transport,purpose}
promshim_clickhouse_decode_duration_seconds{transport,purpose}
promshim_clickhouse_decode_errors_total{transport,purpose,reason}
```

Response/explain additions:

```text
X-Promshim-CH-Transport: native|http
```

Explain plan metadata should include transport, settings, and whether any path
fell back from native-driver typed rows to HTTP/JSON compatibility mode.

## Implementation phases

### Phase 1. Isolate the transport boundary

Goal: make the existing HTTP path implement an interface without behavior
changes.

Code areas:

- `internal/promshim/storage/client.go`
- `internal/promshim/storage/schema/schema.go`
- call sites in `service.go`, `local/planner.go`, and `local/native_subtree.go`

Tasks:

1. Introduce `QueryRequest`, `Transport`, and `Rows` abstractions in
   `internal/promshim/storage`.
2. Wrap the current HTTP multipart implementation as `HTTPJSONTransport`.
3. Keep `storage.Client.Execute(...)` or provide a compatibility adapter so
   existing call sites can migrate incrementally.
4. Add `PROM_SHIM_CLICKHOUSE_TRANSPORT` parsing with only `http` supported in
   this phase.
5. Preserve all current unit tests and generated SQL expectations.

Validation:

```bash
go test ./internal/promshim/storage ./internal/promshim/local ./internal/promshim
./scripts/run-compliance.sh --skip-native
```

Risks / notes:

- This phase should be refactor-only. Any response diff means the abstraction
  leaked behavior.

### Phase 2. Add native driver connectivity and scalar smoke tests

Goal: prove the driver can connect, pass settings, pass parameters, and return
basic typed rows.

Code areas:

- `go.mod`
- `internal/promshim/storage/driver_transport.go`
- harness ClickHouse port/config if native port is not exposed in the new repo

Tasks:

1. Add `github.com/ClickHouse/clickhouse-go/v2`.
2. Add native connection configuration, including address, auth, database,
   compression, pool size, TLS placeholder fields, and request timeout.
3. Implement `NativeDriverTransport` with `Ping`, `Close`, context timeouts,
   and settings injection.
4. Add integration tests for:
   - connection;
   - `allow_experimental_time_series_table` setting;
   - `log_comment` propagation;
   - named parameters for `String`, `Int64`, `Float64`, and `DateTime64` inputs;
   - denormal float behavior (`NaN`, `Inf`, `-Inf`) if supported by the driver
     scan path.
5. Keep the production default as `http`.

Validation:

```bash
go test ./internal/promshim/storage -run Driver
PROM_SHIM_CLICKHOUSE_TRANSPORT=native go test ./internal/promshim/storage -run Integration
```

Risks / notes:

- Driver parameter syntax is the highest-risk unknown. Resolve it before moving
  any promshim query path.
- Ensure the harness exposes the native TCP port only for local validation; do
  not require it for remote-write ingestion.

### Phase 3. Typed decoding for native SQL and metadata paths

Goal: serve repository-owned native SQL and metadata queries through the driver
without changing logical results.

Code areas:

- `internal/promshim/local/result_decode.go`
- `internal/promshim/service.go`
- `internal/promshim/storage/*`
- native renderer result row contracts

Tasks:

1. Identify the concrete result schemas produced by native SQL lowering:
   - instant vector;
   - range matrix;
   - scalar roots;
   - metadata labels/series;
   - histogram helper outputs.
2. Add typed row decoders for those schemas.
3. Remove `FORMAT JSONEachRow` only for driver-executed typed paths. Keep SQL
   builder snapshots stable for HTTP, or make the suffix transport-selected at
   execution time instead of plan/render time.
4. Preserve timestamp, stale-NaN, label ordering, and denormal float semantics.
5. Add a transport parity harness that runs the same query once with HTTP and
   once with native driver and compares Prometheus JSON output.

Validation:

```bash
go test ./internal/promshim/...
PROM_SHIM_CLICKHOUSE_TRANSPORT=http ./scripts/run-harness.sh
PROM_SHIM_CLICKHOUSE_TRANSPORT=native ./scripts/run-harness.sh
PROM_SHIM_CLICKHOUSE_TRANSPORT=native ./scripts/run-compliance.sh --skip-native
```

Risks / notes:

- `FORMAT JSONEachRow` is currently embedded in many SQL snapshots. Prefer a
  small trailer abstraction over broad renderer churn.
- Driver scanning of arrays/tuples/maps may require exact Go destination types;
  isolate those conversions in storage, not in planners.

### Phase 4. Whole-query delegation and subtree delegation parity

Goal: cover ClickHouse `prometheusQuery(...)` and `prometheusQueryRange(...)`
paths through the driver, or explicitly retain HTTP for only those paths until
ClickHouse/driver row shapes are understood.

Code areas:

- whole-query delegation in `service.go`
- subtree delegation in `local/planner.go` and `local/native_subtree.go`
- ClickHouse PromQL result decoding

Tasks:

1. Capture sample driver row schemas for `prometheusQuery(...)` and
   `prometheusQueryRange(...)`.
2. Implement typed decoding if the schema is stable and practical.
3. If the schema is not practical, keep delegation on HTTP temporarily and mark
   the transport as `http_json_delegation` in explain/metrics.
4. Validate that strategy headers still report `delegated_promql` or
   `native_sql` based on execution strategy, not transport.

Validation:

```bash
PROM_SHIM_CLICKHOUSE_TRANSPORT=native ./scripts/run-compliance.sh
PROM_SHIM_CLICKHOUSE_TRANSPORT=native ./scripts/run-harness.sh
```

Risks / notes:

- Transport fallback must not be confused with execution fallback. A delegated
  query served over HTTP is still tier 1, not tier 3/4.

### Phase 5. Benchmark and allocation comparison

Goal: quantify driver impact before changing defaults or calibrating CBE.

Tasks:

1. Run short fixture benchmarks for both transports:

   ```bash
   PROM_SHIM_CLICKHOUSE_TRANSPORT=http \
     ./scripts/run-bench.sh --bring-up --matrix --baseline /tmp/no-baseline
   PROM_SHIM_CLICKHOUSE_TRANSPORT=native \
     ./scripts/run-bench.sh --matrix --baseline /tmp/no-baseline
   ```

2. Run long-range profiles for both transports after seeding data:

   ```bash
   PROM_SHIM_CLICKHOUSE_TRANSPORT=http \
     ./scripts/run-bench.sh --long-range all --matrix --baseline /tmp/no-baseline
   PROM_SHIM_CLICKHOUSE_TRANSPORT=native \
     ./scripts/run-bench.sh --long-range all --matrix --baseline /tmp/no-baseline
   ```

3. Add optional allocation/RSS capture for representative high-row queries:
   - native range outputs;
   - local fallback sample reads;
   - metadata-heavy selectors;
   - histogram queries.
4. Preserve artifacts under `harness/artifacts/transport-http-*` and
   `harness/artifacts/transport-native-*` or equivalent stable names.
5. Update README benchmark notes only after parity is proven.

Validation:

- No compliance regressions.
- No harness diffs.
- Driver transport has equal or better bytes/allocations for high-row paths.
- Any latency regressions are understood and documented before default flip.

Risks / notes:

- Tiny queries may still be dominated by ClickHouse planning/round-trip time.
  The driver is expected to reduce JSON/decode overhead, not eliminate every
  native-vs-Prom gap.

### Phase 6. Flip default and keep rollback

Goal: make the native driver the default promshim ClickHouse transport once
validated.

Tasks:

1. Change default `PROM_SHIM_CLICKHOUSE_TRANSPORT` from `http` to `native`.
2. Keep `http` as an explicitly supported rollback mode.
3. Update README configuration and troubleshooting docs.
4. Update harness/docker-compose defaults to expose/use the native port for
   promshim while keeping remote-write HTTP unchanged.
5. Add release/open-source notes describing the transport change.

Validation:

```bash
go test ./...
./scripts/run-compliance.sh
./scripts/run-harness.sh
./scripts/run-bench.sh --matrix --baseline harness/bench/baseline.json
```

Risks / notes:

- The default flip should be a separate reviewable change from the transport
  implementation. That keeps rollback simple.

## Interaction with CBE

The driver migration should land before behavior-changing CBE. Otherwise CBE
thresholds would be calibrated against an HTTP/JSON transport that we already
expect to retire.

After the driver is default, CBE should estimate costs using the new baseline:

- lower fixed native transport overhead;
- lower bytes in flight for typed/columnar results;
- lower promshim decode allocations;
- per-query ClickHouse settings available through driver contexts.

CBE can then choose among:

1. strict native SQL with small-query driver settings;
2. strict native SQL with long-scan driver settings;
3. local fallback only when bytes/heap/sample caps prove it is safe;
4. whole-query delegation when tier 1 is eligible.

## Definition of done

The transport migration is complete when:

- promshim can run all native SQL, metadata, and delegation query paths through
  the ClickHouse driver or explicitly documented temporary compatibility paths;
- HTTP remains available as a rollback transport;
- compliance and harness pass under the driver transport;
- short and long-range benchmarks have been refreshed for the driver baseline;
- explain output and metrics identify the transport used;
- per-query ClickHouse settings are possible through the storage request model;
- README configuration and troubleshooting docs describe the new default; and
- no CBE thresholds are finalized until the driver baseline is measured.
