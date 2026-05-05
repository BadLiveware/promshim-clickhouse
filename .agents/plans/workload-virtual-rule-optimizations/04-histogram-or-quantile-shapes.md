# Phase 4 — Histogram fallback and repeated quantile shapes

## Purpose

Optimize histogram-heavy dashboard queries that combine native/classic fallback
branches with repeated quantile calculations. This phase targets common CoreDNS
and apiserver dashboard patterns after the higher-risk workload and metadata
join shapes are handled.

## Evidence and priority

- 42 dashboard queries use `histogram_quantile`.
- 22 dashboard queries use `or`; many are histogram native/classic fallback or
  metric-name compatibility fallbacks.
- CoreDNS repeats almost identical histogram query bodies for 0.99, 0.90, and
  0.50 quantiles across request/response size and duration panels.
- Recording rules include 14 `histogram_quantile` rules, including apiserver and
  kubelet quantile rules.

Representative dashboard shape:

```promql
histogram_quantile(
  0.99,
  sum(rate(coredns_dns_request_size_bytes{...}[5m])) by (proto)
  or
  sum(rate(coredns_dns_request_size_bytes_bucket{...}[5m])) by (le, proto)
)
```

## Implementation tasks

### 1. Classify histogram fallback `or` branches

Work:

- Detect `or` branches inside `histogram_quantile` where one side is a native
  histogram aggregate and the other is a classic `_bucket` aggregate.
- Verify compatible metric base names, label matchers, rate windows, and grouping
  labels.
- Reject branches with incompatible label sets, different windows, or additional
  arithmetic that changes sample meaning.

Acceptance criteria:

- Explain metadata distinguishes histogram fallback from generic set-union `or`.
- Unsafe or unrelated `or` expressions keep existing rendering.
- Tests cover CoreDNS-style native/classic fallback and a metric-name fallback
  that should not be treated as histogram fallback.

Validation:

- `go test ./internal/promshim/native/renderer ./internal/promshim/native`

### 2. Share repeated histogram inputs across quantiles

Work:

- Identify repeated histogram input expressions that differ only by quantile
  literal in sibling panel queries or within one query when possible.
- Inside one rendered query, factor the common rate/sum histogram input if
  ClickHouse can reuse it without changing results.
- For separate Grafana panel queries, record this as a possible caching or
  cross-request optimization, not an in-query rewrite.

Acceptance criteria:

- In-query repeated quantile inputs render the rate/sum input once where safe.
- For common separate-panel repetition, diagnostics or future notes identify it
  as a query-cache/collapsed-forwarding opportunity rather than overfitting the
  renderer.

Validation:

- Focused renderer tests if in-query sharing is implemented.
- Explain SQL comparison on representative CoreDNS queries.

### 3. Preserve fallback semantics

Work:

- Ensure native/classic fallback does not drop samples that Prometheus would keep
  through `or` semantics.
- Keep labelset compatibility checks strict, especially around `le` labels.
- Avoid using SQL `coalesce`-style rewrites unless labelset equivalence is proven.

Acceptance criteria:

- Differential tests or focused PromQL comparisons cover empty-native,
  empty-classic, and both-present cases.
- Fallback preserves Prometheus set-operator behavior.

Validation:

- `go test ./internal/promshim/native ./internal/promshim/native/renderer`
- Add compliance corpus cases if a new semantic path is introduced.

## Risks

- Histograms have subtle native/classic type behavior; treating them as ordinary
  scalar series can be incorrect.
- Some repeated quantiles are separate dashboard panel requests and cannot be
  optimized by a single-query renderer alone.

## Exit criteria

- Histogram fallback shapes are explainable.
- Safe in-query repetition is reduced, or the plan records that cross-request
  query caching is the right place for repeated quantiles.
