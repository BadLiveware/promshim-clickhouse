# Phase 9 — Nested evaluation semantics (implemented subset + boundaries)

## Implemented semantics

1. **Subquery window construction (instant context)**
   - Evaluate subquery over `[end-range, end]` with configured/derived step.
   - `end` resolution order:
     - explicit `@ <ts>` on subquery
     - `@ start()/@ end()` resolved from request context
     - otherwise parent instant evaluation time
   - `offset` shifts `end` backward before window construction.

2. **Nested composition in current subset**
   - Nested compositions are supported where parent consumes matrix and is implemented locally.
   - Current local matrix-consuming functions:
     - `last_over_time`
     - `sum_over_time`
     - `avg_over_time`
     - `max_over_time`
     - `min_over_time`
     - `count_over_time`

3. **Nested matrix-function composition**
   - Compositions of matrix-consuming functions and binary operators are supported in the implemented subset, for example:
     - `sum_over_time(...) + count_over_time(...)` (instant and range)
     - `sum_over_time(sum_over_time(...))`

4. **Range-mode parent behavior for local matrix-consuming functions**
   - Parent evaluates at each outer step using instant evaluation.
   - Result matrix point timestamps are normalized to outer step timestamps.

5. **Delegation vs local for subqueries**
   - Delegation is used when child is delegated-leaf-compatible except where explicit local handling is required to preserve known matrix-root subquery parity behavior.

## Explicit boundaries (current)

- Full matrix-consuming operator/function parity is not complete.
- Query-range matrix-root expressions remain explicitly rejected at HTTP layer.
- Delegation of rate-family functions over subquery windows remains explicitly blocked:
  - `rate`, `irate`, `increase`, `delta`, `idelta`, `deriv`, `changes`
  - These return explicit unsupported errors when used with `[range:step]` arguments.
- Some delegated subquery classes may still diverge from Prometheus in current ClickHouse versions.

## Next extension path

- Add more matrix-consuming functions/operators on top of the generic range-function abstraction.
- Reduce delegated divergence classes via targeted rewrite/local fallback.
