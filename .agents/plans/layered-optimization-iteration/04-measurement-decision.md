# Measurement and decision — retired shard

The measurement protocol, acceptance rules, rejection rules, and current attempt
decision belong in:

```text
.ralph/layered-optimization-recursive.md
harness/artifacts/optimization-iterations/<candidate-id>/notes.md
```

Decision summary:

- **Accept** only when the declared non-p50 signal is present, correctness
  validation passes, and rollback is known.
- **Reject** when the signal fails, correctness fails, risk exceeds value, or the
  evidence invalidates the adaptation.
- **Defer** when evidence is incomplete, stale, noisy, or blocked by missing
  instrumentation/profile data.
- **Split** when one candidate mixed multiple effects and attribution is unclear.

Record rejected/deferred/split attempts in:

```text
harness/artifacts/optimization-negative-results.md
```
