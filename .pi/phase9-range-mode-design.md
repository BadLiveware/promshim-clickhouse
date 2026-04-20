# Phase 9 — Local range-mode subquery design (remaining work)

## Problem

Current runtime values are:
- scalar
- vector
- matrix

For full local range-mode subquery semantics, evaluation can require an intermediate
"matrix-per-outer-step" shape before parent operators/functions reduce values.
That shape is not first-class in the current runtime model.

## Candidate approaches

### A) New nested runtime value type
Add an internal type (e.g. `WindowedMatrixValue`) keyed by outer evaluation timestamp,
where each key maps to matrix data for that subquery window.

Pros:
- Explicit and semantically clear.
- Enables faithful local parent function/operator implementations.

Cons:
- Requires broader executor plumbing updates.
- Must ensure it does not leak to HTTP rendering boundary.

### B) Streaming callback evaluator for subquery windows
Keep existing runtime types and implement evaluators that consume subquery windows
per outer step via callback (no explicit nested value object).

Pros:
- Less global type-surface expansion.
- Keeps nested shape internal to specific operators/functions.

Cons:
- Harder to reason about cross-operator composability.
- More bespoke code paths.

## Chosen direction for next slice

Start with **B (streaming callback evaluator)** to reduce risk and scope.

- Add reusable helper in planner/executor to iterate subquery windows by outer step.
- Keep existing `scalar/vector/matrix` runtime model unchanged for now.
- Implement first local matrix->vector consumer (`last_over_time`-style) on top.
- Reassess whether A is still needed after first consumer lands.

## Boundaries

- Do not change HTTP result contract.
- Keep unsupported combinations explicit.
- Keep differential harness corpus limited to stable parity cases.
