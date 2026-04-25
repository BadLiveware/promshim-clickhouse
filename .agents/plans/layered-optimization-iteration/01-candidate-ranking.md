# Candidate ranking — retired shard

The continuous optimization loop no longer keeps ranking rules in this separate
file. Use the canonical loop file instead:

```text
.ralph/layered-optimization-recursive.md
```

Current rolling ranking artifacts remain:

```text
harness/artifacts/optimization-backlog.md
harness/artifacts/optimization-results.md
harness/artifacts/optimization-negative-results.md
```

Keep only 1–3 active hypotheses visible in the canonical loop file and replenish
them after each accepted, rejected, deferred, or split attempt. Research output
is seed material only; it does not outrank fresh local evidence.
