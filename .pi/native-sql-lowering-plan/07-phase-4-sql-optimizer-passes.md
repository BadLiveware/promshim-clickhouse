# 07 — Phase 4 SQL optimizer passes

## Goal
Add the optimizer passes that make native lowering useful rather than merely functional.

## Required first passes
1. common-matcher inference + label-filter pushdown
2. required-column / projection pushdown with no `SELECT *`
3. function / pattern rewrite scaffolding for the supported native subset
4. redundant subquery / projection flattening
5. evaluation-range tightening where earlier scaffolding still leaves fetch windows broader than necessary

## Distinct tasks

1. **Implement common-matcher inference**
   - infer shared safe matchers across binary/vector expressions
   - trim them using `on(...)`, `ignoring(...)`, `group_left`, `group_right`

2. **Implement label-filter pushdown**
   - push explicit and inferred predicates to the deepest safe selector source
   - block pushdown across label mutation / duplicate-label ambiguity boundaries

3. **Implement required-column analysis**
   - derive needed columns top-down
   - keep `tags` optional
   - forbid `SELECT *`

4. **Add function / pattern rewrite scaffolding**
   - create typed rewrite hooks for the initial range/counter subset
   - do not over-broaden the supported pattern set yet

5. **Implement wrapper flattening**
   - eliminate redundant subqueries / projections / aliases
   - preserve semantic barriers deliberately

6. **Tighten fetch windows after other passes**
   - ensure post-rewrite selector fetch bounds are still minimal and correct

## Optional in same slice if simple
- constant folding
- alias normalization
- join-side predicate pushdown helpers

## Validation
- snapshot tests on rendered SQL before/after optimization
- optimizer-specific unit tests proving:
  - a predicate moved deeper
  - a common matcher was inferred only when safe
  - a redundant layer disappeared
  - `SELECT *` was eliminated
  - a semantic barrier was preserved
