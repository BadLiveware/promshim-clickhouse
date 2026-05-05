# Extracted dashboard and recording-rule patterns

## Inputs

- `../dashboards-new/rules.yaml`
- `../dashboards-new/queries.jsonl`
- Alerting rules were counted but excluded from dashboard-priority ordering.

Summary:

- 94 recording rules
- 149 alerting rules ignored for this dashboard-targeted ordering
- 582 dashboard queries
- 27 dashboards

## Dashboard references to recording rules

The following table ranks rule/query interactions by occurrence, rule
complexity, and presence of costly query operators such as `group_left`, `topk`,
`or`, `label_replace`, and `label_join`.

| Rank | Recording rule | Dashboard refs | Dashboards | Definitions | Key interaction facts | Priority implication |
|---:|---|---:|---:|---:|---|---|
| 1 | `namespace_workload_pod:kube_pod_owner:relabel` | 98 | 5 | 8 | Referencing queries include 80 `group_left` joins, 42 aggregations, 56 `rate`/range usages, and 10 `topk` usages. Rule definitions include 5 `group_left`, 12 `label_replace`, 5 `label_join`, 2 `topk`, and many selector variants. | Highest priority; proven SQL blowup and parser failure source. |
| 2 | `node_namespace_pod_container:container_cpu_usage_seconds_total:sum_rate5m` | 30 | 7 | 1 | Query references include 30 aggregations and 8 `group_left` joins. Rule contains `rate(...)`, `group_left(node)`, and `topk by (...) (1, max by (...))`. | High priority; common CPU source feeding workload joins. |
| 3 | `apiserver_request:availability30d` | 4 | 1 | 3 | Definitions contain 11 `or` operators and 25 regex matchers across all verb variants. | High complexity but lower dashboard occurrence. |
| 4 | `cluster:namespace:pod_*:active:kube_pod_container_resource_{requests,limits}` | 24 total | 3 | 4 | Four same-shape CPU/memory request/limit rules, each joined to active pod status with `group_left`. | Medium/high; good same-shape variant target. |
| 5 | `node_namespace_pod_container:container_memory_*` | 8 total | 1 | 4 | Memory working set/RSS/cache/swap share `group_left(node)` + `topk` pod-info enrichment. | Medium; same enrichment shape as CPU source. |
| 6 | `code_resource:apiserver_request_total:rate5m` | 6 | 1 | 2 | Read/write selector variants over `verb=~...`; used by apiserver SLI panels. | Medium; selector-variant grouping may apply. |
| 7 | node exporter rate/ratio rules | 1-3 each | 1-2 | many | Mostly simple rate or ratio rules with regex device/interface filters. | Lower; optimize generic filter handling rather than rule-specific rewrites. |

## Dashboard feature counts

| Feature | Count | Notes |
|---|---:|---|
| regex matchers | 402 | Mostly dashboard variables like `$cluster`, `$namespace`, `$instance`, `$job`; includes match-all cases. |
| range selectors | 286 | Usually rate-window queries; relevant to repeated scan cost. |
| `rate(...)` | 278 | Dominant dashboard time-series pattern. |
| `... by (...)` aggregations | 191 | Often combined with rates and joins. |
| `group_left` joins | 115 | Concentrated in Kubernetes workload, network, resource, and metadata-enrichment panels. |
| negative matchers | 112 | Common filter shape; mostly not safe for selector-variant union unless carefully bounded. |
| `topk` | 48 | Usually metadata-enrichment dedupe, not ranking in the dashboard sense. |
| `histogram_quantile` | 42 | CoreDNS and apiserver panels; often combined with `or` between native and bucket forms. |
| `or` | 22 | Metric-name compatibility fallbacks and histogram native/classic fallbacks. |
| `irate(...)` | 5 | Lower occurrence; same family as rate rules. |
| `increase(...)` | 1 | Low dashboard occurrence, but apiserver recording rules use increase-like SLI rules indirectly. |

## Recording-rule feature counts

| Feature | Count | Notes |
|---|---:|---|
| `... by (...)` aggregations | 60 | Rules commonly preserve a small label set. |
| range selectors | 57 | Mostly rate/increase recording rules. |
| `rate(...)` | 49 | Frequently materializes dashboard-friendly rates. |
| regex matchers | 39 | Includes selector families like apiserver read/write verbs and active pod phases. |
| negative matchers | 36 | Common in pod/resource filtering and apiserver subresource exclusions. |
| `group_left` joins | 22 | Metadata enrichment and active pod filtering. |
| `histogram_quantile` | 14 | Kubelet/apiserver quantile recording rules. |
| `or` | 12 | Fallback/defaulting in apiserver availability and node memory rules. |
| `topk` | 10 | Metadata dedupe in pod/node enrichment rules. |
| `label_replace` | 8 | Concentrated in workload-owner rule expansion. |
| `label_join` | 2 | Concentrated in job-owner workload branches. |

## High-priority pattern families

### Workload-owner virtual rule expansion

Primary query pattern:

```promql
<container or resource expression>
* on(cluster, namespace, pod)
  group_left(workload, workload_type)
  namespace_workload_pod:kube_pod_owner:relabel{...}
```

Why it matters:

- 98 references, 5 dashboards, highest live failure impact.
- The recording rule has 8 definitions with static `workload_type` labels or
  dynamic workload-type synthesis.
- Branches share large substructures but differ by selector matchers,
  `label_replace` destinations, and static labels.
- This family already caused multi-megabyte SQL and ClickHouse parser
  backtracking before the current fixes.

General optimization opportunities:

- Static-label union collapse.
- Selector matcher pushdown.
- Selector-variant union for same-shape branches.
- Branch-table rendering for exact selector variants.
- Explain metadata for branch collapse and skip reasons.

### Metadata enrichment joins

Representative rules and queries:

```promql
rate(container_cpu_usage_seconds_total{...}[5m])
* on(cluster, namespace, pod) group_left(node)
topk by (cluster, namespace, pod) (
  1, max by (cluster, namespace, pod, node) (kube_pod_info{node!=""})
)
```

Why it matters:

- 115 dashboard queries use `group_left`.
- 48 dashboard queries use `topk`.
- CPU and memory recording rules repeatedly attach node/pod metadata using the
  same dedupe idiom.

General optimization opportunities:

- Recognize `topk by K (1, max by K+L (...))` as deterministic metadata lookup
  when uniqueness is enforced by grouping labels.
- Avoid rerendering the same metadata lookup per sibling metric expression.
- Push dashboard template filters into both sides when label preservation is
  provable.

### Histogram fallback and quantile families

Representative dashboard pattern:

```promql
histogram_quantile(
  0.99,
  sum(rate(metric{...}[5m])) by (...)
  or
  sum(rate(metric_bucket{...}[5m])) by (le, ...)
)
```

Why it matters:

- 42 dashboard queries use `histogram_quantile`.
- 22 dashboard queries use `or`, many as metric compatibility or
  native/classic histogram fallback.
- CoreDNS repeats the same query body at 0.99, 0.90, and 0.50 quantiles for UDP
  and TCP size/duration panels.

General optimization opportunities:

- Detect same histogram input with multiple quantiles and share the rate/sum
  subquery where safe.
- Preserve fallback semantics for native-vs-bucket `or` without duplicating
  unrelated filters.
- Emit explain metadata when histogram fallback branches cannot be collapsed.

### Active pod resource request/limit shapes

Representative rule pattern:

```promql
kube_pod_container_resource_{requests,limits}{resource="cpu|memory", ...}
* on(namespace, pod, cluster) group_left()
max by (namespace, pod, cluster) (
  kube_pod_status_phase{phase=~"Pending|Running"} == 1
)
```

Why it matters:

- Four near-identical rules are referenced 24 times total across namespace,
  node, and pod dashboards.
- Branches vary primarily by metric name and `resource` selector.
- The active-pod status side is repeated across CPU/memory and requests/limits.

General optimization opportunities:

- Same-shape selector-variant grouping by `resource` and metric family.
- Repeated RHS metadata/status subplan factoring.
- Common filter pushdown through the resource/status join.

### Apiserver availability and error-budget shapes

Representative rule characteristics:

- `apiserver_request:availability30d` has 3 definitions and 4 dashboard refs.
- Its definitions include 11 `or` operators and 25 regex matchers.
- Burn-rate alerting rules have similar high-complexity structures but were not
  used to prioritize dashboard work.

General optimization opportunities:

- `or vector(0)` defaulting recognition.
- Selector-variant families for read/write verbs and resource scopes.
- Common subexpression sharing across read/write/all availability variants.

## Representative dashboard examples

Workload-owner panels:

- Kubernetes / Compute Resources / Namespace (Workloads) / CPU Usage
- Kubernetes / Compute Resources / Namespace (Workloads) / CPU Quota
- Kubernetes / Compute Resources / Namespace (Workloads) / Memory panels
- Kubernetes / Compute Resources / Workload
- Kubernetes / Networking / Namespace Workload and Workload dashboards

Histogram panels:

- CoreDNS / Requests size UDP/TCP, 99/90/50 percentiles
- CoreDNS / Responses duration and size percentiles
- Kubernetes / API server / Read and Write SLI duration

Metadata-enrichment panels:

- Kubernetes / Networking / Cluster current receive/transmit rate/status
- Kubernetes / Networking / Namespace Pods and Workload panels
- Kubernetes / Compute Resources CPU/memory pod/node/workload panels
