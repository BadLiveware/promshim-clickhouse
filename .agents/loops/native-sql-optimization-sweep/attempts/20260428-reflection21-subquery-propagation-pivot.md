# Attempt 20260428-reflection21-subquery-propagation-pivot

## Hypothesis

After multiple low-risk explainability hardening iterations, expected value is now higher on a bounded behavior-oriented subquery propagation slice (with measurable query-log/ProfileEvents evidence) than on additional metadata-only guards.

## Baseline context reviewed

Recent accepted iterations established:

- subquery-node explain decision surfacing
- canonical reason-code alignment
- canonical rejected-alternative alignment
- service-level API regression guard for nested `query_settings=no_thread_cap`

This means the observability prerequisite from iteration-16 reflection is now substantially complete.

## Implementation

No code change in this iteration.

This iteration is a reflection-driven pivot decision to avoid further diminishing-return metadata-only edits.

## Validation/measurement

No runtime claim in this attempt.

Decision quality is based on accumulated prior evidence and current loop state review.

## Decision

Keep (planning pivot).

Next attempt should be a single bounded behavior slice: subquery physical preference propagation with before/after explain + query-log/ProfileEvents evidence, while preserving strategy correctness.
