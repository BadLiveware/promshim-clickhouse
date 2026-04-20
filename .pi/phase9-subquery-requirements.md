# Phase 9 — Subquery Semantic Target Matrix

This document defines the implementation target for Phase 9 in the shim.

## Syntax baseline

Target syntax:

`<instant_query> '[' <range> ':' [<resolution>] ']' [ @ <float_literal>|start()|end() ] [ offset <duration> ]`

## Required behavior (Phase 9 completion target)

1. **Subquery returns matrix values** from an instant-vector child expression.
2. **Nested subqueries** are supported for implemented child semantics.
3. **Range query evaluation** keeps `start()` and `end()` stable across all steps.
4. **Instant query evaluation** resolves both `start()` and `end()` to evaluation time.
5. **`offset` and `@` interactions** on subquery expressions follow Prometheus ordering invariance (`@ ... offset ...` == `offset ... @ ...`).
6. **Timestamp alignment**:
   - Subquery points are emitted at subquery resolution boundaries.
   - Outer range evaluation samples subquery outputs per step as required by parent expression.
7. **Error shaping**:
   - Unsupported combinations fail explicitly with hard-difficulty/unsupported errors.
   - Do not silently coerce invalid types.

## Implementation slices

### Slice A (landed)
- Delegated-only subquery subset where inner expression is delegated-leaf-compatible.

### Slice B (landed)
- Explicit logical/execution nodes for subquery evaluation.
- Local subquery matrix construction from child instant-vector plans for instant-query path.

### Slice C (partially landed)
- Nested local-child subquery cases covered for implemented instant-mode subset.
- Query-range matrix-root boundary behavior is explicit (matrix-root expressions are rejected).

### Slice D (remaining)
- Full local range-mode subquery parity for the entire matrix-consuming function/operator space.
- Broader nested local/delegated compositions beyond current target set.
- Advanced timestamp/window edge-case parity checks for those frontier classes.

## Current architectural approach (important)

The runtime value model remains `scalar`, `vector`, and `matrix`.

This phase uses **streaming-window evaluation** for local range-mode matrix consumers (no new first-class nested matrix runtime type), while keeping unsupported forms explicit.

Full parity for all nested matrix cases is intentionally deferred to later slices and tracked as caveats/exclusions until explicitly implemented.

## Test matrix

### Unit
- plan support for valid/invalid subquery shapes.
- logical plan lowering for subquery nodes.
- execution tests for:
  - basic `[range:step]`
  - nested subquery
  - `@ start()/@ end()` and `offset` combinations
  - timestamp boundary alignment

### Integration
- hard tests for representative subquery expressions.
- explicit unsupported tests for deferred combinations.

### Differential harness
- add only stable-equality subquery corpus cases.
- keep known divergence cases documented and excluded until resolved.

## Known external caveats

- ClickHouse parser compatibility can diverge from Prometheus for some subquery forms; shim should normalize where possible, and otherwise fail explicitly or keep coverage excluded with caveat notes.
