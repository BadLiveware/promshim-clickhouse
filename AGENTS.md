# Project AGENTS (ch-observability)

## Purpose

Build a practical migration path from the current Tradera `example-namespace` Prometheus + Thanos stack to an **OpenTelemetry + ClickHouse** stack, with a single HA-first architecture and environment-specific capacity tuning.

## Project ownership / documentation scope

- This repository is being created from scratch and used as a private PoC.
- There are no teammates or external users for this PoC today.
- Do not write documentation aimed at other teams/users yet.
- Add/expand runbooks and user-facing docs only after the stack is in a good state and ready to be moved/migrated.

## Current work in this repo

- Stack deployment is chart-driven and HA-first:
  - `chart/ch-observability-poc`
  - `chart/ch-observability-poc/files/otel/otelcol-contrib.yaml`
  - `chart/ch-observability-poc/files/grafana/provisioning/*`
  - `chart/ch-observability-poc/files/cloudbeaver/config/*`
- The active deployment flow is:
  - `scripts/bootstrap-kind.sh` (preferred local bootstrap flow)
  - `helm template ... | kubectl apply` (authoritative chart render/apply path)
- Deployment convention for this repo: render manifests with `helm template` and apply with **server-side** kubectl apply (not `helm install`).
- Long-form migration plan is in:
  - `ROADMAP.md`

## External reference of current production setup

This repository is the source for the migration design; current production source configs are:
- `~/code/tradera/tradera-iac/kubernetes/base/tradera-applications/example-namespace/`
- `~/code/tradera/tradera-iac/setup/dev-dynamic/apps/example-namespace/`
- `~/code/tradera/helm-charts/charts/monitoring/` (published example-namespace Helm chart)

## Source of truth for chart internals

When evolving the Kubernetes migration chart, cross-check these first:
- `~/code/tradera/helm-charts/charts/monitoring/` for chart structure, conventions, and helper patterns.
- `~/code/tradera/tradera-iac/setup/dev-dynamic/apps/example-namespace/k8s/values.yaml` for environment shape and operational override values.
- `~/code/tradera/tradera-iac/kubernetes/base/tradera-applications/example-namespace/prod/values.yaml` for production defaults and alert routing conventions.

## What to keep in scope right now

1. Keep a single HA architecture as the default path.
2. Migrate configuration-first to Helm chart and keep it runnable on local `kind`.
3. Default to the HA ClickHouse topology (3-way shard/replica).
4. Treat environment differences as scaling knobs only: replica counts and resource sizing.
5. Keep credentials and defaults explicit; preserve observability and troubleshooting value (ingest, dashboards, query access).

## Must-nots / constraints

- SSO is explicitly out of scope for now.
- Read-only agent querying is required; use MCP-first path in planning/implementation (`ClickHouse MCP server`).
- Backfill is not a hard requirement for V1, but metric backfill is desirable when practical.
- Long-term Thanos migration details are tracked in `ROADMAP.md`.
- Do not preserve legacy or try to maintain compatibility with older versions of this POC itself. If compatibility breaks, recreate instances from scratch.
- In this PoC, OpenTelemetry is **operator-only**: do not retain legacy chart-managed Deployment/Service fallbacks for the collector.
- The chart remains the source of Kubernetes intent (templates/values/CRD defaults), and `scripts/bootstrap-kind.sh` is the current operational flow used in this repo for deployment.
  - `scripts/bootstrap-kind.sh` should therefore be treated as the development deploy operator and can contain retry/readiness orchestration (including CRD availability waits) needed for idempotent local installs.
  - This is acceptable today because external bootstrap orchestrators are not part of this repo's deployment flow.

## Where to look first

- `ROADMAP.md` for phase plan and acceptance criteria.
- `README.md` for quick local usage.
- `chart/ch-observability-poc/README.md` for Helm deployment via template/apply (`helm install` is intentionally not used).
- `scripts/bootstrap-kind.sh` is the primary reconciliation path for local bootstrapping and is expected to handle retries until CRDs and dependent resources converge.

## Validation check list (local)

- ClickHouse API reachable and schema bootstrap applied.
- OTLP collector receives telemetry.
- Grafana starts with ClickHouse datasource and dashboards.
- CloudBeaver prewired data source remains auto-authenticated when mounted from seed config.

## Environment and customization model

- This chart is run as a single HA configuration in all environments.
- We do not use Helm value overlays as separate environment layers in this repo.
- Environment-specific deltas are intentionally limited to:
  - replica counts (`clickhouse.replicas`, `otel.operator.replicas`, `otelScrape.replicas`, etc.)
  - resource sizing (`*.resources`, storage size/class, limits/requests)

## Clickhouse operator
We do NOT use the Alinity Clickhouse operator.
We do USE the new first party operator https://github.com/ClickHouse/clickhouse-operator

You will not get good search results for "clickhouse operator", it will _ALL_ be the Alinity operator unless you limit your result to either the github repository, or first party documentation.