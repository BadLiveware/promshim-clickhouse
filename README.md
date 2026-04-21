# ch-observability

Private HA playground for ClickHouse + OpenTelemetry observability.

Current storage split:
- logs/traces -> OTel-native ClickHouse tables
- metrics -> ClickHouse `TimeSeries` table (`observability.prometheus`) via Prometheus remote-write

## Quick start

```bash
./scripts/bootstrap-kind.sh
```

## Manual render/apply

```bash
helm template ch-observability-poc ./chart/ch-observability-poc --namespace monitoring-v2 \
  | kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -f -

helm template ch-observability-cnpg ./chart/ch-observability-cnpg --namespace monitoring-v2 \
  | kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -f -
```

## Useful access points

- ClickHouse: `http://localhost:8123`
- OTLP gRPC: `localhost:4317`
- OTLP HTTP: `localhost:4318`
- Collector health: `localhost:13133`
- Grafana: `http://localhost:3000` (`admin` / `admin`)
- CloudBeaver: `http://localhost:8978`

## Validation

```bash
kubectl -n monitoring-v2 port-forward svc/clickhouse 8123:8123
```

Then query:

```bash
curl -u otel:otel 'http://127.0.0.1:8123/?query=SELECT%20count()%20FROM%20system.databases'
```

## Prometheus shim debug/explain

The Go promshim exposes the normal Prometheus-compatible endpoints:

- `/api/v1/query`
- `/api/v1/query_range`
- `/metrics`

It also exposes plan-only debug endpoints:

- `/api/v1/query_explain`
- `/api/v1/query_range_explain`

These return the parsed execution plan, chosen strategy, estimates, and fallback reasons without executing the query against ClickHouse.

Examples:

```bash
curl 'http://127.0.0.1:9090/api/v1/query_explain?query=sum%20by%20(job)%20(up)&time=300'

curl 'http://127.0.0.1:9090/api/v1/query_range_explain?query=sum%20by%20(job)%20(label_join(up,%20"joined",%20"/",%20"job",%20"namespace"))&start=0&end=300&step=30'
```

For side-by-side debugging, the normal query endpoints also accept `explain=1` (or `explain=true`). When present, the response keeps the normal Prometheus `status` + `data` payload and adds a top-level `plan` object. Default requests without `explain` are unchanged.

`/metrics` exposes Prometheus-format process-local rollout signals for shadow mode, including shadow comparison counters partitioned by status/category/compare-mode and served/shadow plan/eval duration histograms.

Examples:

```bash
curl 'http://127.0.0.1:9090/api/v1/query?query=1%20%2B%202&time=300&explain=1'

curl 'http://127.0.0.1:9090/api/v1/query_range?query=1%20%2B%202&start=0&end=120&step=60&explain=true'
```