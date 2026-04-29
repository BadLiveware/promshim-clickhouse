# Configuration


`cmd/promshim` reads configuration from environment variables:

| Variable | Default | Meaning |
|---|---:|---|
| `PROM_SHIM_LISTEN_ADDR` | `:9090` | HTTP listen address. |
| `PROM_SHIM_CLICKHOUSE_TRANSPORT` | `native` | ClickHouse transport: `native` for the ClickHouse Go driver or `http` for the legacy JSONEachRow rollback path. |
| `PROM_SHIM_CLICKHOUSE_ENDPOINT` | `http://127.0.0.1:8123/` | ClickHouse HTTP endpoint used by HTTP transport mode and retained as rollback configuration. |
| `PROM_SHIM_CLICKHOUSE_NATIVE_ADDR` | `127.0.0.1:9000` | ClickHouse native TCP address used when `PROM_SHIM_CLICKHOUSE_TRANSPORT=native`. |
| `PROM_SHIM_CLICKHOUSE_DATABASE` | `observability` | ClickHouse database containing the `TimeSeries` table. |
| `PROM_SHIM_CLICKHOUSE_TABLE` | `prometheus` | ClickHouse `TimeSeries` table name. |
| `PROM_SHIM_CLICKHOUSE_USERNAME` | `default` | ClickHouse user. |
| `PROM_SHIM_CLICKHOUSE_PASSWORD` | `otel` | ClickHouse password. |
| `PROM_SHIM_CLICKHOUSE_COMPRESSION` | `off` | Native driver compression: `off`, `lz4`, or `zstd`. |
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
| `PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES` | empty | Comma-separated family gates eligible for `cost_prefer` local overrides, e.g. `selector_instant,rate_instant`. |
| `PROM_SHIM_DISABLE_OPTIMIZED_IR` | unset / false | Rollback/differential-testing gate that disables logical IR rewrite passes while preserving baseline planning. |
| `PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION` | unset / false | Rollback/differential-testing gate that disables native `by(...)` aggregation child label projection and restores full selector tag materialization. |
| `PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE` | unset / false | Rollback/differential-testing gate that disables native SQL reuse for identical vector subexpressions. |
| `PROM_SHIM_DISABLE_LOCAL_REPEATED_EXPRESSION_CACHE` | unset / false | Rollback/differential-testing gate that disables request-local caching for repeated local range-function subexpressions. |
| `PROM_SHIM_MAX_RANGE_POINTS_PER_SERIES` | `50000` | Reject range queries above this point count per series. |
| `PROM_SHIM_RANGE_CHUNK_POINTS_PER_SERIES` | `5000` | Chunk eligible local range plans above this point count per series. |
| `PROM_SHIM_NATIVE_RANGE_CHUNK_POINTS_PER_SERIES` | `289` | Auto-chunk eligible native-grid range aggregation plans above this point count per series to cap ClickHouse peak memory. Set `0` to disable native range auto-chunking. Explain output and `X-Promshim-Strategy` report `chunked_native` when this path is selected. |
| `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_SECONDS` | `86400` | Further cap eligible native range chunks by output duration. The default `86400` keeps chunks at about one day for coarse-step long-range queries where the point cap alone would still scan multiple days per chunk. Set `0` to disable the duration cap. |
| `PROM_SHIM_NATIVE_RANGE_CHUNK_MAX_CHUNKS` | `12` | Guardrail that enlarges native range chunks when the point/duration caps would create too many ClickHouse subqueries. Set `0` for no max-chunk guardrail. |
| `PROM_SHIM_MAX_RESPONSE_SERIES` | `5000` | Reject query responses and `/series` metadata responses with more series than this limit. |
| `PROM_SHIM_MAX_RESPONSE_POINTS` | `500000` | Reject query responses with more total points than this limit. |
| `PROM_SHIM_MAX_METADATA_ITEMS` | `50000` | Reject `/labels` and label-values metadata responses with more items than this limit. |

Per-request knobs:

- `native_lowering_mode=off|prefer|explain|shadow|force_supported|local_pushdown`
- `routing_policy=strict|cost_shadow|cost_prefer`
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
