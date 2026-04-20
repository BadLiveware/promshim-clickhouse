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