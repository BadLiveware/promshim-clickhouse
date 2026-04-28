# Attempt 20260428-loop-guardrail-clarification-balance

## Request

Clarify loop language so infrastructure improvement is not disallowed, but should not dominate; measurable execution-resource improvements remain the priority.

## Update applied

Adjusted canonical guardrail/scope language in `.pi/loops/native-sql-optimization-sweep/loop.md`:

- Infrastructure/tooling/diagnostic work is explicitly allowed.
- Such work is framed as enabling and should support near-term measurable optimization outcomes.
- Priority remains measurable improvements in wall-time, absolute CPU, memory, and scan/bytes.
- Drift rule now targets repeated infra-only iterations without credible near-term measurable path.

## Decision

Keep (policy clarification, no code/runtime change).
