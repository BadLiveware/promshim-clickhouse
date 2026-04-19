# ROADMAP: Replace `monitoring-v2` Prometheus+Thanos with OTel + ClickHouse

## Why this exists

Current production monitoring in Tradera (`tradera-iac`) is a Prometheus + Thanos stack (referenced from:
`~/code/tradera/tradera-iac/kubernetes/base/tradera-applications/monitoring-v2`
and
`~/code/tradera/tradera-iac/setup/dev-dynamic/apps/monitoring-v2/`).

That setup has known operational pain:
- heavy distributed footprint at rest (Thanos query/query-frontend/store/compactor)
- complicated cold-start behavior and scale dynamics
- scaling down for long idle periods introduces latency/cold-state concerns
- long-term metrics persistence and lifecycle management is cumbersome.

This repository (`ch-observability`) is the local proof-of-concept for the replacement strategy.

## North Star

A single OLAP-first observability data plane where:
- all telemetry uses **OpenTelemetry** (`metrics`, `traces`, `logs`)
- ClickHouse is the primary storage for time-series/log/traces data
- Grafana is the main operator/UI layer (reusing existing dashboards + new ClickHouse-driven visuals)
- alerting is supported with acceptable SLO for production incidents
- horizontal behavior is simpler and more cost-efficient than current Prometheus+Thanos under low/variable load.

## Baseline to replace (current `monitoring-v2`)

From the current chart values we are replacing:
- `kube-prometheus-stack` (Prometheus + operator, kube-state-metrics, node-exporter, alertmanager)
- `thanos` subgraph (query/query-frontend/storegateway/compactor + object-store-backed retention)
- currently `tempo` disabled in dev/prod values
- existing `prometheus` datasource flow in Grafana.

## Required capabilities (must-have)

1. **Telemetry ingestion (OTel-first)**
   - Collect in-cluster metrics/traces/logs via OpenTelemetry Operator + sidecars/targeted instrumentation.
   - Support both pull-ish and push-ish producers where applicable (e.g., kube-state metrics via supported exporters).
   - Keep a migration path for existing Prom scrape targets.

2. **Metrics parity for critical queries**
   - Replace current Prometheus metrics views used by SRE/engineering dashboards.
   - Support key signal families:
     - CPU, memory, filesystem, network
     - pod/container lifecycle and restart/error patterns
     - deployment and workload scaling states
     - queue/request latency and error budgets where PromQL-derived metrics are consumed.

3. **Long-term retention + downsampling strategy in ClickHouse**
   - Tiered data retention (hot/warm/cold)
   - storage TTL policies and partition strategy by date
   - predictable cost under sustained/infrequent querying.
   - Backfill of historical data is not a mandatory requirement for v1, but if feasible, metric backfill is desirable for operational continuity.

4. **Trace + metrics + logs convergence**
   - Correlate telemetry by service, trace id, resource attributes.
   - Maintain fast lookup patterns for error investigations.

5. **Grafana migration**
   - Keep existing Grafana as primary UI where possible.
   - Introduce/continue ClickHouse datasource plugin.
   - Migrate top dashboards from Prometheus queries to ClickHouse-compatible queries.

6. **Alerting continuity**
   - Preserve current alerting targets and severity flows (Slack/PagerDuty-style routes).
   - Keep existing alert routing and ownership model while backing data source changes.

7. **Operational safety/operability**
   - predictable startup/rollback and minimal blast radius
   - secrets/config rotation hygiene
   - clear upgrade, restore, and recovery paths.

8. **Kubernetes production readiness**
   - chart/values layout that matches existing `monitoring-v2` app wiring
   - namespace-scoped resources and sane autoscaling defaults
   - clear capacity/SLO guardrails.

9. **Agent-first read-only Query Access**
   - Provide a stable, documented way for an operator/automation agent (human + bot) to run **read-only** ClickHouse queries.
   - Enforce read-only execution boundaries at the data access layer (dedicated read-only role/user/service account).
   - Standardize allowed query patterns and guardrails (time-window defaults, row/time limits, anti-wildcard/anti-cross-join safeguards).
   - Add auditability for agent read activity so suspicious query activity is observable and reversible.

10. **Local replacement POC (kind-first) before broader rollout**
   - Use a dedicated local Kubernetes replacement POC (kind) to validate the new stack before any production migration.
   - Run ClickHouse as a small 3-node cluster in-k8s to catch operator/HA/schema/backup edge-cases early.
   - Simulate object-store long-term retention paths in-k8s (e.g. local GCS-compatible endpoint such as `garage`) before touching prod values.
   - Keep this environment aligned with `monitoring-v2` contract shape (namespaces, chart layering, and datasource names) to reduce migration drift.

## Migration roadmap

### Phase 0 — Local replacement POC in kind (first step, now)
- Stand up a Kubernetes PoC chart path for **monitoring replacement** (separate from production paths) and verify it can be operated end-to-end.
- Deploy ClickHouse as a small **3-node** Stateful setup in kind and validate startup, replication, schema init, and query path.
- Add/validate **local GCS-style long-term storage simulation** (e.g. `garage`) and run basic retention/backup/restore checks.
- Finalize initial data model and SQL schema contracts in ClickHouse for:
  - metric points
  - logs
  - traces
  - relationship keys for join/correlation
- Lock required exporters and collector config for local stack parity.
- Add docs and smoke checks to validate ingestion and UI visibility.

### Phase 1 — Ingest parity in dev
- Stand up OTel collector in Kubernetes using the same patterns as this repo’s local OTLP endpoints.
- Send one representative workload (`core services + API simulators`) to ClickHouse.
- Keep Prometheus+Thanos temporarily for backstop while validating data quality.
- Optional: if metric backfill is practical, prove migration-time backfill for at least key metrics.
- Add baseline ClickHouse dashboards for:
  - service-level metrics
  - JVM/Node/API errors and latency
  - resource saturation.

### Phase 2 — Alerting and runbook parity
- Build/port 1:1 alerting rules to the new metrics source.
- Validate notification delivery behavior for current Slack/PagerDuty routing expectations.
- Rehearse incident workflows with synthetic failures.

### Phase 3 — Production pilot (canary namespace)
- Deploy ClickHouse-backed stack in a pilot environment in `monitoring-v2` path.
- Run dual-write and dual-read for a defined pilot period.
- Compare:
  - query latency
  - dashboard rendering latency
  - incident detection time
  - storage growth and cost.

### Phase 4 — Gradual cutover
- Move selected teams to ClickHouse dashboards first.
- Then disable selected Prometheus rule paths that are fully covered.
- Keep dual stack until confidence windows are reached.

### Phase 5 — Thanos decommission
- Remove or hard-disable Thanos components from chart values and app dependencies.
- Remove object-store retention wiring tied to Thanos object bucket.
- Simplify value hierarchy and defaults in `monitoring-v2`.

### Phase 6 — Post-cutover hardening
- Capacity tuning for ClickHouse under peak+idle traffic.
- Introduce retention + archive policy enforcement.
- Define runbook for schema evolution and reindex/rewrite operations.

## Technical requirements by area

### Collector/ingest
- Deterministic deployment manifests for:
  - collector in deployment + DaemonSet mode (as needed)
  - processors for resource attributes, service graph context, and cardinality control
  - batching and retry semantics tuned for bursty workloads
  - backpressure behavior that doesn’t drop critical signals.

### ClickHouse platform
- One or more clusters with:
  - persistent storage class chosen per env
  - replica strategy appropriate for dev/prod
  - merge-tree tuning for timeseries write/read balance
  - TTL and partitioning policy documented in code.

### Query layer
- Standardized query conventions for:
  - 1m / 5m / 1h rollups
  - consistent metric naming and units
  - naming/label taxonomy docs.

### Agent read/query access
- Define a dedicated read-only ClickHouse access path for internal agents (and automation).
- Expose the same logical query surface in both local and prod environments (UI + API/CLI if needed).
- Keep credentials short-lived or centrally issued (no long-lived admin credentials).
- Return bounded results with explicit time/row limits and timeout defaults to protect ClickHouse stability.
- Track and expose query activity/audit metadata for incident investigation.

#### Agent Query API contract (v1 draft)

- **Primary interface:** `ClickHouse MCP server` from
  [github.com/ClickHouse/mcp-clickhouse](https://github.com/ClickHouse/mcp-clickhouse)
  deployed as an internal service in local and prod environments.
  - This provides a standardized tool contract for agent read-only execution.
  - Use MCP-native transports (stdio/SSE/streamable-HTTP depending on deployment).
- **Interface guardrail policy:**
  - Backed by a dedicated read-only service account + DB user (or role) with enforced `SELECT`-only permissions.
  - Optional thin HTTP wrapper may be kept (`POST /api/observability/query`) if existing automation depends on JSON contracts.
- **Auth model:**
  - Local: static read-token + service identity for now.
  - Prod: short-lived token issued via cluster auth/credential vending path (SSO still out of scope).
- **Request/operation constraints:**
  - Prefer MCP tool arguments over raw SQL strings when possible.
  - If exposing direct SQL, limit input to read-only statements and required query context (`database`, `timeRange`, `limit`, `timeoutMs`).
- **Read-only guardrails (must enforce):**
  - Reject non-read statements (`insert`, `alter`, `drop`, `create`, `optimize`, etc.).
  - Enforce minimum time window and bounded `limit`.
  - Block expensive patterns unless explicitly whitelisted (e.g., broad cross joins).
  - Default query kill timeout and row cap on all requests.
- **Response envelope (minimum):**
  - `requestId`, `executedQuery`, `database`, `readRows`, `writtenRows: 0`, `elapsedMs`
  - `columns` (name/type metadata)
  - `rows` (value array) and `truncated: true/false`
- **Audit envelope:**
  - log caller identity, source (svc/CI/bot/user), `requestId`, timing, rows scanned/returned, and policy rejections.

### Grafana
- Datasource provisioning for ClickHouse in chart values.
- Dashboard migration playbook:
  - convert PromQL panels
  - replace Prometheus-specific variables where needed
  - verify cross-links to traces/logs.

### Security / platform
- For now: local static secrets may remain simple (as-is).
- Future milestone: SSO integration for identity/access (explicitly out of scope in this phase).

## Open risks / open questions
- Exact metric-to-ClickHouse schema fidelity vs PromQL expressiveness.
- Trace and log query ergonomics when moved entirely to one DB.
- Cost/perf trade-off of very high-cardinality labels in long-tail namespaces.
- Best-fit autoscaling policy for ClickHouse background merge/load patterns.

## Definition of done (for this roadmap)

- Production can run without Thanos.
- Core alerting and dashboard workflows pass parity checks.
- First-class agent read-only ClickHouse query access is validated in local and pilot environments.
- Infrequent query workload no longer requires a complex always-on distributed TSDB query mesh.
- SRE can safely operate the stack without custom, component-specific expertise in Thanos internals.
- A second phase plan for SSO and enterprise security hardening is queued.

## References

- Existing stack manifests:
  - `~/code/tradera/tradera-iac/kubernetes/base/tradera-applications/monitoring-v2`
  - `~/code/tradera/tradera-iac/setup/dev-dynamic/apps/monitoring-v2`

- Local bootstrap stack:
  - `scripts/bootstrap-kind.sh`
  - `chart/ch-observability-poc`
  - `README.md`
