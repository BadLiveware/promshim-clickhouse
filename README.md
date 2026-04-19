# ch-observability

Local ClickHouse + OpenTelemetry playground for observability ingestion and querying.

## Services

- ClickHouse: `localhost:8123` (HTTP), `localhost:9000` (native)
- OpenTelemetry Collector: `localhost:4317` (OTLP gRPC), `localhost:4318` (OTLP HTTP)
- Grafana: `http://localhost:3000` (`admin` / `admin`)
- CloudBeaver (DBeaver web): `http://localhost:8978`

CloudBeaver includes a pre-wired global datasource file at:
  - `cloudbeaver/initial/data-sources.json`

## Start local stack

```bash
docker compose up -d
```

## Included Grafana assets

- Data source: `ClickHouse` (UID: `clickhouse`)
- Dashboards:
  - `ClickHouse Meta Overview`
  - `OTel Ingestion Health`

## .NET simulator apps

### 1) Worker simulator

A synthetic workload generator lives at:

- `src/ChObservability.Simulator`

It continuously emits **logs**, **traces**, and **metrics** using OpenTelemetry.

#### Run worker simulator

```bash
cd src/ChObservability.Simulator
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
dotnet run
```

#### Useful environment variables

- `OTEL_EXPORTER_OTLP_ENDPOINT` (default: `http://localhost:4317`)
- `SIM_LOOP_DELAY_MS` (default: `250`)
- `SIM_MAX_ITERATIONS` (default: infinite)

Example finite run:

```bash
SIM_MAX_ITERATIONS=50 SIM_LOOP_DELAY_MS=50 dotnet run
```

### 2) API simulator

A synthetic ASP.NET Core app with realistic paths lives at:

- `src/ChObservability.ApiSimulator`

It exposes endpoints with instrumentation for request/span/log/metric generation:

- `GET /health`
- `POST /api/login`
- `GET  /api/search?q=...`
- `POST /api/checkout`
- `POST /api/sync`

By default, it also has an **optional background load generator** that continuously calls its own endpoints to produce richer traces.

#### Run API simulator

```bash
cd src/ChObservability.ApiSimulator
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
API_SIM_PORT=8080 \
API_SIM_LOOP_DELAY_MS=200 \
API_SIM_MAX_ITERATIONS=100 \
dotnet run
```

#### Useful API environment variables

- `OTEL_EXPORTER_OTLP_ENDPOINT` (default: `http://localhost:4317`)
- `API_SIM_PORT` (default: `8080`)
- `API_SIM_LOOP_DELAY_MS` (default: `200`)
- `API_SIM_MAX_ITERATIONS` (default: infinite)
- `API_SIM_DISABLE=true` to skip background traffic generation

## DBeaver (CloudBeaver) setup

CloudBeaver is preconfigured to create the admin account automatically and skip the setup wizard on first start.

The repo pre-configures one global ClickHouse datasource:

- `Local ClickHouse (observability)`

It points at:
- Host: `clickhouse`
- Port: `8123`
- Database: `observability`
- Driver: `ClickHouse`

`data-sources-permissions.json` grants this connection to default users (`user` and `admin` teams), so it appears in anonymous/default logins and is visible immediately in the connections list.

`data-sources.json` also ships `otel` credentials so CloudBeaver can auto-authenticate to ClickHouse (`user=otel`, `password=otel`) for this local-only dev stack.

To run the full stack and validate that CloudBeaver picks up the preconfigured datasource:

```bash
./scripts/bootstrap.sh
```

If this is your first run (or if CloudBeaver state got out of sync), use:

```bash
./scripts/bootstrap.sh --full-reset
```

Then open `http://localhost:8978` and sign in with:

- User: `cbadmin`
- Password: `admin`

If you keep the default local settings, this connection is already pre-authenticated and opens without prompting.

If you disable it in `cloudbeaver/config/data-sources.json`, you'll then need to use:

- User: `otel`
- Password: `otel`

⚠️ These credentials are intentionally non-hardening defaults for local convenience only.

If you prefer a manual setup instead, you can create your own connection from UI using the same values above.

The `--full-reset` option is useful when the workspace has stale configuration and you want CloudBeaver to re-import
`cloudbeaver/config/*.json` as if it were a clean first run.

## Quick verification query

```bash
docker compose exec -T clickhouse clickhouse-client --user otel --password otel --query "
SELECT 'logs' AS signal, count() FROM observability.otel_logs
UNION ALL
SELECT 'traces', count() FROM observability.otel_traces
UNION ALL
SELECT 'metrics_sum', count() FROM observability.otel_metrics_sum
"
```

Tip: If you want API-only counts (service filter), use the values in your trace/table fields to filter by `service_name` or the resource attributes available in your OTel schema.
