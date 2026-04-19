# k8s-monitoring Helm chart

This chart is a lightweight meta-chart for Kubernetes cluster-level observability that uses **kube-prometheus-stack as the single source** for:

1. Kubernetes dashboard ConfigMaps
2. `kube-state-metrics`
3. `prometheus-node-exporter`

It does **not** deploy Grafana by default.

## Usage

Render and apply manually:

```bash
helm template monitoring ./chart/k8s-monitoring --namespace monitoring-v2 \
  --include-crds \
  | kubectl -n monitoring-v2 apply -f -
```

## Defaults

By default, this chart:

- enables `kubePrometheusStack` dependency
- renders only dashboard ConfigMaps from kube-prometheus-stack (`grafana.enabled: false`, `grafana.forceDeployDashboards: true`)
- deploys `kube-state-metrics` and `prometheus-node-exporter` via kube-prometheus-stack
- keeps Prometheus Operator, Prometheus, Alertmanager and additional k8s component scrapers off
- disables ServiceMonitor resources by default to avoid requiring Prometheus Operator CRDs in every cluster

## Toggle scrape monitoring

If you want kube-prometheus-stack to render ServiceMonitor resources, set monitor flags under the same exporter blocks:

```yaml
kubePrometheusStack:
  kube-state-metrics:
    prometheus:
      monitor:
        enabled: true
        labels:
          release: monitoring
  prometheus-node-exporter:
    prometheus:
      monitor:
        enabled: true
        labels:
          release: monitoring
```

## Enable/disable components

```yaml
kubePrometheusStack:
  enabled: false
```

Disable just exporters while keeping dashboard generation on/off as needed:

```yaml
kubePrometheusStack:
  kubeStateMetrics:
    enabled: false
  'kube-state-metrics':
    enabled: false
  nodeExporter:
    enabled: false
  'prometheus-node-exporter':
    enabled: false
```

Enable Grafana deployment too (not default):

```yaml
kubePrometheusStack:
  grafana:
    enabled: true
```

Enable the full kube-prometheus-stack profile:

```yaml
kubePrometheusStack:
  grafana:
    enabled: true
  prometheusOperator:
    enabled: true
  prometheus:
    enabled: true
  alertmanager:
    enabled: true
```

## Notes

- This chart is dependency-driven; chart-level templates are intentionally minimal and focused on composition.
