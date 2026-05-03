# Phase 5 — Resource request/limit and active-status join variants

## Purpose

Optimize repeated CPU/memory request/limit recording-rule shapes that join
container resources against active pod status. This phase targets a moderate
occurrence, highly regular family that should benefit from the same structural
variant machinery developed earlier.

## Evidence and priority

Four recording rules share the same physical structure and are referenced 24
times total across namespace, node, and pod compute-resource dashboards:

- `cluster:namespace:pod_cpu:active:kube_pod_container_resource_requests`
- `cluster:namespace:pod_cpu:active:kube_pod_container_resource_limits`
- `cluster:namespace:pod_memory:active:kube_pod_container_resource_requests`
- `cluster:namespace:pod_memory:active:kube_pod_container_resource_limits`

Representative shape:

```promql
kube_pod_container_resource_requests{resource="memory", job="kube-state-metrics"}
* on (namespace, pod, cluster) group_left()
max by (namespace, pod, cluster) (
  kube_pod_status_phase{phase=~"Pending|Running"} == 1
)
```

## Implementation tasks

### 1. Reuse selector-variant infrastructure for resource variants

Work:

- Treat metric-family and `resource="cpu|memory"` differences as candidate
  selector variants only when the surrounding expression shape is identical.
- Keep metric-name differences more conservative than label-value differences;
  require explicit proof that the renderer can represent the broader selector
  without selecting unintended metrics.

Acceptance criteria:

- CPU/memory request/limit shapes are classified with clear hit/miss reasons.
- Unsafe metric-name broadening falls back to separate subplans.
- Tests cover label-only resource variants and metric-name variants separately.

Validation:

- `go test ./internal/promshim/native/renderer`

### 2. Factor active pod status RHS

Work:

- Identify repeated active-status RHS:

```promql
max by (namespace, pod, cluster) (
  kube_pod_status_phase{phase=~"Pending|Running"} == 1
)
```

- Factor or reuse it across request/limit and CPU/memory sibling expressions when
  rendered in one query.
- Keep exact comparison and phase regex behavior unchanged.

Acceptance criteria:

- A query combining multiple resource variants renders the active-status RHS
  fewer times or reports why ClickHouse factoring is not beneficial.
- Filters on `cluster`, `namespace`, and `pod` are pushed to both resource and
  status sides when safe.

Validation:

- Focused renderer tests.
- Live explain for namespace/node/pod CPU and memory quota panels.

## Risks

- Metric-name broadening can accidentally pull unrelated TimeSeries metrics if
  implemented too generally.
- Active-status joins affect whether inactive pods are filtered; semantics must
  remain exact.

## Exit criteria

- Resource request/limit variants have diagnostics and at least one concrete
  optimization or documented safe fallback.
- Representative quota panels remain correct and compact.
