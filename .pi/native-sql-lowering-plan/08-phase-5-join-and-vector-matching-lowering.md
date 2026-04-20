# 08 — Phase 5 join and vector matching lowering

## Goal
Add careful lowering for vector-vector binary operators.

## Scope
- one-to-one joins
- `on(...)` / `ignoring(...)`
- `group_left` / `group_right`
- duplicate-series error detection on the “one” side
- explicit metric-name preservation/drop rules
- timestamp-aware join conditions

## Distinct tasks

1. **Implement join-key derivation**
   - compute `join_group`
   - preserve `original_group` long enough to rebuild result labels
   - drop `__name__` from join key by default unless explicitly matched

2. **Implement cardinality-aware normalization**
   - identify the “one” side
   - validate uniqueness before final join
   - preserve Prometheus duplicate-series failure behavior using [promql/engine.go](file:///home/fl/code/external/prometheus/promql/engine.go)

3. **Implement label-copy semantics**
   - support `group_left` / `group_right` extra-label propagation
   - only copy labels after successful matches
   - re-check duplicate result groups if copied labels can collapse rows

4. **Implement timestamp-aware joins**
   - for instant vectors: align at evaluation time
   - for step/grid relations: join on `eval_ts` + `join_group`

## Validation
- focused unit tests mirroring upstream Prometheus/ClickHouse join cases
- integration tests for duplicate-series failures
- explain output showing join-key derivation and join-kind choice
