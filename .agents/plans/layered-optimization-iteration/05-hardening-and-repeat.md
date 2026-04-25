# Hardening and repeat — retired shard

Hardening and repeat rules are owned by the canonical loop file:

```text
.ralph/layered-optimization-recursive.md
```

Operational summary:

- Commit accepted improvements with code/tests/docs/artifact references and a
  body naming baseline, accepted artifact, measured signal, scope, rollback, and
  validation.
- Commit rejected/deferred attempts when durable evidence prevents repeated work.
- Keep the active context window small: current champion, 1–3 active hypotheses,
  and the last 3–5 attempt summaries.
- Archive old details to `.pi/optimization-loops/layered-optimization-recursive/attempt-archive.ndjson`
  when compaction is triggered.
- After every decision and commit, replenish the next candidate and continue
  until the user stops or a real blocker requires approval.
