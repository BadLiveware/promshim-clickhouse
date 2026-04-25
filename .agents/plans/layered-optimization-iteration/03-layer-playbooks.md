# Layer playbooks — retired shard

Layer-specific evidence rules now live in the canonical loop file and in the
project playbooks named by `AGENTS.md`.

Read the relevant playbook before acting when its trigger fires:

- `.pi/skills/running-sweep/SKILL.md` — benchmark sweeps, seed/profile setup,
  and sweep artifact review.
- `.pi/skills/measuring-ch-optimizations/SKILL.md` — ClickHouse/native SQL,
  CSE, alias, pushdown, scan-reduction, and performance claims.
- `.pi/skills/running-compliance/SKILL.md` — compliance runs/failures and
  expected-failure policy.

For all other layer rules, resume from:

```text
.ralph/layered-optimization-recursive.md
```
