# Attempt 20260428-binary-subquery-runtime-candidate-rejected

## Hypothesis

A further runtime behavior tweak in binary-subquery thread-policy handling might yield measurable wins beyond the current scoped behavior.

## Baseline evidence reviewed

Used focused post-unblock measurement artifacts:

- `harness/artifacts/bench/standalone/20260428-iter29-binary-thread-policy-measure/bench-report.json`
- `harness/artifacts/bench/standalone/20260428-iter29-binary-thread-policy-measure/clickhouse-profile-bench-report.json`

Key observations (prefer vs force_supported):

- mixed-root query:
  - shim p50: `339.64ms` vs `336.52ms` (~0.9%)
  - queryDuration p50: `333ms` vs `332.5ms`
  - memory p50: `474974039` vs `474973487` bytes
  - functionExecute p50: `653` vs `653`
- nested-binary query:
  - shim p50: `197.84ms` vs `196.53ms` (~0.7%)
  - queryDuration p50: `195ms` vs `193.5ms`
  - memory p50: `89764632` vs `89764592` bytes
  - functionExecute p50: `650` vs `650`

## Implementation

No code change.

## Decision

Reject/defer runtime tweak for now.

Rationale: current non-wall-clock corroborating signals (memory, functionExecute, queryDuration) are effectively flat across compared modes for this narrow corpus. A new behavior change here would likely overfit noise and add risk without clear expected value.

## Next step

Shift next runtime candidate to a different shape/family with stronger expected signal, or enlarge corpus coverage for this family before revisiting additional thread-policy behavior changes.
