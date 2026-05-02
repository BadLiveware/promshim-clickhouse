# Configuration


`cmd/promshim` reads configuration from environment variables:

| Variable | Default | Meaning |
|---|---:|---|
| `PROM_SHIM_LISTEN_ADDR` | `:9090` | HTTP listen address. |
| `PROM_SHIM_LOG_LEVEL` | `info` | Process log level: `debug`, `info`, `warn`, or `error`. `debug` enables one structured request-completion log line per HTTP request with method, path, status, duration, bounded PromQL query, hashed parameters, strategy/routing headers, ClickHouse round trips, and Prometheus error type. |
| `PROM_SHIM_LOG_PROMQL` | `true` | Include the bounded raw PromQL `query` field in debug request logs. Set to `false` to keep only the request parameter hash. |
| `PROM_SHIM_CLICKHOUSE_TRANSPORT` | `native` | ClickHouse transport: `native` for the ClickHouse Go driver or `http` for the legacy JSONEachRow rollback path. |
| `PROM_SHIM_CLICKHOUSE_ENDPOINT` | `http://127.0.0.1:8123/` | ClickHouse HTTP endpoint used by HTTP transport mode and retained as rollback configuration. |
| `PROM_SHIM_CLICKHOUSE_NATIVE_ADDR` | `127.0.0.1:9000` | ClickHouse native TCP address used when `PROM_SHIM_CLICKHOUSE_TRANSPORT=native`. |
| `PROM_SHIM_CLICKHOUSE_DATABASE` | `observability` | ClickHouse database containing the `TimeSeries` table. |
| `PROM_SHIM_CLICKHOUSE_TABLE` | `prometheus` | ClickHouse `TimeSeries` table name. |
| `PROM_SHIM_CLICKHOUSE_USERNAME` | `default` | ClickHouse user. |
| `PROM_SHIM_CLICKHOUSE_PASSWORD` | `otel` | ClickHouse password. |
| `PROM_SHIM_CLICKHOUSE_COMPRESSION` | `off` | Native driver compression: `off`, `lz4`, or `zstd`. |
| `PROM_SHIM_CLICKHOUSE_NATIVE_SECURE` | `false` | Enables TLS for the native ClickHouse driver connection. |
| `PROM_SHIM_CLICKHOUSE_TLS_SERVER_NAME` | empty | Optional TLS server name override for the native driver. |
| `PROM_SHIM_CLICKHOUSE_TLS_INSECURE_SKIP_VERIFY` | `false` | Disables TLS certificate verification for native driver connections; use only for controlled test environments. |
| `PROM_SHIM_CLICKHOUSE_MAX_OPEN_CONNS` | `10` | Native driver maximum open connections. |
| `PROM_SHIM_CLICKHOUSE_MAX_IDLE_CONNS` | `10` | Native driver maximum idle connections. |
| `PROM_SHIM_CLICKHOUSE_CONN_MAX_LIFETIME_SECONDS` | `3600` | Native driver connection maximum lifetime. |
| `PROM_SHIM_REQUEST_TIMEOUT_SECONDS` | `30` | ClickHouse request timeout. The `default_safe` ClickHouse settings profile also sends this as `max_execution_time`. |
| `PROM_SHIM_CLICKHOUSE_VERSION` | `26.3` | Version used by delegation and settings-profile capability classifiers. |
| `PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE` | `default_safe` | Shim-owned per-query ClickHouse settings profile. Supported names: `none`, `default_safe`, `repeated_selective`, `tiny_instant`, `simple_range`, `long_range_scan`, `aggregation_heavy`, `join_heavy`, `subtree_pushdown`, `benchmark_control`. `benchmark_control` is an explicit measurement profile that applies bounded `max_threads=4`; other performance profile names remain gated until measured evidence justifies applied settings. |
| `PROM_SHIM_CLICKHOUSE_MAX_MEMORY_USAGE_BYTES` | `0` | Optional `default_safe` per-query `max_memory_usage` cap; `0` leaves it unset. |
| `PROM_SHIM_CLICKHOUSE_MAX_ROWS_TO_READ` | `0` | Optional `default_safe` per-query `max_rows_to_read` cap; `0` leaves it unset until estimates justify a cap. |
| `PROM_SHIM_CLICKHOUSE_MAX_RESULT_ROWS` | `0` | Optional `default_safe` per-query `max_result_rows` cap; `0` leaves it unset until a result-row contract is explicit. |
| `PROM_SHIM_PROMOTED_TAG_COLUMNS` | empty | Comma-separated Prometheus label names whose ClickHouse `tags_to_columns` column has the same name on the TimeSeries tags table. Native selector SQL uses these columns for label matchers and label projection instead of probing `src.tags[...]`. |
| `PROM_SHIM_DISCOVER_PROMOTED_TAG_COLUMNS` | `false` | When true, promshim queries ClickHouse `system.columns` / `system.tables` at startup and adds identity `tags_to_columns` entries (`'label':'label'`) to the promoted tag column set. Explicit `PROM_SHIM_PROMOTED_TAG_COLUMNS` entries are still honored. Discovery failure is logged and ignored. |
| `PROM_SHIM_NATIVE_GRID_FUNCTIONS` | `prefer` | Native-grid lowering gate. `prefer` lets supported tier-2 range selectors for `rate`, `irate`, `delta`, `idelta`, and `last_over_time` use ClickHouse TimeSeries grid functions; set `off` to roll back to promshim's SQL-level kernels. |
| `PROM_SHIM_CUMULATIVE_AVG_OVER_TIME` | `prefer` | High-overlap `avg_over_time` lowering gate. `prefer` uses cumulative per-series state plus ASOF boundary lookups to avoid dense grid-to-data join fanout; set `off` to roll back to the direct grouped aggregate path. |
| `PROM_SHIM_NATIVE_LOWERING_MODE` | `prefer` | Global lowering mode; see execution modes above. |
| `PROM_SHIM_ROUTING_POLICY` | `strict` | Global cost-routing policy; see cost routing policies above. |
| `PROM_SHIM_ALLOW_REQUEST_ROUTING_OVERRIDES` | `false` | Allows per-request `native_lowering_mode` and `routing_policy` overrides. Keep disabled on shared production endpoints; enable only for trusted benchmark/debug clients. |
| `PROM_SHIM_RECORDING_RULE_MODE` | `off` | Recording-rule compatibility mode. `off` ignores configured rule files. `virtual` expands configured recording-rule metric names just in time for instant-vector, nested, range-selector, and subquery contexts, including `/api/v1/query_range` evaluations. |
| `PROM_SHIM_RECORDING_RULE_FILES` | empty | Comma-separated rendered Prometheus rule YAML files or glob patterns to load when `PROM_SHIM_RECORDING_RULE_MODE=virtual`. Alerting rules are ignored; conflicting recording-rule definitions are rejected at query time. |
| `PROM_SHIM_RECORDING_RULE_RELOAD_INTERVAL_SECONDS` | `30` | Recording-rule file reload interval. In `virtual` mode, promshim re-globs and parses configured files before query planning after this interval has elapsed, atomically swaps in valid registries, and keeps serving the previous registry if reload fails. |
| `PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES` | empty | Comma-separated family gates eligible for `cost_prefer` local overrides, e.g. `selector_instant,rate_instant`. |
| `PROM_SHIM_DISABLE_OPTIMIZED_IR` | unset / false | Rollback/differential-testing gate that disables logical IR rewrite passes while preserving baseline planning. |
| `PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION` | unset / false | Rollback/differential-testing gate that disables native `by(...)` aggregation child label projection and restores full selector tag materialization. |
| `PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE` | unset / false | Rollback/differential-testing gate that disables native SQL reuse for identical vector subexpressions. |
| `PROM_SHIM_DISABLE_LOCAL_REPEATED_EXPRESSION_CACHE` | unset / false | Rollback/differential-testing gate that disables request-local caching for repeated local range-function subexpressions. |
| `PROM_SHIM_MAX_RANGE_POINTS_PER_SERIES` | `50000` | Reject range queries above this point count per series. |
| `PROM_SHIM_RANGE_CHUNK_POINTS_PER_SERIES` | `5000` | Chunk eligible local range plans above this point count per series. |
| `PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES` | `289` | Explicit point cap for eligible native range plans. With the default, chunking is shape-aware: high-overlap cumulative windows use the memory guardrail, while low-memory native-grid sums only use the duration cap. Set `0` to disable native range auto-chunking. Explain output includes `nativeRangeChunking`; `X-Promshim-Strategy` reports `chunked_native` when chunking is selected. |
| `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_SECONDS` | `86400` | Further cap eligible native range chunks by output duration. The default `86400` keeps chunks at about one day for coarse-step long-range queries where point caps alone would still scan multiple days per chunk. Set `0` to disable the duration cap. |
| `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_CHUNKS` | `12` | Guardrail that enlarges native range chunks when the point/duration caps would create too many ClickHouse subqueries. Set `0` for no max-chunk guardrail. |
| `PROM_SHIM_NATIVE_RANGE_PREFLIGHT_SERIES_THRESHOLD` | `1000` | For uncertain high-overlap native range shapes, run a capped metadata-only matched-series probe with `LIMIT threshold+1`. Results above the threshold, probe timeout, or probe error keep safe chunking; results at or below the threshold can skip chunking. Set `0` to disable preflight and keep the safe static chunking decision. |
| `PROM_SHIM_NATIVE_RANGE_PREFLIGHT_TIMEOUT_MS` | `50` | Context and ClickHouse `max_execution_time` budget for the bounded native range preflight probe. Set `0` for no explicit preflight timeout. |
| `PROM_SHIM_NATIVE_RANGE_PREFLIGHT_MAX_MEMORY_BYTES` | `67108864` | ClickHouse `max_memory_usage` for the bounded native range preflight probe. Set `0` for no explicit preflight memory setting. |
| `PROM_SHIM_MAX_RESPONSE_SERIES` | `5000` | Reject query responses and `/series` metadata responses with more series than this limit. Query endpoints reject before execution when fresh estimates prove the cap would be exceeded, and still enforce the exact cap after evaluation. Metadata queries push a `limit+1` cap into ClickHouse and return an error when the extra sentinel row is present. |
| `PROM_SHIM_MAX_RESPONSE_POINTS` | `500000` | Reject query responses with more total points than this limit. Query endpoints reject before execution when fresh estimates prove the cap would be exceeded, and still enforce the exact cap after evaluation. |
| `PROM_SHIM_MAX_METADATA_ITEMS` | `50000` | Reject `/labels` and label-values metadata responses with more items than this limit. |

### PrometheusRule CRD syncer

`cmd/promshim-rule-syncer` is a separate Kubernetes-aware helper for clusters
that store dashboard/alert compatibility rules as Prometheus Operator
`PrometheusRule` CRDs. It reads matching CRDs, validates/renders Prometheus rule
YAML, and writes files for promshim to load. Promshim itself remains
Kubernetes-unaware; enable virtual rule expansion with
`PROM_SHIM_RECORDING_RULE_MODE=virtual` and point
`PROM_SHIM_RECORDING_RULE_FILES` at the rendered files.

The syncer is intentionally sidecar-oriented:
`PrometheusRule CRDs -> shared volume files`. Run it in the same pod as
promshim, mount a shared `emptyDir`, write rule files there, and configure
promshim with a glob such as
`PROM_SHIM_RECORDING_RULE_FILES=/etc/promshim/rules/*.yaml`. File writes use a
temporary file plus atomic rename, generated files are prefixed with
`promshim-`, and stale generated `.yaml` files are removed from the output
directory.

Common flags and matching environment variables:

| Flag | Env | Meaning |
|---|---|---|
| `--output-dir` | `PROM_SHIM_RULE_SYNC_OUTPUT_DIR` | Directory for rendered rule files. Default: `/etc/promshim/rules`. |
| `--namespaces` | `PROM_SHIM_RULE_SYNC_NAMESPACES` | Comma-separated PrometheusRule namespaces. Empty means all namespaces. |
| `--rule-selector` | `PROM_SHIM_RULE_SYNC_SELECTOR` | Kubernetes label selector for PrometheusRule objects, e.g. `release=k8s-monitoring`. |
| `--prometheus-version` | `PROM_SHIM_RULE_SYNC_PROMETHEUS_VERSION` | Prometheus compatibility version for rule validation. Default: `3.0.0`. |
| `--sync-interval` | `PROM_SHIM_RULE_SYNC_INTERVAL` | Periodic sync interval. Default: `30s`. |
| `--once` | `PROM_SHIM_RULE_SYNC_ONCE` | Run one sync and exit. |

Mount the same writable directory into promshim and the syncer, and point
`PROM_SHIM_RECORDING_RULE_FILES` at the rendered `*.yaml` files. Deployment
semantics and Kubernetes examples live in [`recording-rules.md`](recording-rules.md).

Per-request knobs:

- `native_lowering_mode=off|prefer|explain|shadow|force_supported|local_pushdown` when `PROM_SHIM_ALLOW_REQUEST_ROUTING_OVERRIDES=true`
- `routing_policy=strict|cost_shadow|cost_prefer` when `PROM_SHIM_ALLOW_REQUEST_ROUTING_OVERRIDES=true`
- `explain=1` or `explain=true`
- `X-Promshim-Log-Comment: ...` to forward a ClickHouse `log_comment` for query
  log/profile correlation. When omitted, promshim generates a bounded comment
  containing endpoint, normalized mode/policy, and a hash of request parameters;
  raw PromQL and label values are not included.

Successful query responses include `X-Promshim-CH-Transport: http|native` so
transport rollout can be correlated with strategy, round-trip, and duration
headers. They also include `X-Promshim-Settings-Profile` when a settings profile
was resolved. Explain responses include the same transport value as
`clickHouseTransport` and settings provenance as `clickHouseSettingsProfile` and
`plan.settingsProfile`.
