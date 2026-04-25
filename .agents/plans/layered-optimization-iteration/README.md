# Layered optimization loop plan

This directory is now a compact handoff for the continuous optimization loop.
It is **not** the decision-critical source of truth while the Ralph loop is
running.

## Canonical loop file

Read and update this file first:

```text
.ralph/layered-optimization-recursive.md
```

That file owns the active objective, guardrails, measurement protocol,
acceptance/rejection rules, current champion snapshot, active hypotheses, recent
attempt summaries, compaction policy, commit policy, and stop rules.

## Why the old layered plan was collapsed

The previous version split the loop across ranking, experiment design,
measurement, hardening, layer playbooks, and research-seed documents. That made
fresh agents read several files before they could continue an iteration and
encouraged duplicated rules between the plan and the active Ralph task.

The loop is unbounded: it should keep selecting, measuring, accepting/rejecting,
recording, and replenishing attempts until the user stops it or a blocker
requires approval. Treat finite backlogs as a rolling idea queue, not as the
scope boundary.

## Single-source-of-truth ownership

| Information | Owner |
|---|---|
| Objective, guardrails, measurement protocol, thresholds, active hypotheses, current champion, recent results | `.ralph/layered-optimization-recursive.md` |
| Detailed/raw old attempt records when the Ralph file gets too large | `.pi/optimization-loops/layered-optimization-recursive/attempt-archive.ndjson` |
| Durable accepted outcomes | `harness/artifacts/optimization-results.md` |
| Durable rejected/deferred outcomes | `harness/artifacts/optimization-negative-results.md` |
| Current rolling backlog | `harness/artifacts/optimization-backlog.md` |
| Per-attempt evidence notes | `harness/artifacts/optimization-iterations/<candidate-id>/notes.md` |

Do not copy the same decision rule into multiple files. Add a short pointer to
the owner instead.

## Resume procedure

1. Read `.ralph/layered-optimization-recursive.md`.
2. Check workspace safety and preserve the pre-existing untracked duplicate
   `.agents/plans/cost-routing-calibration.md` unless explicitly asked to
   resolve it.
3. Read the active attempt note named in the Ralph ledger.
4. Read required playbooks only when their trigger fires:
   - `.pi/skills/running-sweep/SKILL.md` for sweeps/artifacts;
   - `.pi/skills/measuring-ch-optimizations/SKILL.md` for ClickHouse/native SQL
     optimization claims;
   - `.pi/skills/running-compliance/SKILL.md` for compliance failures or
     expected-failure decisions.
5. Execute one measured attempt at a time. Each attempt ends in an accepted,
   rejected, deferred, or split decision and a semantic commit when commit
   permission is active.
6. Replenish the rolling queue and continue. Do not stop because a finite list is
   exhausted.

## Minimal attempt record

Each active attempt note should contain only decision-critical information:

- candidate id, selected date, and git revision;
- hypothesis and expected non-p50 signal;
- baseline/post artifact paths or commands;
- correctness and rollback guardrails;
- validation commands declared before implementation;
- decision, evidence summary, retry condition if rejected/deferred, and commit.

Bulky generated artifacts stay under ignored artifact directories and are
referenced by path instead of copied into the loop file.

## Retired shard files

The numbered files in this directory are retained only as compatibility pointers
for older references. They intentionally defer to the canonical Ralph file and
should not be expanded again unless the user explicitly asks for a separate
bounded plan.
