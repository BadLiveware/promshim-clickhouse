# Phase 10 — Long-tail compatibility and polish plan

## Goal

Systematically close the highest-value remaining Prometheus compatibility gaps without changing the shim HTTP contract or introducing silent semantic drift.

## Requirements

### Preserve
- Existing HTTP API surface:
  - `/api/v1/query`
  - `/api/v1/query_range`
  - metadata endpoints
- Explicit unsupported boundaries where parity is not yet achieved.
- Differential harness as the acceptance gate for newly promoted stable corpus entries.
- Current query contract behavior for range-root validation and result/error envelope shape.

### Add in Phase 10 initial slice
1. `absent()` support for supported instant-vector inputs.
2. `absent_over_time()` support for supported matrix/range-vector inputs.
3. Sparse/disappearing-series fixtures so staleness and absence semantics can be regression-checked.
4. Better parity for duplicate-labelset edge behavior.
5. Tighter Prometheus-style error shaping for key edge cases.

### Non-goals for the initial slice
- Do **not** add `lookback_delta` HTTP parameter support in this slice; preserve the current API contract.
- Do **not** silently delegate or approximate staleness-sensitive shapes if parity is unclear.
- Do **not** fold native histogram sample-type support into Phase 10 initial execution; that remains gated on unfinished Phase 5 groundwork.

## Current starting point

- Core planner/evaluator architecture is stable.
- Stable harness set is currently 82/82 passing.
- `absent` / `absent_over_time` are now implemented for the supported subset with unit/integration/harness coverage.
- The deterministic harness dataset now includes sparse/disappearing series fixtures for staleness and absence validation.
- Duplicate-labelset handling exists in some paths, but Phase 10 should audit remaining parity gaps against upstream collision patterns.
- Error typing (`bad_data`, `unsupported`, `execution`) exists, but wording/shape parity can still be improved.

## Ordered implementation slices

### Slice 10.1 — Add absent-family support (`Task #43`) — **Landed**

#### Scope
- Add analyzer/planner/logical-builder support for:
  - `absent(v instant-vector)`
  - `absent_over_time(v range-vector)`
- Add local execution path that:
  - returns empty vector when input is non-empty
  - returns `{...} 1` when input is empty
  - derives labels conservatively from selector-based inputs
  - falls back to `{}` for complex expressions where Prometheus does the same

#### Likely code areas
- `internal/promshim/plan/promql.go`
- `internal/promshim/logical_builder.go`
- `internal/promshim/planner.go`
- `internal/promshim/exec/` (new absent helper)
- planner/exec/integration tests

#### Validation
- Unit tests using upstream Prometheus absent examples:
  - selector with equality matcher => derived labels
  - regex/duplicate matcher cases => dropped ambiguous labels
  - complex expression inputs => `{}` output labels
- Hard/medium integration tests for empty/non-empty instant and range-window cases
- Harness entries promoted only after parity verification; stable corpus now includes absent-family coverage

#### Risks
- Label derivation from AST needs to match Prometheus historical quirks closely enough.
- `absent_over_time` requires expression-context-aware output even when runtime matrix is empty.

---

### Slice 10.2 — Add sparse/disappearing-series fixtures (`Task #44`) — **Landed**

#### Scope
- Extend deterministic harness and/or integration fixtures with series that:
  - stop emitting before the end of the query window
  - emit at wider cadence than the default step
  - create clean empty windows for `absent_over_time`
- Keep the current stable 82-query corpus intact while adding new fixture capability.

#### Likely code areas
- `internal/promharness/dataset.go`
- harness corpus/docs as needed
- integration fixtures/helpers if easier for first iteration

#### Validation
- Verify dataset still seeds deterministically.
- Keep existing corpus green unchanged.
- Add targeted fixture assertions before adding parity-gated new corpus rows.

#### Risks
- It is easy to destabilize the stable harness set by changing existing series unexpectedly.
- Sparse-fixture modeling must separate “no samples in window” from plain low-frequency series.

---

### Slice 10.3 — Align staleness-sensitive behavior (`Task #45`) — **Landed for current sparse/disappearing probe set**

#### Scope
- Identify Prometheus-vs-shim differences for:
  - instant selectors near the lookback boundary
  - range selectors over sparse/disappearing series
  - `absent_over_time` over windows with and without samples
- Prefer targeted local fallback or explicit unsupported handling for shapes where delegated behavior drifts.
- Assume the current implicit/default lookback semantics; do not widen API surface in this slice.

#### Validation
- Targeted harness or integration comparisons against Prometheus using sparse fixtures
- Explicit regression tests for boundary timestamps and “series disappeared” windows

#### Risks
- Some staleness behavior is deeply tied to backend sampling/lookback policy.
- There is risk of accidentally changing selector semantics more broadly than intended.

---

### Slice 10.4 — Harden duplicate-labelset parity (`Task #46`) — **Landed for local unary/vector-scalar name-dropping paths**

#### Scope
- Audit remaining code paths where label removal or function output can collapse distinct series into the same labelset.
- Align behavior with Prometheus for supported subset:
  - error when conflicting same-labelset samples coexist at the same evaluation timestamp
  - preserve current explicit bad_data behavior where appropriate
- Use upstream collision tests as references.

#### Likely code areas
- local expression execution
- label mutation helpers
- vector matching / function output post-processing
- service/integration tests for surfaced error text

#### Validation
- Add regression tests modeled on upstream `collision.test` / duplicate-labelset examples.
- Confirm existing non-collision paths remain green.

#### Risks
- Some paths may need centralized post-expression duplicate-labelset validation instead of one-off checks.

---

### Slice 10.5 — Tighten Prometheus-style API error shaping (`Task #47`) — **Initial tightening landed**

#### Scope
- Normalize public API responses for key planner/execution errors while preserving current contract fields.
- Focus on high-value edge cases:
  - parse failures
  - invalid expression type for range queries
  - duplicate-labelset failures
  - explicit unsupported feature boundaries
  - invalid regex / label validation failures

#### Validation
- Service-level regression tests over HTTP JSON envelope
- Ensure `errorType` remains stable while wording becomes more intentional and less implementation-leaky

#### Risks
- Over-normalizing too early can hide useful debugging context or churn existing tests.
- Must avoid breaking intentional `unsupported` vs `bad_data` distinctions already relied upon in the repo.

## Deferred track (not in initial execution slice)

### Native histogram nuances
- This remains Phase 10 candidate scope, but it is gated on unfinished Phase 5 native histogram sample-type groundwork.
- Recommendation: do **not** schedule this in the first Phase 10 execution batch.
- Revisit after Phase 5 histogram support has a concrete native-histogram implementation plan.

## Validation ladder

### Inner loop
- `gofmt -w ...`
- targeted `go test` for touched planner/executor/service packages
- targeted integration tests for new edge cases

### Checkpoints
- `go test ./...`
- `./scripts/run-harness.sh --no-build`

### Promotion rule
- Only add new Phase 10 corpus entries to the stable harness set after:
  1. unit/integration coverage exists,
  2. Prometheus parity is verified, and
  3. excluded/unstable cases are documented if not promotable.

## Recommended execution order

1. `Task #43` — Implement absent-family support
2. `Task #44` — Add sparse-series fixtures
3. `Task #45` — Align staleness-sensitive behavior
4. `Task #46` — Harden duplicate-labelset behavior
5. `Task #47` — Tighten API error shaping

## Rollback points

- Keep each slice independently revertible.
- Avoid mixing fixture changes with behavior changes when practical.
- Promote harness corpus rows in separate commits from core behavior changes when parity is newly established.
