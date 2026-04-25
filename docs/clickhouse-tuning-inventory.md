# ClickHouse tuning inventory for promshim

This inventory separates ClickHouse tuning surfaces that promshim may own from
operator/deployment choices. It records evidence requirements for measured
changes and separates safety defaults from opt-in benchmark controls.

## Classification rules

| Category | Meaning | Who owns it |
|---|---|---|
| Server/operator recommendation | Cluster, table, storage, or profile setting that should be documented as an advisory reference profile. | Operator / deployment docs. |
| User/profile setting | ClickHouse user profile or role-level setting that may be recommended but is not changed per statement by promshim. | Operator, with promshim docs naming assumptions. |
| Shim-owned session/query setting | Setting promshim may apply to its own ClickHouse sessions/statements after allowlisting, version checks, explain output, and rollback. | promshim. |
| SQL-shape alternative | A renderer/renderer choice, not a ClickHouse setting. | promshim optimizer, with EXPLAIN/ProfileEvents evidence. |
| Unsafe or out of scope | Setting can alter correctness, freshness, isolation, resource safety, or tenant expectations. | Do not set from promshim. |
| Version-dependent/experimental | Setting exists only in certain ClickHouse versions or behind experimental analyzers/features. | Only after version detection and safe fallback. |
| Distributed-only | Relevant only for distributed ClickHouse deployments. | Reference profile or future distributed validation, not default local shim behavior. |

Promshim-owned settings must be:

- scoped to promshim's ClickHouse request/session;
- allowlisted with exact names and value ranges;
- version-aware with unsupported-version fallback to strict/reference behavior;
- visible in explain output and query-log correlation;
- disabled by configuration/profile gates; and
- backed by named sweep/ProfileEvents/EXPLAIN evidence before default serving.

## Inventory

| Surface | Category | Purpose | Default / scope | Expected families | Risk | Validation signal | May promshim set it? |
|---|---|---|---|---|---|---|---|
| `log_queries`, `log_profile_events`, `log_comment`, query ID | Shim-owned session/query setting / request metadata | Correlate promshim requests with `system.query_log` and ProfileEvents. | Per statement/session; already part of measurement discipline. | All benchmark/explain runs. | High-cardinality comments or raw query leakage. | Memory summaries have no missing log comments; query IDs map to benchmark rows. | Yes, with bounded comments and no raw tenant labels as metric labels. |
| `max_execution_time`, `timeout_overflow_mode` | Shim-owned safety setting | Bound expensive ClickHouse statements. | Per query/session. | All ClickHouse candidates. | Too aggressive values can cause false failures; too lax values hide cliffs. | Timeout errors are explainable; strict/reference fallback where safe. | Yes after profile design. |
| `max_memory_usage`, related memory caps | Shim-owned safety setting or user/profile setting | Bound query memory. | Session/profile. | Dense, histogram, vector-match, aggregation families. | OOM or spill behavior differences; version/default differences. | Query errors classified; `MemoryTrackerUsage` tracked. | Candidate for shim-owned safety profile after version checks. |
| `max_threads` | Shim-owned profile knob or user/profile setting | Reduce variance, limit CPU, or tune latency/throughput. | Session/query. | Focused measurement, possibly selected heavy families. | Can improve one shape and harm another; affects p50 comparability. | Before/after sweep and CPU ProfileEvents by family. | Applied only by explicit `benchmark_control` runs; not automatic serving. |
| `use_query_condition_cache` | Version-dependent shim/profile knob | Reuse condition analysis for repeated predicates. Requires compatible analyzer behavior. | Query/user setting depending on version. | Repeated selector/matcher families, range scans. | Freshness/semantics differ from query cache; analyzer dependency. | `EXPLAIN PLAN`/ProfileEvents for repeated predicate work; no correctness drift. | Future candidate only with version gate and explicit explain field. |
| Query cache (`use_query_cache` and related settings) | Unsafe/out-of-scope for default shim serving | Cache final query results. | Session/user/server. | Dashboard repeat queries. | Freshness and tenant isolation caveats; can mask measurement signals. | If tested, only with negative controls and clear freshness contract. | No default. Keep disabled for optimization measurement. |
| PREWHERE automatic behavior | SQL-shape alternative / ClickHouse optimizer behavior | Push selective filters before full reads. | ClickHouse optimizer/analyzer decides. | Selector and matcher-heavy families. | Manual forcing can be cosmetic or harmful; hidden dependency on SQL shape. | `EXPLAIN PLAN indexes=1, actions=1`, `SelectedRows`, `SelectedBytes`. | Do not set a magic knob; prefer SQL predicates ClickHouse can push down. |
| Manual PREWHERE SQL rendering | SQL-shape alternative | Force early filter evaluation if ClickHouse does not infer it. | Renderer choice. | Exact metric/time/matcher selectors. | May be cosmetic after CH rewrite; can reduce readability or break version assumptions. | `EXPLAIN SYNTAX` must differ and ProfileEvents/storage counters must improve. | Future renderer experiment only, not a setting. |
| Analyzer enablement (`allow_experimental_analyzer` or successor) | Version-dependent/experimental | Unlock optimizer features such as condition cache or improved pushdown. | Server/profile/session depending on version. | Families relying on analyzer-only features. | Experimental/version-dependent; may change query semantics/plans. | Version matrix, explain output, compliance, sweep comparison. | No default; future opt-in profile only. |
| Projections | Server/operator recommendation | Precompute/layout alternate physical projections for metric scans. | DDL/storage. | Selector, aggregation, dashboard-heavy families. | Hidden benchmark dependency if not documented; maintenance/storage cost. | Benchmark manifest names reference profile; `EXPLAIN PLAN` projection usage. | No. Document as advisory deployment profile. |
| Materialized views | Server/operator recommendation | Pre-aggregate or transform common rollups. | DDL/pipeline. | Expensive rollups and high-cardinality aggregations. | Freshness, correctness, storage cost, query rewriting complexity. | Dedicated benchmark and correctness contract. | No hidden dependency; possible future explicit feature. |
| Ordering/primary key choices for TimeSeries target tables | Server/operator recommendation | Improve time and metric/matcher pruning. | Table engine/storage layout. | All scan-heavy families. | Existing deployments may differ; promshim correctness must not depend on it. | Reference-profile benchmark assumptions; `SelectedRows`/part-pruning counters. | No; document assumptions. |
| TTL/partitioning | Server/operator recommendation | Bound storage and partition-pruning shape. | DDL/storage policy. | Long-range profiles, retention-specific scans. | Can change available data and benchmark comparability. | Benchmark manifest names retention/profile and row counts. | No. |
| Data skipping indexes | Server/operator recommendation | Improve label/matcher pruning outside primary key. | DDL/storage. | Regex/equality matcher families. | Write overhead and version/layout assumptions. | `EXPLAIN PLAN indexes=1`, `SelectedRows`, `SelectedBytes`. | No hidden dependency; reference guidance only. |
| `max_bytes_before_external_group_by`, spill settings | User/profile or future shim profile | Bound memory for heavy aggregations. | Profile/session. | Aggregation, histogram, vector-match families. | Spilling may harm latency; correctness okay but operationally noisy. | Memory and spill ProfileEvents; dense negative controls. | Future measured profile only. |
| Distributed query settings (`distributed_product_mode`, shard parallelism, remote read limits) | Distributed-only | Control multi-shard behavior. | Cluster/session. | Future distributed deployments. | Not relevant to local harness; can change semantics/performance drastically. | Separate distributed benchmark plan. | No default local shim setting. |
| Compression/client transport settings | User/profile or client config | Reduce transfer or improve CPU tradeoff. | ClickHouse client/session. | Large output families. | CPU/network tradeoff; hard to attribute to query optimizer. | Network bytes, CPU, response size, p50 across transports. | Future transport profile, not a default optimizer dependency. |

## Settings profile contract

Settings profiles are named and bounded:

- `none`: minimal compatibility settings required for the `TimeSeries` target and
  JSON denormal encoding.
- `default_safe`: safety and provenance settings, including read-only scope,
  request timeout, client-close cancellation, and optional caps.
- `benchmark_control`: explicit measurement/calibration profile that adds
  `max_threads=4` to reduce benchmark thread pressure; it is not selected
  automatically for served traffic.
- `repeated_selective`, `tiny_instant`, `simple_range`, `long_range_scan`,
  `aggregation_heavy`, `join_heavy`, and `subtree_pushdown`: named
  provenance profiles that remain evidence-gated until a dedicated experiment
  justifies applied settings.

Profiles must appear in explain output and sweep artifacts with both the profile
name and the concrete statement settings sent to ClickHouse. Unknown or
unsupported settings must reject the optimized candidate or fall back to
`none`/strict routing; they must not silently change global server state.

## Caches and freshness

Do not conflate these surfaces:

- **Query condition cache** can help repeated condition analysis/pruning. It is a
  optimizer/filter aid and still needs `EXPLAIN` plus storage-counter evidence.
- **Query cache** caches result data. It can hide query work and has freshness
  caveats, so it is inappropriate for default optimization measurement.

All benchmark commands used for optimization attribution should avoid relying on
result-cache hits unless the claim is explicitly about result caching.

## Shim-owned allowlist

The first implemented allowlist is intentionally safety-first. It is emitted only
through promshim-owned query/session settings, never by mutating server-wide
ClickHouse configuration.

| Setting | Profile behavior | Scope | Version note | Validation / provenance |
|---|---|---|---|---|
| `allow_experimental_time_series_table` | Always applied, including `none`, because all promshim data paths target the experimental `TimeSeries` table. | Query/session. | Required by current target schema. | Explain reason `required_time_series_engine`; visible in ClickHouse settings/query log. |
| `output_format_json_quote_denormals` | Always applied to preserve JSON encoding of NaN/Inf values across transports. | Query/session. | Existing compatibility behavior. | Explain reason `preserve_json_nan_inf`; decoding tests cover denormals. |
| `log_comment` | Propagated from `X-Promshim-Log-Comment` when supplied; otherwise generated as a bounded endpoint/mode/policy plus request-parameter hash. | Query/session metadata. | Supported ClickHouse query setting. | Sweep memory summaries must report no missing log comments. Raw PromQL and label values are not put in generated comments. |
| `max_execution_time` | Applied by `default_safe` and gated profile names from `PROM_SHIM_REQUEST_TIMEOUT_SECONDS`. | Query/session. | Core ClickHouse setting. | Explain reason `safety_timeout`; timeout failures remain user-visible execution errors. |
| `timeout_overflow_mode` | Applied as `throw` with `max_execution_time`. | Query/session. | Core ClickHouse setting. | Explain reason `safety_timeout`; avoids partial/broken results. |
| `cancel_http_readonly_queries_on_client_close` | Applied by `default_safe`; most relevant to HTTP transport, harmless provenance for native. | Query/session. | Core ClickHouse setting. | Explain reason `cancel_on_client_close`. |
| `readonly` | Applied as `2` by `default_safe` so promshim statements stay read-only while still allowing per-query settings other than `readonly`. | Query/session. | Core ClickHouse setting; user/profile constraints may still reject it. | Explain reason `read_only_query_scope`; failures are visible rather than silently ignored. |
| `max_memory_usage` | Applied only when `PROM_SHIM_CLICKHOUSE_MAX_MEMORY_USAGE_BYTES>0`. | Query/session. | Core ClickHouse setting. | Explain reason `safety_memory_cap`; default skip reason `not_configured`. |
| `max_rows_to_read` | Applied only when `PROM_SHIM_CLICKHOUSE_MAX_ROWS_TO_READ>0`. | Query/session. | Core ClickHouse setting; some profiles/users may mark it readonly. | Explain reason `safety_read_cap`; default skip reason `requires_estimate_cap`. |
| `max_result_rows` | Applied only when `PROM_SHIM_CLICKHOUSE_MAX_RESULT_ROWS>0`. | Query/session. | Core ClickHouse setting. | Explain reason `safety_result_cap`; default skip reason `requires_result_contract`. |
| `use_query_condition_cache` | Named for `repeated_selective`, but skipped until a measured experiment justifies enabling it. | Query/session. | Added in ClickHouse 25.3; default changed in 25.4 per `src/Core/SettingsChangesHistory.cpp`. | Explain skip reasons `requires_measured_evidence` or `version_unsupported`. |
| `use_query_cache` | Explicitly skipped by all non-`none` profiles. | Query/session. | Core result-cache family. | Explain skip reason `freshness_sensitive_not_default`. |
| `max_threads` | Applied as `4` only by `benchmark_control`; otherwise available only to explicit allowlisted internal overrides. | Query/session. | Core ClickHouse setting. | Explain reason `benchmark_variance_thread_bound`; artifacts `settings-profile-benchmark-control-smoke` compare it with `none` and `default_safe`. |

The implemented profile names are `none`, `default_safe`, `repeated_selective`,
`tiny_instant`, `simple_range`, `long_range_scan`, `aggregation_heavy`,
`join_heavy`, `subtree_pushdown`, and `benchmark_control`. `benchmark_control`
applies only the bounded measurement knob above; the remaining non-default
performance-oriented names provide provenance and skip reasons until
sweep/ProfileEvents evidence justifies applied settings.

HyperDX/ClickStack survey notes from `~/code/external/hyperdx` reinforced this
split: its ClickHouse client applies operational defaults such as
`max_execution_time`, `cancel_http_readonly_queries_on_client_close`, and
version-available optimizer settings only after consulting `system.settings`, and
it treats `max_rows_to_read` as potentially readonly in some deployments. For
promshim, those examples are used as operational caution rather than as
authority to enable planner knobs globally.

## Status

No production performance setting is enabled without evidence. The
safety/provenance allowlist above is implemented, `benchmark_control` is an
explicit opt-in measurement profile, and the operator-facing reference profile
lives in [`docs/clickhouse-reference-profile.md`](clickhouse-reference-profile.md).
Future work may turn a skipped performance setting into an applied serving
profile only after adding version checks, configuration gates, explain fields,
named sweep evidence, and rollback notes.
