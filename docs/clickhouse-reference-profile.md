# Reference ClickHouse deployment profile for promshim

This document describes an operator-facing ClickHouse profile for promshim-style
PromQL workloads over ClickHouse `TimeSeries` data. It is a reproducibility and
troubleshooting guide, not a correctness contract: promshim query semantics must
remain correct without these server/operator choices.

The reference profile name for benchmark notes is
`promshim-ch-timeseries-reference-v1`. In the local benchmark harness this name
selects an optional compose override that makes query-log, ProfileEvents, and
settings attribution explicit; the default benchmark stack remains available as
`default-benchmark-compose`.

## Evidence labels

Every recommendation below uses one of these labels:

| Label | Meaning |
|---|---|
| `required-for-correctness` | Required to preserve promshim semantics. This should be rare and must cite the semantic dependency. |
| `advisory-for-performance` | Recommended starting point for production performance, but optional and workload-dependent. |
| `benchmark-reference-only` | Assumption used to make local measurements reproducible; not a production mandate. |
| `experimental-not-default` | Useful experiment for a named family only after EXPLAIN/ProfileEvents evidence and rollback. |

## First-party source pack

Use first-party ClickHouse sources as normative references for server/operator
settings. Non-first-party observability examples can inform caution, but they are
not authority for promshim defaults.

| Source | Why it matters |
|---|---|
| [ClickHouse query-level settings](https://clickhouse.com/docs/operations/settings/query-level) | Defines the layering between user/profile, session, and query settings. Promshim-owned settings belong to the query/session layer, not hidden server mutation. |
| [ClickHouse settings profiles](https://clickhouse.com/docs/operations/settings/settings-profiles) | Explains user/profile defaults and read-only profile examples. Operator profile choices live here or in user config. |
| [ClickHouse `system.query_log`](https://clickhouse.com/docs/operations/system-tables/query_log) | Provides duration, read rows/bytes, memory, settings, query IDs, and distributed child-query fields. Also documents `SYSTEM FLUSH LOGS`. |
| [ClickHouse `EXPLAIN`](https://clickhouse.com/docs/sql-reference/statements/explain) | Defines `EXPLAIN SYNTAX`, `PLAN`, `PIPELINE`, `ESTIMATE`, indexes/projections options, and distributed plan output. |
| [ClickHouse Operator overview](https://clickhouse.com/docs/clickhouse-operator/overview) | First-party Kubernetes operator scope: declarative cluster management, storage provisioning, HA, monitoring, and upgrades. |
| [ClickHouse operator minimal example](https://github.com/ClickHouse/clickhouse-operator/raw/refs/heads/main/examples/minimal.yaml) | Baseline manifest reference point; production profiles are additive to this minimal shape. |
| Local optimization-opportunity research snapshot and provenance sidecar | Background research for optimizer and deployment guidance; kept outside product documentation. |

## Reference workload

Promshim workloads are usually:

- read-heavy PromQL API traffic from dashboards, alerts, and ad-hoc queries;
- latency-sensitive, with bursts from dashboard refreshes;
- highly variable in selector cardinality, range width, and output points;
- repetitive enough that query-log correlation and condition-cache experiments can
  matter, but not repetitive enough to make result-cache freshness acceptable by
  default; and
- concurrent with ingestion and ClickHouse background merges.

Assumptions for `promshim-ch-timeseries-reference-v1`:

| Assumption | Evidence label | Notes | Validation signal / where to measure |
|---|---|---|---|
| ClickHouse stores metrics in the experimental `TimeSeries` table engine. | `required-for-correctness` | Current promshim SQL paths target `timeSeriesData(...)` and `timeSeriesTags(...)`; the shim-owned `allow_experimental_time_series_table` query setting is separate from this operator profile. | Query startup succeeds; explain SQL references `timeSeriesData`/`timeSeriesTags`; compliance remains clean. |
| Local benchmark stack is single-node ClickHouse plus single Prometheus and promshim. | `benchmark-reference-only` | The harness models deterministic comparisons and query-log evidence, not production HA or cross-shard fanout. | Sweep manifest profile/density/transport; `system.query_log.hostname`; container compose files. |
| Benchmark data is seeded with pinned profile end times and known sparse/dense datasets. | `benchmark-reference-only` | Results should name profile (`7d`, `30d`, `1y`), density, transport, settings profile, and corpus. | Sweep `manifest.json`; memory summary row counts; corpus path in bench report. |
| Production deployments may be single-node, replicated, or distributed. | `advisory-for-performance` | Route and storage recommendations are topology-dependent; correctness must not rely on one topology. | Query-log `is_initial_query`, `initial_query_id`, distributed EXPLAIN output, per-replica logs. |
| Query logging and ProfileEvents are enabled enough for tuning. | `benchmark-reference-only` for harness, `advisory-for-performance` for production | The benchmark override `harness/bench/docker-compose.reference.yml` mounts `harness/bench/clickhouse/users.d/reference-profile.xml`; production deployments should choose equivalent user/profile settings only for tuning windows where the overhead is acceptable. | `system.query_log` rows with `ProfileEvents`, `Settings`, `log_comment`, read rows/bytes, memory. |

The local benchmark stack does **not** model multi-tenant admission control,
object-storage latency, cross-zone network cost, full production retention,
replicated writes, or distributed fanout. Claims measured only on the local stack
must say so.

## Operator/server recommendation matrix

| Recommendation | Evidence label | Scope | Expected validation signal | Where to measure |
|---|---|---|---|---|
| Keep promshim traffic on a read-only ClickHouse user/profile where possible. | `advisory-for-performance` | User/profile defaults. Promshim also applies query-scoped `readonly=2` in `default_safe`. | Accidental writes fail; query settings remain visible. | `system.query_log.Settings`, user/profile definitions. |
| Enable query logging with ProfileEvents for benchmark and tuning windows. | `benchmark-reference-only` for harness, `advisory-for-performance` for production | Server/user profile. | No missing log comments; rows include duration, read rows/bytes, memory, ProfileEvents, and settings. | `system.query_log`; `SYSTEM FLUSH LOGS` during focused captures. |
| Keep `log_comment` bounded and generated by promshim when callers omit one. | `benchmark-reference-only` for measurement correlation; `advisory-for-performance` for production troubleshooting | Promshim query/session metadata. | Memory summaries show zero missing comments; comments do not contain raw PromQL/label values. | Sweep memory summaries; `system.query_log.Settings['log_comment']`. |
| Bound query time and memory through a combination of user/profile defaults and promshim `default_safe` settings. | `advisory-for-performance` | User/profile plus promshim query profile. | Timeout/resource errors are explicit; no partial results; dense controls expose cliffs. | Response errors, `system.query_log.exception`, `memory_usage`, `ProfileEvents['MemoryTrackerUsage']` when available. |
| Tune concurrency conservatively for dashboard workloads instead of maximizing per-query parallelism. | `advisory-for-performance` | Server/user profile and deployment sizing. | Lower tail latency under concurrent dashboards; no merge starvation. | `system.processes`, `system.query_log.query_duration_ms`, CPU saturation, background merge metrics. |
| Use local SSD or similarly low-latency storage for hot `TimeSeries` parts. | `advisory-for-performance` | Storage class / persistent volumes. | Lower read latency and less noisy p95 for scan-heavy families. | `query_duration_ms`, read bytes, OS read bytes, disk metrics, sweep p95. |
| Treat retention, TTL, and partitioning as benchmark context. | `benchmark-reference-only` unless adopted by an operator | DDL/storage policy. | Comparable row/part counts across sweeps; long-range profiles are named. | Sweep manifest; `EXPLAIN ESTIMATE`; query-log read rows/bytes. |
| Prefer storage layouts that let metric/time predicates prune early. | `advisory-for-performance` | Table/storage design. `TimeSeries` internals constrain what is directly configurable. | Fewer selected marks/rows for selector/range families. | `EXPLAIN PLAN indexes=1`, `EXPLAIN ESTIMATE`, `SelectedRows`, `SelectedMarks`, `read_rows`. |
| Add projections or materialized views only for stable, hot query families. | `experimental-not-default` | DDL/storage pipeline. | `EXPLAIN PLAN projections=1` shows use or filtering benefit; correctness and freshness are separately validated. | `EXPLAIN PLAN`, `system.query_log.projections`, benchmark artifacts for the named family. |
| Avoid ClickHouse result query cache as a default PromQL path. | `experimental-not-default` | User/profile/query setting. | If tested, cache-hit metrics are explicit and freshness contract is documented. | Query-cache metrics/settings; negative controls with fresh data. |
| Consider query condition cache only for repeated selective dashboard queries after version/evidence checks. | `experimental-not-default` | User/profile or promshim named performance profile. | Repeated predicate work or planning/filter counters improve without result freshness risk. | `EXPLAIN PLAN`, query-log ProfileEvents, repeated-selector sweeps. |
| Keep distributed-query settings out of the single-node reference profile. | `benchmark-reference-only` for local, `experimental-not-default` for distributed tuning | Distributed cluster/session. | Separate distributed benchmark proves fanout/cross-shard behavior. | `EXPLAIN PLAN distributed=1`, `system.query_log` initial/child rows, per-shard query logs. |

## Resource and concurrency guidance

PromQL dashboard bursts can create many overlapping range queries. A high
`max_threads` or high request concurrency can improve a single wide scan while
hurting p95 latency for a dashboard because queries compete with each other and
with background merges.

Recommended operating workflow:

1. Start with conservative user/profile concurrency and memory limits.
2. Run named sweeps or a dashboard replay with promshim log comments enabled.
3. Inspect `system.query_log` for `query_duration_ms`, `read_rows`, `read_bytes`,
   `memory_usage`, `Settings`, and ProfileEvents.
4. Inspect `system.processes` during load for queued/running query buildup.
5. If ingestion is concurrent, correlate latency spikes with merge pressure and
   disk metrics before changing SQL or promshim CBE costs.

Saturation symptoms and evidence:

| Symptom | Likely resource | Evidence to collect |
|---|---|---|
| p95/p99 dashboard latency rises while read rows are stable | CPU/concurrency contention | `query_duration_ms`, concurrent `system.processes`, CPU, ProfileEvents function counters. |
| Range queries get slower with larger retention even when output points are fixed | Storage pruning/layout | `SelectedMarks`, `SelectedRows`, `read_rows`, `read_bytes`, `EXPLAIN ESTIMATE`. |
| Aggregations or vector matches fail or spill under dense data | Memory/cardinality | `memory_usage`, `MemoryTrackerUsage`, result rows/bytes, response series/points caps. |
| Latency spikes during ingestion | Merge/disk contention | Merge metrics, disk read/write utilization, query-log windows around ingestion. |

## Storage and cache guidance

Storage choices are operator decisions. Promshim should expose query shape,
settings profile, and evidence; it should not silently require special server DDL
for correctness.

- Use fast local storage for hot parts when possible (`advisory-for-performance`).
  Validate with read latency, query p95, and OS/ClickHouse read bytes.
- Keep retention and TTL explicit (`benchmark-reference-only` in sweeps,
  `advisory-for-performance` in production). Validate by naming profile/density
  and checking row/part counts before comparing runs.
- Treat projections and materialized views as optional family accelerators
  (`experimental-not-default`). They need their own correctness/freshness validation path,
  `EXPLAIN PLAN projections=1`, query-log `projections`, and before/after sweeps.
- Distinguish caches:
  - filesystem and mark/index caches affect wall-clock and OS reads, so compare
    warm/cold context explicitly;
  - uncompressed cache can trade memory for CPU/latency and needs memory evidence;
  - query condition cache is a predicate/planning aid for repeated selective
    filters and is not a result cache;
  - query cache stores final results and can violate user freshness expectations
    if enabled without a clear PromQL contract.

Wall-clock deltas can be cache artifacts. For optimization attribution, pair
wall-clock with EXPLAIN and query-log counters that match the claim.

## Observability and correlation workflow

Minimum tuning workflow:

1. Use promshim explain endpoints or `explain=1` to capture candidate, route,
   settings profile, required input bounds, and rendered SQL.
2. Run the query with a bounded `X-Promshim-Log-Comment` or rely on promshim's
   generated comment.
3. Flush logs during focused captures with `SYSTEM FLUSH LOGS`.
4. Query `system.query_log` for `type='QueryFinish'`, the log comment, duration,
   read rows/bytes, memory, ProfileEvents, Settings, and query text.
5. Capture `EXPLAIN SYNTAX`, `EXPLAIN PLAN`, and `EXPLAIN PIPELINE` for SQL-shape
   claims. Use `EXPLAIN PLAN distributed=1` only for distributed deployments.
6. Save evidence under a named sweep/artifact directory and record profile,
   density, transport, ClickHouse version, and settings profile.

Be careful with noisy windows: manual probes, warmups, failed requests, and
background benchmark checks can all appear in `system.query_log`. Prefer bounded
log comments and artifact-local memory summaries over ad-hoc time-window counts.

## Single-node vs distributed applicability

| Topic | Single-node benchmark profile | Distributed ClickHouse |
|---|---|---|
| Correctness | Promshim semantics must match regardless of topology. | Same; distributed tuning must not change result semantics. |
| Query log | One local `system.query_log` is usually enough. | Initial query and child queries may be on different nodes; collect all replicas or use cluster-wide queries where available. |
| EXPLAIN | `EXPLAIN PLAN`/`PIPELINE` describes local execution. | Use `EXPLAIN PLAN distributed=1` and inspect remote plans/fanout. |
| Aggregations | Measures single-node scan/aggregation cost. | Cross-shard aggregation may add network and coordinator memory pressure. |
| Caches | Warmness is local to the benchmark node. | Cache warmness can differ by shard/replica. |
| Operator profile | Compose stack is not a Kubernetes HA model. | Use first-party clickhouse-operator resources and document storage class, replicas, shards, and Keeper assumptions. |
| Benchmark conclusions | Valid for local stack/profile/density named in the artifact. | Requires separate distributed sweeps or explicit future-work caveat. |

For now, promshim's reference benchmark profile is single-node. Distributed
recommendations are advisory and require separate validation before claims.

## Local harness alignment

The local benchmark compose stack intentionally models a measurement environment:

- fixed ClickHouse/Prometheus/promshim ports;
- isolated benchmark volumes separate from compliance;
- pinned data profiles and densities;
- query logging and generated log comments for memory summaries;
- native ClickHouse transport by default for benchmark stack runs.

The baseline benchmark compose profile is named `default-benchmark-compose` in
sweep artifacts. Selecting `promshim-ch-timeseries-reference-v1` adds only the
benchmark override `harness/bench/docker-compose.reference.yml`, which mounts
`harness/bench/clickhouse/users.d/reference-profile.xml` for explicit
`log_queries`, `log_profile_events`, and `log_query_settings` defaults. It does
not change the compliance compose stack and does not configure external
ClickHouse deployments.

It does not attempt to be a production operator manifest. Compliance remains
isolated from long-range benchmark data and should not inherit experimental
reference-profile tuning beyond the safety/logging needed for validation.

Current harness decision table:

| Setting surface | Local benchmark decision | User/operator guidance | Evidence |
|---|---|---|---|
| Query logging and settings/ProfileEvents attribution | Adopted in the optional reference-profile override. | Enable for tuning windows or observability-sensitive deployments when overhead is acceptable. | `harness/artifacts/bench/sweeps/ch-reference-profile-smoke/` manifest names `promshim-ch-timeseries-reference-v1`; memory summary has zero missing log comments. |
| Result query cache | Rejected as a benchmark default. | Keep disabled for PromQL serving unless a separate freshness contract is designed. | Baseline artifacts rely on real execution and query-log counters, not cache-hit masking. |
| Query condition cache | Not adopted as a server default. | Treat as a later repeated-selector experiment with version checks. | No before/after artifact currently proves a stable benefit for this corpus. |
| Bounded `max_threads` / concurrency defaults | Not adopted as a server default yet. | Tune per deployment from dashboard concurrency and CPU saturation evidence. | Existing sparse baseline is single-run oriented; concurrent mixed-corpus evidence is still required. |
| Projections/materialized views | Not adopted. | Consider only for named hot families with freshness/storage-cost validation. | No projection artifact proves a `TimeSeries` workload benefit. |

If future harness defaults intentionally model more of
`promshim-ch-timeseries-reference-v1`, the sweep manifest should name that
profile and the change should be validated with `run-sweep.sh --dry-run
--estimate` plus a live smoke.

## Review criteria

- [ ] Every recommendation uses one of the four evidence labels.
- [ ] Operator/server recommendations are separate from promshim-owned query
      settings.
- [ ] Benchmark assumptions name profile, density, transport, ClickHouse version,
      settings profile, and corpus.
- [ ] EXPLAIN/ProfileEvents/query-log evidence is required for optimization
      claims.
- [ ] Distributed behavior is either validated separately or scoped as advisory
      future work.
- [ ] No server/operator tuning is presented as a hidden promshim correctness
      dependency.
