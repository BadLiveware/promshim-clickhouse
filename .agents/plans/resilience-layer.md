# Resilience layer for promshim request handling

## Purpose

Add an admission/coalescing/caching layer in front of promshim's PromQL planner so that:

1. ClickHouse cannot be overloaded by promshim regardless of inbound traffic shape (thundering herd on dashboard load, refresh waves, slow CH queries causing pile-up, expensive ad-hoc queries).
2. Repeated and predictable Grafana traffic (same panel hit by many viewers, consecutive refreshes of the same query at fixed intervals) is served without re-doing work that has already been done.

This is a *resilience* track, not an *optimization* track. The success criteria are bounded memory, bounded backend concurrency, fail-fast under pressure, and graceful degradation during CH outages. p50/p95 improvements are downstream consequences, not the gate.

This work sits *above* promshim's existing tier-1/2/3/4 routing — every layer here either short-circuits before routing or applies bounded admission control before a request reaches the planner.

## Desired end state

- Inbound HTTP requests pass through admission control, in-flight deduplication, and a step-aligned response cache before reaching the PromQL planner.
- Each layer is independently feature-flagged; disabling all of them returns promshim to current behavior.
- Each layer is bounded in memory and concurrency. No unbounded queues, no unbounded caches, no unbounded in-flight sets.
- Alerting paths (and any caller sending `Cache-Control: no-cache`) bypass cache and single-flight without code changes at the caller.
- Observability is mandatory: every layer ships with hit/miss/eviction/drop/wait metrics from day one.
- Killing ClickHouse for 30s causes promshim to return fast 503s with `Retry-After`, with bounded memory and no goroutine explosion. After CH returns, request handling resumes without operator intervention.

## Why this is its own track

Existing loops don't fit:

- `tier2-native-optimization` measures emitted SQL via ClickHouse ProfileEvents (`FunctionExecute/query`, `selectedRows/query`). A response cache that returns without hitting CH won't show up in those counters.
- `tier2-benchmark-gap-closure` is scoped to "ClickHouse-side optimization work … transparent, removable, low-storage aids that do not change query text" — explicitly *ClickHouse-side*. The resilience layer is *promshim-side*.

Different acceptance criteria, different measurement axes, different rollback mechanism (feature flags rather than `DROP INDEX`).

The two existing loops should also continue measuring with the cache *forced off* — otherwise their evidence about SQL-shape changes will be masked by cache layer effects.

## Phase sequence

The order is dictated by what each layer *protects against*, not by what's most exciting.

| Phase | What | Why this order |
|---|---|---|
| [T1 — Bulkhead](./resilience-layer-T1-bulkhead.md) | Bounded backend concurrency + queue + fast-shed | The actual safety net. Without it, cache-cold thundering herds reach CH unbounded. Required before anything else. |
| [T2 — Single-flight](./resilience-layer-T2-singleflight.md) | Collapse concurrent identical queries to one backend call | Reduces pile-up at the same key before requests reach the bulkhead. Cheap, well-known pattern. |
| [T3 — Step-aligned cache](./resilience-layer-T3-step-aligned-cache.md) | Memoize results across consecutive Grafana refreshes | Eliminates the bulk of repeated work given dashboard refresh patterns. Built on top of T1/T2. |
| [T4 — Split-by-interval cache](./resilience-layer-T4-split-by-interval.md) | Cache historical chunks indefinitely; only re-execute the moving tail | Biggest scale win for long-range dashboards. Reuses existing chunk/merge primitives in `internal/promshim/local/chunking.go`. Built on top of T3. |

Phases can ship independently and provide value individually. Skipping T1 leaves protection gaps T2/T3/T4 cannot fill. Skipping T4 is acceptable; T1+T2+T3 already cover the most common dashboard-refresh patterns.

## Cross-cutting concerns

These apply to every phase and should be designed in from T1, not bolted on later.

### Observability

Every layer ships with Prometheus metrics on day one. Without them, you cannot tell whether the layer is helping, doing nothing, or amplifying a failure.

Minimum metric surface per layer:

- Bulkhead: `inflight`, `queue_depth`, `queue_full_drops`, `wait_duration_seconds`, `acquired_total`, `rejected_total`.
- Single-flight: `dedup_hits_total`, `waiters_per_key` histogram, `leader_errors_total`, `bounded_waiter_drops_total`.
- Cache: `hits_total`, `misses_total`, `evictions_total{reason="lru|ttl|bytes"}`, `bytes`, `entries`, `bypass_total{reason="alerting|header"}`.

Plus a structured log line on every drop/shed/eviction with a correlation ID so incident triage can trace what got dropped and why.

### Bypass paths

A consistent bypass policy applies to T2 (single-flight) and T3/T4 (cache):

- Alerting endpoints bypass entirely. Either a separate mux path or a marker on the `EvalParams`.
- `Cache-Control: no-cache` request header bypasses cache and single-flight.
- `?cache=skip` query parameter (debug only) bypasses cache and single-flight.
- Errors are not cached as success. Either skip cache write on error, or cache with explicit error marker and very short TTL (5–15s) to prevent retry storms.

### Feature flags

Every layer is independently toggleable via env var or runtime config. Default off until benchmarked + load-tested. Suggested env names:

- `PROM_SHIM_BULKHEAD_ENABLED`
- `PROM_SHIM_SINGLEFLIGHT_ENABLED`
- `PROM_SHIM_RESPONSE_CACHE_ENABLED`
- `PROM_SHIM_SPLIT_BY_INTERVAL_ENABLED`

Disabling all four restores current behavior exactly.

### Failure isolation

- Cache write failure must not fail the request. Silent fall-through to direct execution.
- Backend failure must not poison the cache.
- Single-flight leader failure must release the slot immediately, not lock the key for the duration.
- Memory pressure triggers eviction, not request rejection (until the bulkhead's separate ceiling is hit).

### Validation methodology

Resilience changes need *load tests* and *failure injection*, not just unit tests. Each phase includes a load-test gate:

- Sustained throughput at design capacity.
- Saturation behavior: graceful degradation, bounded memory, no goroutine explosion.
- CH-outage behavior: `kill -STOP` on a CH container for 30s. Promshim should fast-shed 503s, recover when CH returns.
- Cache-cold thundering herd (T3+): 1000 simultaneous identical requests with cache cold.

The benchmark stack is suitable for these tests via `scripts/run-sweep.sh` infrastructure plus a load-generator harness.

## Out of scope (and why)

- **Rollups, materialized views, projections.** Excluded by `tier2-benchmark-gap-closure` loop rules and by the user's stated criteria. Operator burden is real; resilience must not require a per-deployment maintenance contract.
- **Persistent / disk-backed cache.** In-memory only. Persistence brings invalidation, recovery, multi-process synchronization concerns that aren't worth the complexity.
- **Multi-instance / cluster-wide cache coherence.** Per-instance LRU is sufficient. If promshim runs N replicas, each gets its own cache. No distributed cache invalidation required.
- **Aggressive predictive prefetch.** A reactive cache is enough; predicting the next refresh is more complexity than the win warrants.
- **Snowflake/BigQuery-style automatic re-clustering of underlying tables.** ClickHouse does not have it; resilience layer does not need it.

## Anti-repeat / known traps

- Do not push `NOT isNaN(value)` into ASOF right-side data scans (carry-over from existing tier-2 invariants; relevant if any cache work touches lowering).
- Do not cache or coalesce alerting traffic without an explicit bypass path. Silent staleness on alerting will result in incorrect pages.
- Do not bound a cache only on entry count. A 10k-entry cache of 100MB-each entries is a 1TB allocation. Two bounds: max entries AND max bytes.
- Do not use access-time TTL. Insertion-time TTL is required for predictable staleness ceilings.
- Do not cache errors as if they were successful results.
- Do not unbounded-fan-in on single-flight. Cap waiters per key; excess get fast 503.

## Reference architecture

Cortex / Grafana Mimir's query-frontend implements all four phases for Prometheus-compatible storage. Useful study material for design specifics, especially T4 (their `splitBy` interval cache). Patterns are well-trodden; do not re-derive from first principles.
