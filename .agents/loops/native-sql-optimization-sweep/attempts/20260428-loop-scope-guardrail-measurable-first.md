# Attempt 20260428-loop-scope-guardrail-measurable-first

## Request addressed

User requested: prioritize actual measurable improvements over infrastructure work.

## Changes made

Updated canonical loop policy in `.pi/loops/native-sql-optimization-sweep/loop.md` to enforce measurable-first execution:

- Scope now explicitly prioritizes measurable runtime improvements.
- Guardrails now constrain infra/diagnostic work to explicit unblock value for an active measurable candidate.
- Acceptance rules now require behavior changes to show expected runtime movement and restrict instrumentation-only work to direct unblock/de-risk value.
- Rejection rules now include infra/tooling drift without measurable-candidate unblock value.

## Decision

Keep (policy update, no runtime code change).

This re-aligns future iterations toward performance outcomes and away from open-ended infrastructure churn.
