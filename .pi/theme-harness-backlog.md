# Theme harness backlog

Date: 2026-04-21
Source run: `./scripts/run-harness.sh --all-themes`
Reports: `harness/artifacts/compare-report-*.json`

## Goal

Turn the draft theme-corpus failures into a concrete backlog by separating:

1. **corpus hygiene / oracle issues** that should be fixed or removed from draft expectations,
2. **real promshim product gaps** that need implementation or explicit rejection boundaries, and
3. **true parity bugs** where promshim returns a different successful result shape than Prometheus.

The stable default corpus remains green. This backlog is only about the draft theme corpora.

## Current snapshot by theme (latest full --all-themes pass)

| Theme | Result |
| --- | --- |
| `aggregation` | 80 ok / 2 error |
| `binary-arithmetic` | 29 ok |
| `comparison` | 4 ok |
| `histogram` | 20 ok |
| `label-mutation` | 1 ok |
| `range-function` | 4 ok |
| `range-selector` | 78 ok |
| `rate-family` | 76 ok / 2 error |
| `selector` | 98 ok / 2 error |
| `set-operator` | 4 ok |
| `subquery` | 1 ok / 2 error |
| `vector-matching` | 4 ok |

## Repeating failure clusters

These same query ids show up across multiple themes:

| Query id | Count | Class |
| --- | ---: | --- |
| `draft_cand_0225_rate_family_subquery_aggregation_selector` | 4 | promshim unsupported-by-design: `irate` over subquery |
| `draft_cand_0242_rate_family_subquery_aggregation_selector` | 4 | promshim unsupported-by-design: `rate` over subquery |

---

## 1) Corpus hygiene / oracle backlog

These are not promshim correctness bugs. They are draft-corpus issues where the query or expectation is bad.

### 1.1 Invalid Prometheus entries resolved

#### A. `draft_cand_0151_rate_family_aggregation_selector`
- Status: addressed via corpus clean-up in Task #54.

#### B. `draft_cand_0152_rate_family_vector_matching_aggregation_selector`, `draft_cand_0257_rate_family_vector_matching_aggregation_selector`
- Status: addressed via corpus clean-up in Task #54.

### 1.2 Reclassify unsupported-by-design subquery rate-family rows

These are already documented in `.pi/phase9-delegated-divergence-catalog.md` as intentionally unsupported due to delegated subquery divergence risk.

- Query ids:
  - `draft_cand_0225_rate_family_subquery_aggregation_selector`
  - `draft_cand_0242_rate_family_subquery_aggregation_selector`
- Current result:
  - promshim returns explicit unsupported errors,
  - but the draft theme rows still expect success.
- Action:
  - do **not** treat these as immediate implementation bugs unless scope changes,
  - instead decide whether theme corpora should:
    - exclude unsupported-by-design shapes from success themes, or
    - model them as expected unsupported/error rows.
- Done when:
  - these rows stop being counted as success-case regressions unless we explicitly choose to implement them.

### 1.3 Add a draft-theme policy note

The current `--all-themes` run mixes three things together:
- truly supported success cases,
- unsupported-by-design cases,
- malformed query rows.

Action:
- document the intended meaning of theme corpora:
  - are they a pure passing target,
  - or a discovery corpus that can intentionally contain unsupported and malformed rows?
- if they are a discovery corpus, downgrade or separate reporting so malformed or intentionally-unsupported rows are not confused with parity regressions.

Done when:
- theme failures cleanly distinguish **bad corpus rows**, **known unsupported**, and **real parity/implementation bugs**.

---

## 2) Real promshim implementation backlog

These are genuine product gaps or planner/executor issues.

### 2.1 Seal the `increase(...)` range-mode leak

#### Problem
Several queries that are expected to succeed end up routed into ClickHouse execution paths that call `increase(...)`, but ClickHouse itself does not implement that function. promshim leaks a backend `NOT_IMPLEMENTED` error instead of either:
- handling the query locally, or
- rejecting it cleanly at analysis/planning time.

#### Affected rows
- `draft_cand_0295_rate_family_aggregation_selector`
- `draft_cand_0593_rate_family_aggregation_selector`
- `draft_cand_0452_rate_family_selector`

#### Why this matters
This is worse than a normal unsupported gap because the planner is choosing a path that cannot succeed and then exposing backend implementation details.

#### Action
Choose one of these explicitly:
1. **preferred short-term**: add an analyzer/planner guard so these shapes fail as stable `unsupported PromQL` rather than leaking ClickHouse `NOT_IMPLEMENTED`, or
2. add a local fallback path for the supported `increase(...)` shapes needed by these queries, or
3. if native ClickHouse support is intended, only route there once that implementation actually exists.

#### Done when
- no theme row fails with raw ClickHouse `Function increase is not implemented`, and
- the affected shapes either succeed with parity or fail with an intentional shim error boundary.

### 2.2 `vector(...)` support

Implemented in this phase:
- local planning/execution for `vector(scalar)` now exists.
- coverage includes `vector(...)` fallback idiom used in division-with-empty-vector queries.
- affected IDs now pass: `draft_cand_0329_histogram_rate_family_aggregation_selector`, `draft_cand_0622_histogram_rate_family_aggregation_selector`, `draft_cand_0330_rate_family_aggregation_selector`.

#### Why this was required
`vector(...)` is common in Grafana-rate family dashboard patterns and was producing unsupported errors.

### 2.3 `round(...)` support

Implemented in this phase:
- local planning/execution for `round(vector[, scalar])` is now available.
- affected ID `draft_cand_0513_rate_family_aggregation_selector` now succeeds and metric-name drop behavior matches Prometheus expectations.

### 2.4 Nested aggregation

Implemented in this phase:
- nested aggregate operators are now handled for concrete dashboard-facing shapes.
- affected IDs now pass: `draft_cand_0053_rate_family_vector_matching_aggregation_selector`, `draft_cand_0214_aggregation_selector`.

### 2.5 Rate-family functions over subquery arguments

Implemented in this phase:
- local planning/execution now handles subquery-arg forms for:
  - `increase(...)`
  - `delta(...)`
  - `idelta(...)`
  - `changes(...)`
  - `deriv(...)`
  - `rate(...)`
  - `irate(...)`
- support works in both instant and range query modes through the local subquery execution path.
- output handling remains deterministic:
  - metric names are dropped
  - label ordering is stable
  - explain plans show explicit local strategy.

Theme-impact status:
- `draft_cand_0225_rate_family_subquery_aggregation_selector` now passes
- `draft_cand_0242_rate_family_subquery_aggregation_selector` now passes
- subquery and rate-family theme reports are fully green.

---

## 3) Resolved parity fixes

### 3.1 Range-function selector metric-name parity

Resolved in this phase:
- `draft_cand_0011_range_function_selector` now passes
- local range-function outputs mirror Prometheus metric-name dropping for this theme-covered shape.

---

## Recommended execution order

### Completed execution order
1. Fix or drop invalid Prometheus rows:
   - `0151`, `0152`, `0257` ✅
2. Fix the `increase(...)` range-mode leak:
   - `0295`, `0593`, `0452` ✅
3. Implement local support for rate-family functions over subquery arguments ✅
   - `0225`, `0242` and the remaining local subquery-support slices for
     `increase`, `delta`, `idelta`, `changes`, and `deriv`
4. Fix matrix `__name__` retention mismatch:
   - `0011` ✅
5. Implement theme-facing function gaps:
   - `vector(...)` ✅
   - `round(...)` ✅
   - nested aggregation ✅

Current remaining explicit boundary (deferred):
- none in the current theme-covered rate-family subquery surface.

---

## Acceptance bar status

Achieved in the current validation run:

1. **no Prometheus parse errors** remain for the corrected expected-success theme rows,
2. **no leaked ClickHouse `NOT_IMPLEMENTED` errors** remain in the addressed planner paths,
3. the `draft_cand_0011_range_function_selector` diff is gone,
4. the current theme reports are fully green.

Validated with:
- `go test ./...`
- `./scripts/run-harness.sh --theme subquery`
- `./scripts/run-harness.sh --theme rate-family --no-build`
- `./scripts/run-harness.sh --all-themes --no-build`
