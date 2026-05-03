# Phase 3 — Metadata-enrichment join shapes

## Purpose

Optimize common dashboard and recording-rule patterns that attach metadata with
`group_left` joins, especially the Kubernetes `topk by (...) (1, max by (...))`
dedupe idiom. This is the next broadest performance target after workload-owner
virtual-rule branch collapse.

## Evidence and priority

- 115 dashboard queries contain `group_left`.
- 48 dashboard queries contain `topk`.
- 22 recording rules contain `group_left`; 10 contain `topk`.
- `node_namespace_pod_container:container_cpu_usage_seconds_total:sum_rate5m`
  has 30 dashboard refs across 7 dashboards and contains:

```promql
sum by (cluster, namespace, pod, container) (
  rate(container_cpu_usage_seconds_total{...}[5m])
)
* on (cluster, namespace, pod) group_left(node)
topk by (cluster, namespace, pod) (
  1, max by (cluster, namespace, pod, node) (kube_pod_info{node!=""})
)
```

Similar enrichment appears in memory working set/RSS/cache/swap rules and in
networking dashboards.

## General shape

```promql
<fact series>
* on(<key labels>) group_left(<metadata labels>)
topk by (<key labels>) (
  1,
  max by (<key labels>, <metadata labels>) (<metadata selector>)
)
```

The expression is usually not a user-facing top-k ranking request. It is a
stable metadata lookup/dedupe pattern.

## Implementation tasks

### 1. Detect metadata lookup joins

Work:

- Identify binary joins where the RHS is `topk by K (1, max by K+M (...))`.
- Verify the join uses `on(K)` and `group_left(M)` or an empty `group_left()`
  where no metadata labels are included.
- Reject shapes where `topk` is not exactly `1`, where grouping labels do not
  align, or where the RHS expression is not a simple grouped metadata selector.

Acceptance criteria:

- Detection is structural and metric-name independent.
- Tests cover the CPU usage rule shape, memory rule shape, and an unsafe real
  ranking `topk` fallback.

Validation:

- `go test ./internal/promshim/native/renderer ./internal/promshim/local`

### 2. Reduce repeated rendering of metadata RHS

Work:

- Within one native rendered query, identify identical metadata lookup RHS
  subplans used by sibling expressions.
- Reuse or factor only the RHS lookup when ClickHouse SQL behavior is stable.
- Prefer a small typed helper shape over broad SQL-string CSE.

Acceptance criteria:

- Queries with multiple metrics enriched by the same pod/node lookup render the
  metadata lookup fewer times.
- Existing semantics for missing metadata, duplicate matches, and label
  propagation are preserved.
- Explain output reports when a metadata lookup shape was recognized and whether
  reuse/factoring was applied.

Validation:

- Focused renderer tests.
- Live explain for Kubernetes networking cluster and workload panels.

### 3. Push filters through metadata joins when safe

Work:

- Extend matcher pushdown or renderer predicate placement so dashboard filters
  on preserved key labels are applied to both the fact and metadata sides.
- Keep filters on metadata-only labels on the RHS unless the output semantics
  require post-join filtering.

Acceptance criteria:

- `cluster`, `namespace`, `pod`, and similar key filters reduce both sides of
  the join when label preservation is guaranteed.
- Unsafe filters remain as post-join predicates.

Validation:

- `go test ./internal/promshim/rules ./internal/promshim/native/renderer`
- Live explain SQL inspection for network and compute-resource dashboard panels.

## Risks

- `topk` has deterministic-enough use here because it is a dedupe idiom, but
  general `topk` ordering semantics must not be rewritten.
- RHS duplicate handling must preserve Prometheus grouping uniqueness behavior.
- Factoring may shorten SQL without improving runtime if ClickHouse inlines the
  subquery.

## Exit criteria

- Metadata lookup shapes are recognized and explainable.
- Repeated RHS metadata subplans are reduced for at least one representative
  dashboard query, or skip reasons document why factoring is not beneficial.
