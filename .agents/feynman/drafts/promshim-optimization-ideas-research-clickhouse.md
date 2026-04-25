# ClickHouse research notes for promshim optimization ideas

Task status: **done**. These are candidate ideas, not authoritative recommendations.

## Evidence table

| # | Source | URL | Key claim | Type | Confidence |
|---|--------|-----|-----------|------|------------|
| 1 | ClickHouse Docs — EXPLAIN Statement | https://clickhouse.com/docs/en/sql-reference/statements/explain | `EXPLAIN` supports plan/pipeline/estimate modes; `EXPLAIN PLAN indexes=1` can show index type, keys, conditions, filtered parts/granules/ranges; `projections=1` can show analyzed projections and selected rows/marks/ranges. | primary | high |
| 2 | ClickHouse Docs — system.query_log | https://clickhouse.com/docs/operations/system-tables/query_log | `system.query_log` records query duration, read rows/bytes, memory, query text, `normalized_query_hash`, `ProfileEvents`, changed `Settings`, `log_comment`, `query_id`, projection usage, and query-cache usage; logs flush periodically and can be forced with `SYSTEM FLUSH LOGS`. | primary | high |
| 3 | ClickHouse Docs — system.events | https://clickhouse.com/docs/operations/system-tables/events | `system.events` contains server-since-start counters and event descriptions, including query-cache/condition-cache events, primary/secondary index analysis, selected marks/parts/ranges, PREWHERE row counters, and read/CPU/cache counters. | primary | high |
| 4 | ClickHouse Docs — PREWHERE optimization | https://clickhouse.com/docs/en/optimize/prewhere | PREWHERE reduces I/O by filtering columns before reading non-filter columns; `optimize_move_to_prewhere` is enabled by default; effect can be measured by comparing bytes read and by `EXPLAIN`/logs. | primary | high |
| 5 | ClickHouse Docs — Query condition cache | https://clickhouse.com/docs/operations/query-condition-cache | The query condition cache stores per-filter/per-granule skip bits for repeated selective filters on mostly immutable data; it is controlled by `use_query_condition_cache`, requires `enable_analyzer`, is not retained across restarts, and exposes hit/miss counters. | primary | high |
| 6 | ClickHouse Docs — Query cache | https://clickhouse.com/docs/en/operations/query-cache | The result query cache serves repeated `SELECT` results but is transactionally inconsistent, has TTL/default freshness caveats, is controlled by `use_query_cache` and read/write settings, and reports usage via `system.query_log`/events/metrics. | primary | high |
| 7 | ClickHouse Docs — Data skipping indexes | https://clickhouse.com/docs/en/guides/improving-query-performance/skipping-indexes | Skip indexes allow ClickHouse to skip chunks guaranteed not to match; they can help when enough granules are skipped, but are workload/data-shape dependent and can add ingest/query cost. | primary | high |
| 8 | ClickHouse Docs — Sparse primary indexes | https://clickhouse.com/docs/en/optimize/sparse-primary-indexes | MergeTree sparse primary indexes store one mark per granule; leading-key filters can use binary search, while filtering only later key columns may use less effective generic exclusion search depending on cardinality/order. | primary | high |
| 9 | ClickHouse Docs — Projections | https://clickhouse.com/docs/en/sql-reference/statements/alter/projection | Projections store hidden optimized layouts or pre-aggregations; ClickHouse can select the projection that scans least data; projections add disk/write overhead and have mutation/merge behavior settings. | primary | high |
| 10 | ClickHouse Docs — ClickStack performance tuning | https://clickhouse.com/docs/en/use-cases/observability/clickstack/performance_tuning | Official observability guidance prioritizes materializing frequently queried attributes, adding selective skip indexes, cautious primary-key changes, materialized views, and projections; metrics schemas are described as opinionated for Prometheus-style workloads. | primary | high |
| 11 | ClickHouse Docs — Query-level session settings | https://clickhouse.com/docs/en/operations/settings/query-level | ClickHouse settings can be applied at query level via HTTP CGI parameters or `SETTINGS` clauses and are reset after the query. | primary | high |
| 12 | ClickHouse Docs — system.settings | https://clickhouse.com/docs/operations/system-tables/settings | `system.settings` exposes current session settings with value/default/changed/readonly/type metadata and can be filtered with `WHERE changed`. | primary | high |
| 13 | ClickHouse Docs — HTTP interface | https://clickhouse.com/docs/en/interfaces/http | HTTP queries can pass per-query settings/profile parameters, `query_id`, and receive `X-ClickHouse-Summary`; HTTP sessions require `session_id`; HTTP 200 does not always guarantee query success during streaming. | primary | high |
| 14 | Local project — promshim settings profiles | file://internal/promshim/storage/settings_profile.go | promshim already has a query-setting allowlist and settings profiles; it applies required time-series/JSON-denormal settings, safety timeout/read-only/cancel settings, optional resource caps, and currently skips query-condition/result-cache settings unless evidence gates are met. | local primary | high |
| 15 | Local project — ch-explain helper | file://scripts/ch-explain.sh | The repo already has a script that issues PromQL through promshim, captures lowered SQL via `system.query_log`, and writes EXPLAIN variants plus query-log JSONL for each captured SQL. | local primary | high |
| 16 | Local project — ch-profile helper | file://scripts/ch-profile-capture.sh | The repo already has a script that runs benchmarks and aggregates `system.query_log` ProfileEvents, duration, read rows/bytes, result rows, and memory into JSON. | local primary | high |
| 17 | Local project — harness ClickHouse schema | file://harness/clickhouse/init.sql | The harness creates `observability.prometheus ENGINE = TimeSeries` plus `metrics.samples`/`metrics.time_series` MergeTree tables ordered by `(metric_name, fingerprint, unix_milli)`. | local primary | high |
| 18 | ClickHouse Blog — Index-based pruning in ClickHouse | https://clickhouse.com/blog/index-based-pruning | ClickHouse explains primary-key pruning, lightweight projections, and skip-index pruning together, with `EXPLAIN indexes=1, projections=1` examples for marks/granules and projection analysis. | vendor technical blog | medium-high |

## Findings and candidate ideas

### 1. Make EXPLAIN + query_log evidence a first-class optimization loop input

ClickHouse exposes both compile-time and run-time proof surfaces: `EXPLAIN PLAN indexes=1` can show index type, keys, conditions, and parts/granules/ranges, while `EXPLAIN PLAN projections=1` can show analyzed projections and selected rows/marks/ranges [1]. `system.query_log` then gives the executed query’s duration, read rows/bytes, memory, changed settings, `ProfileEvents`, `log_comment`, projection names, query-cache usage, and normalized hash [2]. The repo already has helpers that capture lowered SQL and EXPLAIN variants after a PromQL request, and a separate ProfileEvents aggregation script [15], [16].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Benchmark/proof harness and optimization report generator. |
| Expected proof signal | Per-query before/after diff of `EXPLAIN` selected parts/granules/ranges, `system.query_log.read_rows/read_bytes/memory_usage/query_duration_ms`, and key `ProfileEvents` such as `SelectedMarks`, `SelectedRows`, `RowsReadByPrewhereReaders`, `RowsReadByMainReader`, and index-analysis counters [2], [3]. |
| Correctness/freshness/operational risks | Low semantic risk because this is observational; operational risk is noise from caches, background merges, and query-log flush timing. `SYSTEM FLUSH LOGS` is available for deterministic capture [2]. |
| First experiment shape | Extend `scripts/ch-explain.sh` to always emit JSON-formatted `EXPLAIN PLAN indexes=1, projections=1` where supported, then join those files with `scripts/ch-profile-capture.sh` output by `log_comment`/normalized query [1], [2], [15], [16]. |

### 2. Add a “ClickHouse proof signature” to benchmark rows

`system.query_log` contains `normalized_query_hash`, `ProfileEvents`, changed `Settings`, `log_comment`, `query_id`, projections used, and query-cache usage [2]. `system.events` documents counters for selected marks/parts/ranges, primary/secondary index analysis, query-condition-cache hits/misses, query-cache hits/misses, and PREWHERE row counts [3]. promshim already sets/propagates `log_comment` paths and uses bounded setting profiles [14], [15].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Benchmark artifact schema and cost-routing calibration. |
| Expected proof signal | Each benchmark sample can include a compact proof signature: normalized SQL hash, settings profile, `read_rows/read_bytes`, `memory_usage`, selected marks/ranges from ProfileEvents, projection names, and query-cache usage [2], [3]. |
| Correctness/freshness/operational risks | Low semantic risk; privacy risk if raw SQL or labels leak into artifacts. Prefer hashes plus bounded exemplars, and keep `log_comment` bounded as current scripts do [2], [15]. |
| First experiment shape | Add a post-bench aggregation that emits one row per PromQL/candidate/profile with selected query-log columns and a filtered ProfileEvents map. Compare whether faster candidates also reduce `read_rows`, `read_bytes`, or selected marks; flag “latency-only/no-pruning-proof” cases for manual review [2], [3], [16]. |

### 3. PREWHERE audit: detect when native lowering preserves or blocks I/O reduction

PREWHERE reduces I/O by reading/filtering only needed columns before loading non-filter columns, and ClickHouse automatically moves filters from `WHERE` to `PREWHERE` when `optimize_move_to_prewhere` is enabled, which is the default [4]. The ClickHouse PREWHERE guide says impact can be measured by comparing bytes read with `optimize_move_to_prewhere` disabled/enabled and by inspecting `EXPLAIN`/logs [4].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | SQL lowering audit; not a default query-setting change unless evidence shows a regression. |
| Expected proof signal | For wide or label-heavy native-lowered SQL, `read_bytes` drops with PREWHERE enabled while `read_rows` may stay similar; `EXPLAIN PLAN actions=1` shows Prewhere info; ProfileEvents can include `RowsReadByPrewhereReaders` and `RowsReadByMainReader` [3], [4]. |
| Correctness/freshness/operational risks | Forcing manual `PREWHERE` is risky if query semantics involve `FINAL`/engine-specific behavior; safer first step is detection and proof capture, because ClickHouse already auto-moves conditions by default [4]. |
| First experiment shape | Select high-read native-lowered queries, run as-is and with `SETTINGS optimize_move_to_prewhere=false`, compare `read_bytes`, `RowsReadByPrewhereReaders`, `RowsReadByMainReader`, and EXPLAIN Prewhere info. If auto-PREWHERE is absent for a shape that should benefit, inspect SQL shape rather than injecting manual PREWHERE first [3], [4]. |

### 4. Query-condition-cache profile for repeated selective PromQL shapes

ClickHouse’s query condition cache stores a bit per filter/granule indicating whether any row can satisfy a filter; later executions can skip granules for the same/repeated selective conditions [5]. The docs list prerequisites: repeated filter conditions, mostly immutable data, and selective filters; the cache requires `enable_analyzer`, is not retained across restarts, and is controlled by `use_query_condition_cache` [5]. promshim already has a `repeated_selective` profile name and deliberately skips `use_query_condition_cache` with “requires_measured_evidence” provenance [14].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Query/session settings profile, gated by query family and benchmark evidence. |
| Expected proof signal | Warm runs show lower `read_rows/read_bytes/query_duration_ms` and non-zero `QueryConditionCacheHits` with `use_query_condition_cache=1`; cold/control runs show misses or no benefit [3], [5]. |
| Correctness/freshness/operational risks | Correctness risk appears lower than result caching because it caches filter granule eligibility rather than final results, but it is stateful, version-dependent, analyzer-dependent, and restart-volatile [5]. It may hide real pruning differences during optimization sweeps unless explicitly controlled [5]. |
| First experiment shape | Add an experimental profile that enables `use_query_condition_cache=1` only for a repeated-selective benchmark axis. Run cold/warm pairs with cache disabled/enabled; record `QueryConditionCacheHits/Misses`, selected marks, `read_rows/read_bytes`, and latency. Keep it off in default-safe until warm-run evidence is stable [3], [5], [14]. |

### 5. Keep result query cache out of default PromQL paths; maybe use only as an explicit freshness experiment

ClickHouse’s query result cache can serve repeated `SELECT` results directly, but the docs explicitly frame it as transactionally inconsistent and TTL-based [6]. The docs also recommend query-specific use for maximum control and warn that enabling it at user/profile level can make all `SELECT` queries return cached results [6]. promshim already records a skipped `use_query_cache` provenance reason, `freshness_sensitive_not_default` [14].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Settings-profile policy and benchmark control, not default serving. |
| Expected proof signal | If explicitly tested, `system.query_log.query_cache_usage` becomes `Read`/`Write`, and `system.events` shows `QueryCacheHits/Misses`; but latency improvements would not prove SQL/pruning improvements [2], [3], [6]. |
| Correctness/freshness/operational risks | High freshness risk for PromQL-style current-window queries because ClickHouse documents the result cache as transactionally inconsistent and TTL-expiring [6]. It can also contaminate optimization measurements by hiding work [6], [14]. |
| First experiment shape | Do not enable by default. If needed, add a separate benchmark axis for dashboard replay with pinned historical time ranges and explicit TTL/tag settings, and report it as a UX cache experiment rather than a SQL optimization [2], [3], [6], [14]. |

### 6. Schema/layout advisor: primary-key and projection evidence for Prometheus-like access patterns

MergeTree sparse primary indexes use marks/granules and work best when filters align with leading key columns; filtering only a later compound-key column can fall back to generic exclusion search and read many more granules depending on cardinality [8]. The harness MergeTree tables order samples and time-series metadata by `(metric_name, fingerprint, unix_milli)` [17]. Projections can add hidden layouts with different orderings, and ClickHouse can automatically choose a projection that scans less data, but projections add disk and write overhead [9]. ClickHouse’s observability tuning guide treats primary-key changes and projections as advanced/operator-owned actions after simpler optimizations [10].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Operator guidance/schema advisor plus harness experiment; not a query rewrite. |
| Expected proof signal | `EXPLAIN indexes=1` changes from many selected granules/generic exclusion search to fewer selected granules/binary search for targeted query families; `system.query_log.projections` identifies projection usage; `read_rows/read_bytes` decline [1], [2], [8], [9]. |
| Correctness/freshness/operational risks | Physical layout changes can improve one workload and hurt another; projections duplicate data or add write I/O; materializing projections on existing data is a mutation and can consume resources [9], [10]. |
| First experiment shape | Clone a benchmark schema/table with alternative orderings such as time-leading or fingerprint-leading variants, and/or add a projection for the dominant non-leading access pattern. Run the same PromQL corpus and compare EXPLAIN selected granules, query-log read rows/bytes, and ingest/write overhead. Treat findings as deployment guidance unless promshim controls the benchmark schema [1], [2], [8], [9], [10], [17]. |

### 7. Skip-index advisor for selective label/fingerprint/time-series filters

ClickHouse skip indexes can skip data chunks guaranteed not to match, but the docs emphasize that they are not traditional row secondary indexes and only pay off when enough granules are skipped to offset evaluation cost [7]. The docs list minmax/set/Bloom-filter-style options and recommend testing on real data; `use_skip_indexes` can disable skip indexes for a control run [7]. Official observability guidance similarly recommends validating skip indexes with `EXPLAIN indexes=1` and benchmarks, and notes Bloom filters are most useful for high-cardinality sparse strings while minmax is lightweight for numeric range filters [10].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Operator guidance/schema advisor and benchmark axis. Query/session layer can set `use_skip_indexes=0` only for control comparisons, because it is enabled by default [7]. |
| Expected proof signal | `EXPLAIN indexes=1` includes a `Skip` section showing fewer granules after the skip index; `system.query_log.read_rows/read_bytes` and ProfileEvents such as `FilteringMarksWithSecondaryKeysMicroseconds`/`SelectedMarks` change in the expected direction [1], [3], [7]. |
| Correctness/freshness/operational risks | Bad skip indexes add insert/index-evaluation cost and can fail to prune if values are uncorrelated with primary-key order or too common per granule [7], [10]. Bloom-filter false positives can reduce pruning but should not skip valid matching data according to the skip-index model [7]. |
| First experiment shape | On a cloned/harness table, add candidate minmax indexes for numeric/time buckets or Bloom/set indexes for selective high-cardinality identifiers, materialize only a bounded partition, then run corpus queries with `use_skip_indexes=1` and `0` to isolate net benefit [7], [10], [17]. |

### 8. Query-setting visibility and safe promshim-owned settings

ClickHouse supports query-level settings via HTTP CGI parameters and `SETTINGS` clauses, and query-level values reset after the query [11]. `system.settings` exposes current setting values and whether they were changed/readonly, and `system.query_log.Settings` records changed settings when setting-change logging is enabled [2], [12]. promshim already allowlists a small set of settings and applies query-scoped safety settings such as `max_execution_time`, `timeout_overflow_mode='throw'`, `cancel_http_readonly_queries_on_client_close`, `readonly=2`, and optional memory/read/result caps [14].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Existing settings-profile resolver and `/api/v1/status`/explain endpoints. |
| Expected proof signal | For every benchmark query, `system.query_log.Settings` contains the expected changed settings when `log_query_settings` is enabled; `system.settings WHERE changed` can verify session-level leakage is absent; artifacts include applied/skipped setting provenance [2], [12], [14]. |
| Correctness/freshness/operational risks | Mis-scoped settings can leak across sessions if implemented as session settings rather than query settings; overly aggressive caps can throw errors; some settings are version/readonly constrained [11], [12], [14]. |
| First experiment shape | Add a validation test that issues two queries through HTTP/native paths: one with a profile override and one without. Assert via `query_log.Settings` and/or `getSetting()` that the setting is query-scoped, not sticky, and in the allowlist [2], [11], [12], [13], [14]. |

### 9. HTTP/native transport observability: query_id and summary are useful proof hooks

The ClickHouse HTTP interface supports per-query settings/profile parameters, `query_id`, and response headers such as `X-ClickHouse-Summary`; it also warns that HTTP 200 can precede a later streaming error body [13]. `system.query_log` records `query_id`, `log_comment`, interface type, query text, duration, and exceptions [2].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | ClickHouse transport abstraction and error/telemetry wrapper. |
| Expected proof signal | promshim can attach a bounded `query_id`/`log_comment` to every ClickHouse call, return it in debug headers, and correlate client-observed failures with `system.query_log` exception/query rows [2], [13]. |
| Correctness/freshness/operational risks | Do not expose sensitive query text or labels through public headers; HTTP streaming errors require parsing body/exception markers rather than trusting status 200 alone [13]. |
| First experiment shape | Add an opt-in debug mode that returns a redacted `X-Promshim-ClickHouse-Query-Id` and proof artifact path; verify query-log correlation across HTTP and native transports [2], [13]. |

### 10. Observability-specific tuning should stay mostly operator-owned

ClickStack’s official observability tuning order starts with materialized attributes, skip indexes, then primary-key changes, materialized views, and projections; it explicitly says logs/traces usually benefit most from tuning while metrics schemas are opinionated for Prometheus-style workloads and usually do not require modification for standard charting [10]. promshim’s harness has both a ClickHouse TimeSeries table and MergeTree mirror tables, so schema experiments are available locally without implying production defaults [17].

Candidate idea:

| Field | Note |
|---|---|
| Possible promshim layer | Documentation, benchmark harness, and optional migration/advisor output. |
| Expected proof signal | Operator guidance is backed by corpus-specific before/after evidence: EXPLAIN pruning, query-log read bytes/rows, ProfileEvents, and correctness/compliance comparisons [1], [2], [3], [10], [17]. |
| Correctness/freshness/operational risks | Schema/layout recommendations can be deployment-specific, consume disk/write I/O, and may not apply to ClickHouse TimeSeries engine internals; keep them as candidate experiments unless promshim owns the schema [9], [10], [17]. |
| First experiment shape | Build an “advisor report” over benchmark artifacts that says: top N slow/high-read query classes, whether primary/pruning evidence exists, and which operator-owned experiment to try next: materialized column, skip index, projection, or key change [1], [2], [3], [7], [8], [9], [10]. |

## Safe-vs-operator-owned setting/layout split

- **Likely promshim-owned/query-scoped:** existing time-series enablement, JSON denormal quoting, read-only query scope, timeout/overflow mode, cancel-on-client-close, bounded resource caps, and experimental `use_query_condition_cache` only behind a measured profile gate [11], [14].
- **Likely operator-owned/deployment guidance:** primary key/order changes, projections, skip-index DDL/materialization, materialized columns/views, server cache sizes, and query-log server configuration [2], [7], [8], [9], [10].
- **Not default-safe for PromQL freshness:** result query cache, because ClickHouse documents it as transactionally inconsistent and TTL-based [6], [14].

## Coverage Status

- **Checked directly:** Official ClickHouse docs for EXPLAIN, `system.query_log`, `system.events`, PREWHERE, query condition cache, query cache, skipping indexes, sparse primary indexes, projections, query-level settings, `system.settings`, HTTP interface, and ClickStack performance tuning [1]-[13].
- **Checked local project files:** promshim setting profiles, existing EXPLAIN/ProfileEvents helper scripts, and local ClickHouse harness schema [14]-[17].
- **Checked broader landscape:** Web search found vendor docs/blogs and ClickHouse observability docs; `alpha search` was run for ClickHouse/pruning/cache academic angles but did not surface a directly ClickHouse-specific paper that I could fetch and summarize. The ClickHouse condition-cache docs cite a DOI for predicate caching, but the ACM landing page was bot-blocked in this environment, so I did not summarize that paper’s contents [5].
- **Uncertain / needs follow-up:** Exact ClickHouse version gates for each setting beyond the docs/local version checks, TimeSeries-engine-specific internal pruning behavior, and production impact of lightweight projections for Prometheus-style metrics should be validated on the target ClickHouse version and production-like data [1], [5], [9], [10], [14], [17].

## Sources

1. ClickHouse Docs — EXPLAIN Statement — https://clickhouse.com/docs/en/sql-reference/statements/explain
2. ClickHouse Docs — system.query_log — https://clickhouse.com/docs/operations/system-tables/query_log
3. ClickHouse Docs — system.events — https://clickhouse.com/docs/operations/system-tables/events
4. ClickHouse Docs — How does the PREWHERE optimization work? — https://clickhouse.com/docs/en/optimize/prewhere
5. ClickHouse Docs — Query condition cache — https://clickhouse.com/docs/operations/query-condition-cache
6. ClickHouse Docs — Query cache — https://clickhouse.com/docs/en/operations/query-cache
7. ClickHouse Docs — Understanding ClickHouse data skipping indexes — https://clickhouse.com/docs/en/guides/improving-query-performance/skipping-indexes
8. ClickHouse Docs — A practical introduction to primary indexes in ClickHouse — https://clickhouse.com/docs/en/optimize/sparse-primary-indexes
9. ClickHouse Docs — Projections — https://clickhouse.com/docs/en/sql-reference/statements/alter/projection
10. ClickHouse Docs — ClickStack performance tuning — https://clickhouse.com/docs/en/use-cases/observability/clickstack/performance_tuning
11. ClickHouse Docs — Query-level Session Settings — https://clickhouse.com/docs/en/operations/settings/query-level
12. ClickHouse Docs — system.settings — https://clickhouse.com/docs/operations/system-tables/settings
13. ClickHouse Docs — HTTP interface — https://clickhouse.com/docs/en/interfaces/http
14. Local project — internal/promshim/storage/settings_profile.go — file://internal/promshim/storage/settings_profile.go
15. Local project — scripts/ch-explain.sh — file://scripts/ch-explain.sh
16. Local project — scripts/ch-profile-capture.sh — file://scripts/ch-profile-capture.sh
17. Local project — harness/clickhouse/init.sql — file://harness/clickhouse/init.sql
18. ClickHouse Blog — Index-based pruning in ClickHouse — https://clickhouse.com/blog/index-based-pruning
