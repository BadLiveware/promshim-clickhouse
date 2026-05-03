# Phase 6 — Apiserver availability and error-budget shapes

## Purpose

Optimize high-complexity apiserver availability and SLI expressions after the
more frequent Kubernetes workload/resource dashboards are addressed. These
expressions have fewer dashboard references but dense `or`, regex selector, and
defaulting patterns that can become expensive if expanded naively.

## Evidence and priority

- `apiserver_request:availability30d` has 4 dashboard references and 3 recording
  definitions.
- The definitions contain 11 `or` operators and 25 regex matchers across
  read/write/all variants.
- Related burn-rate alerting rules are complex but were ignored for dashboard
  priority ordering; they may become relevant if alert-rule virtual evaluation
  becomes an operational target.
- `code_resource:apiserver_request_total:rate5m` has 6 dashboard references and
  2 selector-variant definitions for read/write verbs.

Representative characteristics:

```promql
sum by (cluster) (...{verb=~"LIST|GET", scope=~"resource|", le=~"1(\\.0)?"} or vector(0))
+
sum by (cluster) (...{verb=~"POST|PUT|PATCH|DELETE", le=~"1(\\.0)?"} or vector(0))
```

## Implementation tasks

### 1. Recognize `or vector(0)` defaulting

Work:

- Detect defaulting branches of the form `<expr> or vector(0)` inside arithmetic
  and aggregation contexts.
- Preserve Prometheus set-union semantics: this is safe only when the zero vector
  is used to provide an absent fallback, not to rewrite arbitrary labelsets.
- Add explain metadata for defaulting recognition and fallback reasons.

Acceptance criteria:

- Tests cover absent/present data behavior for recognized defaulting shapes.
- Unsupported `or` branches remain generic set operations.

Validation:

- `go test ./internal/promshim/native/renderer ./internal/promshim/local`

### 2. Group read/write selector variants

Work:

- Apply selector-variant grouping to exact or tightly bounded regex families
  such as read/write verb groups only when overlap can be proven absent.
- Prefer exact branch tables for explicit value sets parsed from simple regex
  alternatives like `LIST|GET`.
- Reject broad regexes and overlapping value sets.

Acceptance criteria:

- `code_resource:apiserver_request_total:rate5m` read/write variants are either
  grouped safely or explain why regex handling is not safe enough.
- Availability 30d variants reduce repeated selectors or provide precise skip
  reasons.

Validation:

- Focused renderer tests.
- Live explain for Kubernetes API server SLI/availability dashboard panels.

### 3. Consider alert-rule relevance separately

Work:

- Record whether the same optimizations would help burn-rate alerting rules.
- Do not expand the dashboard-targeted phase scope into alert compliance unless
  the user asks or operational evidence shows alert evaluation through promshim
  is required.

Acceptance criteria:

- Plan notes distinguish dashboard impact from alerting-rule complexity.

## Risks

- `or vector(0)` can be label-sensitive; careless rewrites can change empty vs
  zero behavior.
- Regex alternatives are not always finite disjoint sets.

## Exit criteria

- API server dashboard SLI/availability patterns have explainable handling.
- Any implemented defaulting or selector-variant optimization has tests for
  semantic edge cases.
