# 09 — Phase 6 range functions and subqueries

## Goal
Make subqueries and range functions participate fully in maximal subtree lowering.

## Scope
- normalized range-vector fragment lowering
- subquery step-grid expansion
- lowering for the first range-heavy subset, likely:
  - `last_over_time`
  - `sum_over_time`
  - `avg_over_time`
  - `min_over_time`
  - `max_over_time`
  - `count_over_time`
- partial lowering around unsupported roots
- whole-root lowering when the range subtree is fully supported

## Distinct tasks

1. **Implement normalized range-vector fragment lowering**
   - keep range fragments relational and tall internally
   - delay final matrix shaping until late finalization

2. **Implement subquery step-grid expansion**
   - expand subqueries without treating them as mandatory execution barriers
   - make required-time-range propagation honor subquery timing

3. **Implement first range-function subset**
   - start with `last_over_time`, `sum_over_time`, `avg_over_time`, `min_over_time`, `max_over_time`, `count_over_time`
   - keep function rewrite and lowering behavior tied to Prometheus semantics

4. **Support mixed plans around unsupported roots**
   - lower maximal range/subquery child islands even when the root still executes locally

## Validation
- unit tests for time-window propagation
- integration tests for subquery examples
- differential tests against delegated path / Prometheus where practical
