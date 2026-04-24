# promshim

`promshim` is a Prometheus HTTP API compatibility layer for metrics stored in
ClickHouse's experimental `TimeSeries` table engine.

It lets existing Prometheus clients — most importantly Grafana dashboards and
PromQL-based tooling — continue to ask Prometheus-shaped questions while the
samples live in ClickHouse. It is the active part of this repository; the older
`chart/` work is the local ClickHouse + OpenTelemetry playground that promshim
was built to query.

Promshim is **not** a Prometheus server, a scraper, a remote-write receiver, an
Alertmanager, or a replacement for every TSDB responsibility. It is a read-side
bridge: parse PromQL, choose the best safe execution strategy, query
ClickHouse, and return Prometheus-compatible JSON.

## Compatibility at a glance

Promshim aims to be **100% Prometheus-compatible for the query API surface it
serves, as far as exact compatibility is possible outside Prometheus's own TSDB
implementation details**.

The current correctness gate is not a hand-written smoke test. Promshim passes,
within the narrow accepted-deviation policy below:

- the full upstream `prometheus/compliance` PromQL suite, run against reference
  Prometheus and promshim on the same frozen fixture;
- promshim's own deterministic differential harness and dashboard-focused
  corpora; and
- native-only coverage runs that keep tier-2 gaps visible instead of silently
  hiding them behind fallback execution.

The only accepted deviations are narrow, documented cases where exact
Prometheus behavior depends on storage-engine internals or tiny primitive-level
floating-point differences. Everything else is treated as a bug or visible
coverage gap.

## Where it fits

```mermaid
flowchart TB
  %% Deliberately keep edge labels out of the graph: GitHub's Mermaid controls
  %% sit on the right side, and long labels overlap on narrow screens.
  Producers["Metric producers<br/>exporters, OTel collectors,<br/>remote-write senders"]
  ClickHouse[(ClickHouse<br/>TimeSeries table)]
  Clients["Prometheus API clients<br/>Grafana, dashboards, tooling"]
  Promshim["promshim<br/>Prometheus-compatible read API"]

  Producers --> ClickHouse
  Clients --> Promshim
  Promshim --> ClickHouse
```

Read the arrows as:

| Flow | Meaning |
|---|---|
| Producers → ClickHouse | Metric samples are written into ClickHouse, usually through Prometheus remote write or OTel-driven collection. |
| Clients → promshim | Grafana and other Prometheus API clients call `/api/v1/query`, `/api/v1/query_range`, and metadata endpoints. |
| promshim → ClickHouse | Promshim reads `timeSeriesTags(...)` / `timeSeriesData(...)`, or delegates whole queries to `prometheusQuery(...)` / `prometheusQueryRange(...)` when safe. |

In the broader observability ecosystem, promshim sits between these pieces:

- **Prometheus clients:** promshim speaks the query-side subset of the
  Prometheus HTTP API so dashboards and diagnostic tools can keep using PromQL.
- **ClickHouse:** ClickHouse owns storage and most heavy execution. Promshim
  reads `timeSeriesTags(...)`, `timeSeriesData(...)`, and, when safe,
  ClickHouse's `prometheusQuery(...)` / `prometheusQueryRange(...)` table
  functions.
- **OpenTelemetry:** in the intended migration path, OTel handles collection and
  normalization while ClickHouse becomes the long-term telemetry store. Promshim
  preserves Prometheus read compatibility during that migration.
- **Grafana:** existing Prometheus datasource panels can point at promshim, while
  newer panels may use the ClickHouse datasource directly.
- **Thanos/Mimir/Cortex/VictoriaMetrics:** promshim is much narrower. It does
  not provide distributed Prometheus storage, replication, compaction, rule
  evaluation, or alerting. Its job is to make ClickHouse-hosted metrics usable
  from PromQL consumers.

## What it does

Promshim currently implements these HTTP surfaces:

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/query` | Prometheus instant query API |
| `GET /api/v1/query_range` | Prometheus range query API |
| `GET /api/v1/labels` | Label-name metadata over the ClickHouse `TimeSeries` table |
| `GET /api/v1/label/{name}/values` | Label-value metadata |
| `GET /api/v1/series` | Series metadata |
| `GET /api/v1/query_explain` | Plan-only instant-query explain output |
| `GET /api/v1/query_range_explain` | Plan-only range-query explain output |
| `GET /metrics` | Prometheus-format promshim process/shadow-mode metrics |
| `GET /health`, `GET /-/healthy`, `GET /-/ready` | Health/readiness probes |

Normal query responses use the Prometheus response envelope:

```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": []
  }
}
```

Successful query responses also include advisory headers:

- `X-Promshim-Strategy` — root execution strategy, such as `delegated_promql`,
  `native_sql`, `local`, or `chunked_local`.
- `X-Promshim-Fallback-Reason` — why a lower-priority strategy was used, when
  available.
- `X-Promshim-CH-Roundtrips` — ClickHouse request count observed while serving
  the HTTP request.
- `X-Promshim-CH-Millis` — cumulative ClickHouse request time observed by
  promshim.

`explain=1` or `explain=true` on the normal query endpoints keeps the regular
Prometheus `data` payload and adds a top-level `plan` object. The dedicated
`*_explain` endpoints return the plan without executing the query.

## How it works

Every PromQL request goes through the same pipeline:

1. **Parse with the upstream Prometheus parser.**
   The Go module currently vendors `github.com/prometheus/prometheus` and uses
   its PromQL parser/types so syntax and type checking stay close to upstream.
2. **Build and analyze a logical plan.**
   Promshim records expression kind, output type, label lineage, time
   requirements, selector shape, and native-lowering eligibility.
3. **Route by strict execution priority.**
   Higher tiers always beat lower tiers:

   1. **Whole-query delegation to ClickHouse PromQL** — send the complete query
      to ClickHouse's `prometheusQuery(...)` or `prometheusQueryRange(...)` when
      the conservative capability map says ClickHouse can safely own the entire
      expression.
   2. **Repository-owned native SQL lowering** — render ClickHouse SQL directly
      over the `TimeSeries` table functions. This is the main development path.
   3. **Local Go executor with subtree pushdown** — evaluate the expression in
      promshim, but push eligible subtrees down to native SQL or ClickHouse
      PromQL delegation.
   4. **Full local execution** — correctness fallback when no higher tier can
      serve the request.

4. **Execute against ClickHouse.**
   ClickHouse is queried over HTTP with parameterized multipart requests,
   `allow_experimental_time_series_table=1`, and `FORMAT JSONEachRow`.
5. **Decode and render Prometheus JSON.**
   Native ClickHouse rows are decoded into promshim's runtime vector/matrix
   model, response limits are enforced, and the result is streamed back in the
   Prometheus API shape.

The priority order is deliberate. Promshim is a bridge, not the desired final
query engine. As ClickHouse's native PromQL support matures, more queries should
move into whole-query delegation and less code should remain in the shim.

## Execution modes

The default mode is controlled by `PROM_SHIM_NATIVE_LOWERING_MODE` and can be
overridden per request with `native_lowering_mode=...`.

| Mode | Served result | Native/delegated behavior | Use case |
|---|---|---|---|
| `prefer` | First successful tier in priority order | Enabled | Normal mode; this is the default. |
| `off` | Local executor | Disabled except ordinary ClickHouse reads needed by local plans | Baseline/debug mode. |
| `explain` | Same planning freedom as `prefer` | Enabled | Always include explain output in normal query responses. |
| `shadow` | Local executor | Runs a native/delegated candidate in the background and records comparison metrics | Safe rollout and divergence detection. |
| `force_supported` | Native SQL root only | Fails unless the final root plan is `native_sql` | Native-only compliance and gap discovery. |

Shadow mode exposes process-local counters/histograms under `/metrics`. It is
intended for rollout confidence, not durable audit storage.

## PromQL coverage

Coverage is measured, not hand-waved. The compatibility claim above is backed
by these gates:

- `go run ./cmd/promshim-matrix` generates `.pi/path2-compliance-matrix.md` and
  `.pi/path2-compliance-matrix.json` from the parser-visible feature surface.
- `./scripts/run-compliance.sh` runs upstream `prometheus/compliance` against a
  reference Prometheus and promshim on the same frozen fixture.
- `./scripts/run-harness.sh` runs differential corpora, dashboard-focused
  corpora, compliance, and the benchmark tripwire.

Tier 2 native SQL is intended to be a complete PromQL execution path for the
float-sample/classic-histogram query surface this repo targets. In
`force_supported` mode, the full compliance and repo-owned harness suites are
expected to run without unsupported native roots: selectors and matchers,
`offset`/`@` modifiers, subqueries, aggregations including selection
aggregations, scalar/vector arithmetic, comparisons, vector matching, set
operators, range/counter functions, classic histogram helper functions, label
mutation, sort functions, scalar roots, `absent`, `absent_over_time`, `info`,
and the rest of the targeted PromQL surface are tier-2-covered.

The explicit scope boundary is **native histograms**. Promshim supports classic
Prometheus histogram bucket queries and histogram helper functions, but native
histogram samples are not currently part of the ClickHouse `TimeSeries`/harness
contract.

Known accepted deviations are intentionally narrow and live in
`harness/compliance/expected-failures.json`:

- `topk` exact-tie ordering can differ because Prometheus exposes TSDB iteration
  order as a tie-breaker that is not derivable from labels alone.
- A bounded tolerance exists for tiny ClickHouse-vs-Go modulo float drift:
  absolute error must stay within `1e-6`, with labels and timestamps still
  matching exactly. This accounts for ClickHouse's modulo implementation using
  `x - trunc(x / y) * y` where Go/Prometheus uses `math.Mod`.

Anything else is treated as a visible bug or coverage gap, not something to
hide in the allowlist.

## Data model assumptions

Promshim expects metrics in a ClickHouse `TimeSeries` table, normally:

```sql
CREATE DATABASE IF NOT EXISTS observability;
CREATE TABLE IF NOT EXISTS observability.prometheus ENGINE = TimeSeries;
```

The harness configures ClickHouse's Prometheus remote-write endpoint at
`/write`, backed by that table. Promshim reads the same table through:

- `timeSeriesTags(database.table)` for series metadata, label matchers, and
  min/max time bounds.
- `timeSeriesData(database.table)` for sample rows.
- `prometheusQuery(...)` and `prometheusQueryRange(...)` only when whole-query
  or subtree PromQL delegation is selected.

The `TimeSeries` engine is still experimental in ClickHouse, so the schema
contract is centralized under `internal/promshim/storage/schema/` to make
upstream ClickHouse changes easier to audit.

## Configuration

`cmd/promshim` reads configuration from environment variables:

| Variable | Default | Meaning |
|---|---:|---|
| `PROM_SHIM_LISTEN_ADDR` | `:9090` | HTTP listen address. |
| `PROM_SHIM_CLICKHOUSE_ENDPOINT` | `http://127.0.0.1:8123/` | ClickHouse HTTP endpoint. |
| `PROM_SHIM_CLICKHOUSE_DATABASE` | `observability` | ClickHouse database containing the `TimeSeries` table. |
| `PROM_SHIM_CLICKHOUSE_TABLE` | `prometheus` | ClickHouse `TimeSeries` table name. |
| `PROM_SHIM_CLICKHOUSE_USERNAME` | `default` | ClickHouse user. |
| `PROM_SHIM_CLICKHOUSE_PASSWORD` | `otel` | ClickHouse password. |
| `PROM_SHIM_REQUEST_TIMEOUT_SECONDS` | `30` | ClickHouse HTTP client timeout. |
| `PROM_SHIM_CLICKHOUSE_VERSION` | `26.3` | Version used by the delegation capability classifier. |
| `PROM_SHIM_NATIVE_LOWERING_MODE` | `prefer` | Global lowering mode; see execution modes above. |
| `PROM_SHIM_MAX_RANGE_POINTS_PER_SERIES` | `50000` | Reject range queries above this point count per series. |
| `PROM_SHIM_RANGE_CHUNK_POINTS_PER_SERIES` | `5000` | Chunk eligible local range plans above this point count per series. |
| `PROM_SHIM_MAX_RESPONSE_SERIES` | `5000` | Reject responses with more series than this limit. |
| `PROM_SHIM_MAX_RESPONSE_POINTS` | `500000` | Reject responses with more total points than this limit. |

Per-request knobs:

- `native_lowering_mode=off|prefer|explain|shadow|force_supported`
- `explain=1` or `explain=true`
- `X-Promshim-Log-Comment: ...` to forward a ClickHouse `log_comment` for query
  log/profile correlation.

## Quick start for local development

### Run the main validation workflow

From the repository root:

```bash
./scripts/run-harness.sh
```

That runs:

1. the deterministic differential corpus,
2. the stable dashboard subset,
3. the upstream PromQL compliance harness, and
4. the native-SQL benchmark tripwire.

Warm runs are expected to be fast; the scripts intentionally run in the
foreground and should not be wrapped in long external timeouts.

### Run only compliance

```bash
./scripts/run-compliance.sh
```

This performs two passes:

1. `prefer` mode, allowlist-gated; this is the correctness gate.
2. `force_supported` native-only mode, used to keep native gaps visible.

Useful variants:

```bash
./scripts/run-compliance.sh --skip-native
./scripts/run-compliance.sh --skip-prefer
./scripts/run-compliance.sh --keep-up
```

### Start a stack and query promshim manually

The compliance stack exposes Prometheus on `:29090`, promshim on `:29091`, and
ClickHouse on `:28123`:

```bash
./scripts/run-compliance.sh --keep-up --skip-native

curl 'http://localhost:29091/api/v1/query?query=up'

curl 'http://localhost:29091/api/v1/query_explain?query=sum%20by%20(job)%20(up)'

curl 'http://localhost:29091/api/v1/query?query=sum%20by%20(job)%20(up)&explain=1'
```

When finished:

```bash
cd harness/compliance && docker compose down
```

### Run promshim directly

If you already have a ClickHouse `TimeSeries` table:

```bash
go run ./cmd/promshim
```

Then point a Prometheus-compatible client at `http://localhost:9090`.

## Benchmarks and profiling

The benchmark tripwire compares reference Prometheus, promshim native SQL, and
local fallback behavior on pinned corpora:

```bash
./scripts/run-bench.sh --bring-up --matrix
```

Long-range profiles require the compliance stack to be running and the matching
long-range data to be seeded first:

```bash
./scripts/run-compliance.sh --keep-up --skip-classify
./scripts/seed-long-range.sh --profile 30d --target ch
./scripts/run-bench.sh --long-range 30d --matrix
```

Published benchmark matrix: **pending**. The current native-lowering IR migration
is still in flight, so the README intentionally does not freeze a performance
matrix yet. After that migration lands, run the short and long-range benchmark
profiles, preserve the artifacts, and add the resulting Native-vs-Prometheus
matrix here.

Cost-based execution (CBE) routing is also planned. The intended first version is
not a dynamic black-box optimizer; it should use static, pre-known heuristics
calibrated from this bench suite to decide when a lower tier is predictably
faster for a bounded small-query class, while keeping strict tier priority as the
default and preserving native-only compliance visibility. The working plan lives
in `.pi/cost-based-routing-plan.md`.

For native SQL optimization work, preserve before/after artifacts and inspect
ClickHouse profile counters rather than relying on wall-clock noise alone. The
repo includes helper scripts such as:

- `scripts/ch-profile-capture.sh`
- `scripts/ch-profile-diff.sh`
- `scripts/ch-explain.sh`
- `scripts/bench-matrix.sh`

## Trade-offs

Promshim exists because a direct migration from Prometheus+Thanos to
ClickHouse-backed observability has real compatibility risk. Its design chooses
an explicit set of trade-offs:

### Benefits

- **Preserves existing PromQL/Grafana workflows** while moving storage toward
  ClickHouse.
- **Keeps correctness measurable** with differential and upstream compliance
  harnesses.
- **Allows gradual rollout** through `prefer`, `shadow`, and `force_supported`
  modes.
- **Can retire itself incrementally** as ClickHouse whole-query PromQL support
  grows.
- **Uses ClickHouse where it is strong**: large scans, aggregations, label
  filtering, and long-range dashboard queries.

### Costs and limitations

- **It emulates Prometheus semantics over a different storage engine.** Some
  Prometheus behavior depends on TSDB implementation details rather than pure
  PromQL semantics.
- **ClickHouse `TimeSeries` is experimental.** Upstream schema/function changes
  can affect promshim.
- **Not every Prometheus API is implemented.** Promshim serves query and
  metadata endpoints, not scraping, recording rules, alerting, federation,
  admin APIs, or remote write.
- **Performance varies by query shape.** Native SQL is often the goal, but some
  shapes are still slower than Prometheus or require careful SQL-shape work.
- **Local fallback is for safety, not product direction.** New feature work
  should expand whole-query delegation or native SQL lowering, not the lower
  fallback tiers.
- **Operational maturity is intentionally PoC-grade.** This repository is a
  migration playground and validation harness, not a packaged production
  distribution.

## Repository map

| Path | Role |
|---|---|
| `cmd/promshim/` | Promshim binary entrypoint. |
| `internal/promshim/httpapi/` | Prometheus-compatible HTTP routing and response rendering. |
| `internal/promshim/logical/` | PromQL logical plan representation and logical optimization. |
| `internal/promshim/native/` | Native-lowering analysis, capability metadata, and optimizer. |
| `internal/promshim/native/renderer/` | ClickHouse SQL renderer for native lowering. |
| `internal/promshim/storage/` | ClickHouse HTTP client and SQL builders over `TimeSeries`. |
| `internal/promshim/local/` | Local executor and fallback/subtree-pushdown planner. |
| `internal/promshim/shadow/` | Shadow-mode comparison and metrics. |
| `harness/` | Deterministic differential harness and query corpora. |
| `harness/compliance/` | Upstream PromQL compliance harness integration. |
| `scripts/` | Local validation, benchmark, profile, and stack helpers. |
| `chart/` | Original ClickHouse + OTel playground charts. |

## Development rules of thumb

- Treat the execution priority as a hard invariant: whole-query delegation,
  then native SQL, then subtree pushdown, then local fallback.
- Put new feature coverage in tier 1 or tier 2. Tiers 3 and 4 are fallbacks and
  should only change for correctness fixes unless explicitly requested.
- Do not add compliance allowlist entries for shim gaps. Fix the gap or leave it
  visible.
- Use the harness before claiming support. For native work, run the native-only
  pass as well as the normal prefer-mode gate.
- For performance changes, keep the SQL shape, profile counters, and before/after
  benchmark artifacts with the change so the trade-off is reviewable.

## Current status

Promshim is a working compatibility bridge for the repository's ClickHouse
`TimeSeries` metrics experiments. Its Prometheus query compatibility is gated by
the full upstream compliance suite plus repo-owned differential/dashboard
harnesses, with only narrow documented deviations for behavior that cannot be
reproduced exactly outside Prometheus internals. The main native SQL path has
broad PromQL family coverage, but the project should still be read as an active
migration/compatibility layer rather than a general-purpose Prometheus
replacement. A published benchmark matrix will be added after the current
native-lowering IR migration settles and the benchmark artifacts are refreshed.
CBE routing based on static heuristics from those benchmark results is planned,
but strict tier-priority routing remains the default today.
