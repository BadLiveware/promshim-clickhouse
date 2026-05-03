# Workload virtual-rule optimization plan

## Purpose

This plan sequences targeted optimizations for the recording-rule and dashboard
set in `../dashboards-new/`. The immediate operational target is the Kubernetes
workload dashboard family that expands
`namespace_workload_pod:kube_pod_owner:relabel`, but the implementation direction
is general: recognize safe PromQL shapes and optimize them conservatively across
recording-rule expansion and native SQL rendering.

This is a bounded phase plan, not an open-ended tuning loop. Each phase should
leave promshim with better explainability and a safe fallback path.

## Evidence source

Analyzed locally:

- `../dashboards-new/rules.yaml`
  - 94 recording rules
  - 149 alerting rules ignored for dashboard-priority ordering
- `../dashboards-new/queries.jsonl`
  - 582 dashboard queries
  - 27 dashboards

High-level dashboard feature counts:

| Feature | Dashboard queries |
|---|---:|
| regex matchers | 402 |
| range selectors | 286 |
| `rate(...)` | 278 |
| `... by (...)` aggregations | 191 |
| `group_left` joins | 115 |
| negative matchers | 112 |
| `topk` | 48 |
| `histogram_quantile` | 42 |
| `or` | 22 |

High-level recording-rule feature counts:

| Feature | Recording rules |
|---|---:|
| `... by (...)` aggregations | 60 |
| range selectors | 57 |
| `rate(...)` | 49 |
| regex matchers | 39 |
| negative matchers | 36 |
| `group_left` joins | 22 |
| `histogram_quantile` | 14 |
| `or` | 12 |
| `topk` | 10 |
| `label_replace` | 8 |
| `label_join` | 2 |

## Phase ordering rationale

Phases are ordered by a combined estimate of:

1. Observed badness in live validation: SQL size, parser backtracking, timeout,
   repeated subtrees, or high-cardinality joins.
2. Occurrence in `../dashboards-new/queries.jsonl`.
3. Recording-rule complexity in `../dashboards-new/rules.yaml`.
4. Generality: whether a shape applies outside one named metric/rule.
5. Safety: whether the optimization can be guarded with precise fallbacks.

## Phase index

1. [`01-current-virtual-rule-union.md`](01-current-virtual-rule-union.md)
   - Completed in-loop; static-label union explainability and matcher-safe pathing are in place.
2. [`02-selector-variant-union.md`](02-selector-variant-union.md)
   - Completed: safe selector-variant canonicalization + shared-selector-child
     reuse with nested unsafe-overlap explainability and evidence-signoff.
3. [`03-join-enrichment-shapes.md`](03-join-enrichment-shapes.md)
   - Next phase to implement: common metadata-enrichment joins using `group_left` and
     `topk by (...) (1, max by (...))`.
4. [`04-histogram-or-quantile-shapes.md`](04-histogram-or-quantile-shapes.md)
   - Collapse and share classic/native histogram fallback `or` patterns and
     repeated quantile work.
5. [`05-resource-status-join-variants.md`](05-resource-status-join-variants.md)
   - Optimize same-shape CPU/memory request/limit rules joined to active pod
     status.
6. [`06-apiserver-error-budget-shapes.md`](06-apiserver-error-budget-shapes.md)
   - Handle high-complexity apiserver availability/error-budget expressions with
     `or vector(0)` and regex selector families.
7. [`07-query-template-filter-normalization.md`](07-query-template-filter-normalization.md)
   - Normalize common dashboard template filters, match-all regexes, and safe
     matcher pushdown across query templates.
8. [`08-repeated-subtree-factoring.md`](08-repeated-subtree-factoring.md)
   - Evaluate native repeated-subtree factoring after earlier phases remove the
     biggest sources of duplication.

Supporting evidence and extracted pattern tables live in
[`evidence.md`](evidence.md).

## Global constraints

- Preserve Prometheus semantics and strict ambiguity behavior.
- Prefer rule/renderer optimizations over ClickHouse setting escalation.
- Every optimization must have a conservative fallback.
- Do not key logic to a single workload metric name when a structural pattern can
  be recognized instead.
- Do not add compliance expected-failure entries for shim bugs.
- If running compliance, sweeps, or benchmark claims, read the matching project
  playbook first.
- Keep produced code/docs domain-facing; do not mention plan phases in product
  code, generated SQL aliases, API fields, or user-facing docs.

## Overall validation strategy

For each phase:

- Add focused unit tests for applied and rejected shapes.
- Inspect `/api/v1/query_range_explain` for the representative dashboard query
  family before and after the change.
- Record rendered SQL length, `UNION ALL` count, optimization hit/miss metadata,
  and HTTP status for representative queries.
- Run focused Go packages during development and `go test ./...` before claiming
  the phase complete.
- Clean up any local port-forward or promshim processes after live validation.

Representative live queries for the current workload target:

- Namespace workload CPU usage and quota panels using
  `node_namespace_pod_container:container_cpu_usage_seconds_total:sum_rate5m`
  joined with `namespace_workload_pod:kube_pod_owner:relabel`.
- Namespace workload memory panels using container memory metrics or memory
  recording rules joined with `namespace_workload_pod:kube_pod_owner:relabel`.
- Workload and networking panels that use the same workload-owner enrichment
  rule or the same `topk` metadata-enrichment shape.

## Completion criteria for the whole plan

- The highest-occurrence workload-owner query family has compact native SQL,
  explainable optimization hits, and live HTTP 200 results in the KIND cluster.
- Other common dashboard patterns have either targeted optimizations or recorded
  skip reasons with lower priority evidence.
- Diagnostics make future dashboard blowups attributable to a specific shape or
  fallback reason.
- Full validation passes, or gaps are explicitly documented with missing
  infrastructure/credential reasons.
