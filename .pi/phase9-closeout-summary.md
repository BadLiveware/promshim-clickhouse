# Phase 9 — Closeout summary

## Scope completed in this phase

- Implemented local matrix-subquery composition for supported parent classes:
  - `last_over_time`, `sum_over_time`, `avg_over_time`, `max_over_time`, `min_over_time`, `count_over_time`
  - matrix-to-vector and matrix-to-matrix compositions needed by these functions
  - instant + range execution for matrix-producing child expressions where implemented
- Added nested matrix binary composition support for implemented matrix functions and covered via tests.
- Added explicit planner/analyzer support for unsupported rate-family subquery forms:
  - `rate`, `irate`, `increase`, `delta`, `idelta`, `deriv`, `changes`
  - all variants now return hard unsupported errors with explicit reason text instead of silent delegate behavior.
- Added regression coverage across unit + hard integration + planner paths for the above.

## Confirmed stability and validation

- Harness corpus is at **67** query cases and passes end-to-end parity for supported classes:
  - `./scripts/run-harness.sh --no-build` => `67` queries with status `ok`.
- Local regression tests passing:
  - `go test ./internal/promshim/...`
  - `go test ./integration/promshim`

## Current explicit boundaries (intentional exclusions)

1. **Rate-family subquery class**
   - `rate`, `irate`, `increase`, `delta`, `idelta`, `deriv`, `changes` with `[range:step]` arguments are intentionally unsupported.
   - Reason: delegated execution showed numerical boundary divergence in previous ClickHouse/expr combinations; shim now fails explicitly at analysis/build time.

2. **Query-range matrix-root expressions**
   - Direct query-range matrix-root forms remain unsupported at request validation level with explicit `invalid expression type` error text.

3. **Remaining local-parity frontier**
   - Full Prometheus-compatible subquery semantics are not fully implemented for all matrix-consuming operator/function combinations.
   - Remaining work is tracked for later phases.

## Runtime strategy decision

- Phase 9 keeps the current execution model (`scalar/vector/matrix`) and uses streaming-window evaluation per outer step for local range-mode execution rather than introducing a dedicated nested matrix runtime type.

## Update status

Phase 9 is functionally closed for the implemented scope above; final status docs/task updates are now pending cleanup tasks that remain in the tracker.
