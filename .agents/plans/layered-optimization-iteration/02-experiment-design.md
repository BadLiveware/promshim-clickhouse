# Experiment design — retired shard

The active proof boundary, expected signal, validation commands, and rollback
rules belong in the active attempt note referenced by:

```text
.ralph/layered-optimization-recursive.md
```

Use this compact rule set when creating or reviewing an attempt note:

1. Test one hypothesis only.
2. Declare the primary non-p50 signal before editing or benchmarking.
3. Name baseline and post-change artifact paths or capture commands.
4. State correctness guardrails and rollback path.
5. Run only the validation needed for the selected layer, plus `git diff --check`
   before committing.
6. End with accept, reject, defer, or split and record retry conditions for any
   non-accepted result.

Do not reintroduce a second experiment-design source of truth here.
