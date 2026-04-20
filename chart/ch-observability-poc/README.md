# ch-observability-poc Helm chart

Private HA PoC chart for this repo.

## Deploy

```bash
./scripts/bootstrap-kind.sh
```

Or render and apply:

```bash
helm template ch-observability-poc ./chart/ch-observability-poc --namespace monitoring-v2 \
  --include-crds |
  kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -n monitoring-v2 -f -
```

## CNPG helper (Grafana metadata DB)

```bash
helm template ch-observability-cnpg ./chart/ch-observability-cnpg --namespace monitoring-v2 |
  kubectl --context kind-ch-observability-poc apply --server-side --field-manager=ch-observability-bootstrap --force-conflicts -n monitoring-v2 -f -
```

## Notes

- HA defaults are kept in `values.yaml`.
- ClickHouse bootstrap SQL defaults to replicated mode (`files/clickhouse/init/replicated/001-observability-bootstrap.sql`).
- Logs and traces are stored in OTel-native ClickHouse tables; metrics are written to `observability.prometheus` using ClickHouse `TimeSeries` + Prometheus remote-write.
- Grafana is managed natively through the Grafana Operator CR (`kind: Grafana`).
- This chart provisions its own dashboards and datasources via native CRDs (`GrafanaDatasource`, `GrafanaDashboard`).
- `k8s-sidecar` containers are also deployed so third-party charts can contribute dashboards and datasources via labeled ConfigMaps.
- This chart does **not** emit sidecar-fed Grafana dashboard/datasource ConfigMaps for its own content.
- The chart does emit the minimal dashboard provider ConfigMap Grafana needs so sidecar-written dashboard files are discovered.
- Grafana datasource plugin requirements are declared on the native `GrafanaDatasource` resources via `spec.plugins`.
