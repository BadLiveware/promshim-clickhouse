# Layered optimization loop charter

This file is the tracked, canonical charter for the layered promshim
optimization loop. It replaces the old phase-shaped plan with an unbounded
measure → change → measure → decide cycle.

The loop is not complete when the current candidate list is exhausted. A finite
backlog is only a rolling idea queue. After every accepted, rejected, deferred,
or split attempt, replenish the next hypothesis from local evidence, prior
results, code inspection, benchmark artifacts, or research seeds and continue
until the user stops the loop or a real blocker requires approval.

If a Ralph runner needs a `.ralph/` task file, create a local runtime mirror from
this charter, but do not let it become a second source of truth. This README owns
the decision rules.

## Objective

Continuously improve promshim's ability to answer Prometheus HTTP API queries on
ClickHouse `TimeSeries` data with lower cost, lower latency, or better routing
confidence while preserving PromQL compatibility.

Primary optimization evidence is candidate-specific and must be declared before
editing. Prefer non-p50 signals that explain why a change is better:

- ClickHouse work: read rows/bytes, selected marks, ProfileEvents, query log
  counters, query count, or pipeline shape;
- promshim work: local CPU, allocations, decoded samples, round trips, planner or
  optimizer work, cache hit/skip counters, or bounded metadata quality;
- routing quality: safer candidate eligibility, better cost-model features,
  fewer unknowns, fewer false wins, or more explainable fallback decisions.

Wall-clock latency is useful, but small wall-clock deltas are not proof by
itself. Treat deltas under 5% as noise unless they are backed by aligned
non-p50 evidence and repeated measurements.

## Guardrails

Every attempt must preserve these invariants unless the user explicitly changes
scope in the current conversation:

- PromQL semantics and compliance stay visible; shim bugs are not hidden in
  `harness/compliance/expected-failures.json`.
- Missing estimates, stale data, uncertain costs, unsupported semantics, and
  over-cap inputs route to the safe/reference path.
- Tier 1 whole-query ClickHouse delegation remains preferred when it is known
  correct; CBE may choose among native SQL, subtree pushdown, and local execution
  only when candidates are known-correct.
- Any new eligibility proof must fail closed for staleness, histograms, offsets,
  subqueries, vector matching, label mutation, and unsupported PromQL features
  unless those cases are explicitly proven.
- Benchmark and compliance stacks stay isolated. Do not run long-range benchmark
  data against compliance ports.
- Generated artifacts are referenced by path; do not paste bulky reports into
  this charter.
- Preserve unrelated workspace changes, including the pre-existing untracked
  duplicate `.agents/plans/cost-routing-calibration.md`, unless the user asks to
  resolve them.

## Required playbooks

Read the matching playbook before the trigger action:

- `.pi/skills/running-sweep/SKILL.md` before running or reviewing sweeps,
  setting up benchmark data, or comparing profiles/densities/transports/modes.
- `.pi/skills/measuring-ch-optimizations/SKILL.md` before evaluating native SQL,
  ClickHouse, CSE, alias, pushdown, scan-reduction, or performance claims.
- `.pi/skills/running-compliance/SKILL.md` before running compliance, triaging a
  compliance failure, native gap report, or expected-failure decision.

## Measurement protocol

Start each attempt with a short attempt note under:

```text
harness/artifacts/optimization-iterations/<candidate-id>/notes.md
```

The note must declare the hypothesis, primary signal, guardrails, validation
commands, baseline artifact path or command, and rollback path before code edits.

Default setup checks:

```bash
git status --short
./scripts/run-sweep.sh --bench-status
```

Default dry-run estimate before any new benchmark profile or corpus:

```bash
./scripts/run-sweep.sh --dry-run --estimate \
  --profile 7d --density sparse --corpus-set optimization
```

Default fresh benchmark shape for broad optimization candidates, adjusted only
when the attempt note explains why a different profile/corpus is needed:

```bash
./scripts/run-sweep.sh --name <candidate-id>-baseline \
  --profile 7d --density sparse --corpus-set optimization \
  --shim-modes prefer,force_supported,off \
  --routing-policies strict --memory summary --skip-compliance

./scripts/run-sweep.sh --name <candidate-id>-post \
  --profile 7d --density sparse --corpus-set optimization \
  --shim-modes prefer,force_supported,off \
  --routing-policies strict --memory summary --skip-compliance
```

Use a narrower command when the proof is intentionally narrow, such as focused Go
tests, `ch-explain` captures, calibration regeneration, or an explain-only
metadata check. Use broader sweeps only when the change can affect served query
behavior or routing decisions across families.

Minimum validation before accepting code changes:

```bash
git diff --check
```

Add the relevant project command for the touched surface, for example:

```bash
go test ./internal/promshim/...
go test ./cmd/promshim-routing-calibrate
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh scripts/ch-explain.sh scripts/bench-artifact-summary.sh
```

Compliance is required for changes that can affect PromQL results, routing
correctness, native lowering semantics, subtree pushdown semantics, or local
execution semantics. Read `.pi/skills/running-compliance/SKILL.md` first.

## Acceptance rules

Accept an attempt only when all are true:

1. The declared primary signal is present in fresh evidence.
2. Correctness validation passes or the attempt is explicitly instrumentation-only
   with no served behavior change.
3. Guardrail queries/families show no unexplained regression. For broad
   performance changes, investigate any guarded wall-clock or work-counter
   regression over 3% unless the attempt note predeclared a different bound.
4. Wall-clock-only wins are at least 10% on the affected query/family, or smaller
   wins are backed by aligned non-p50 evidence and repeated measurements.
5. The rollback path is a normal revert or a clearly bounded feature/profile
   switch.
6. Results are recorded in the active attempt note and, for durable wins,
   `harness/artifacts/optimization-results.md`.

Instrumentation/foundation attempts can be accepted without latency improvement
when they add bounded, validated evidence that makes later optimization decisions
more attributable and do not change served behavior.

## Rejection, deferral, and split rules

Reject when the declared signal is absent, correctness fails, the change routes
unsupported semantics unsafely, the cost/risk exceeds the measured value, or the
implementation only moves work without reducing total cost.

Defer when evidence is incomplete, noisy, stale, blocked by missing seed data, or
requires instrumentation that should be attempted first.

Split when one attempt changes multiple independent mechanisms and the evidence
cannot attribute the result.

Record all rejected, deferred, and split outcomes in:

```text
harness/artifacts/optimization-negative-results.md
```

Include the retry condition. A rejected result is valuable loop memory; do not
repeat it without a new data profile, ClickHouse version, proof method, or query
family that invalidates the previous reason.

## Current champion snapshot

Snapshot date: 2026-04-25.

Known accepted optimization-support commits:

- `2a2d22c` — ClickHouse proof signatures in benchmark summaries.
- `371ab09` — bounded optimizer pass trace metadata.
- `e45b759` — analysis-only CBE feature extraction for calibration outputs.

Current branch head when this charter was rewritten: `208b801` (`docs: compact
layered optimization loop plan`). Treat this as documentation context, not as
fresh performance evidence.

Latest durable records:

- accepted outcomes: `harness/artifacts/optimization-results.md`;
- deferred/negative outcomes: `harness/artifacts/optimization-negative-results.md`;
- rolling candidate input: `harness/artifacts/optimization-backlog.md`.

Recent useful artifacts named by the backlog include:

- `harness/artifacts/sweeps/promshim-optimization-foundation-7d-sparse/manifest.json`;
- `harness/artifacts/sweeps/bench-clickhouse-proof-signature-smoke/artifact-summary.json`;
- `harness/artifacts/sweeps/ir-rewrite-trace-budget-smoke-posttiming/artifact-summary.json`;
- regenerated `.agents/cost-routing-calibration.json` and `.pi/cost-routing-calibration.md` when present.

These artifacts seed ranking and proof design. Do not use them as post-change
acceptance evidence for new code.

## Active context window

Keep only the current champion, the next 1–3 hypotheses, and the last 3–5
attempt summaries in this charter. The rolling backlog may contain more ideas,
but it is not a finite scope boundary.

Current active hypotheses:

1. `ir-semantic-dependency-classifier` — explain-only classifier facts and
   rejection reasons should reproduce current allow/reject decisions before any
   behavior broadening.
2. `settings-query-condition-cache-profile` — scoped cache-profile experiment
   only after a cold/warm baseline separates query condition cache effects from
   OS page cache, mark cache, result cache, and benchmark ordering.
3. `local-rolling-range-rollups` — split into exact float-only local range
   function proof before considering any counter/extrapolation, quantile,
   histogram, offset, `@`, subquery, sparse, or stale behavior.

Last attempt summaries:

- accepted `bench-clickhouse-proof-signature`: benchmark artifacts can now report
  matched/missing/ambiguous ClickHouse proof signatures.
- accepted `ir-rewrite-trace-budget`: explain traces include bounded pass cost
  and fingerprint metadata.
- accepted `cbe-ir-feature-extraction`: calibration outputs include non-serving
  feature medians while recommendations remain unchanged.
- deferred `native-prewhere-pruning-audit`: sampled shapes already showed active
  primary-key/prewhere pruning, so manual PREWHERE rewrite had no proven gap.

## Replenishment rules

After every decision:

1. Update the attempt note with evidence, decision, and retry condition.
2. Update the accepted or negative result ledger.
3. Re-rank only the next small window using current evidence.
4. If the backlog is empty or stale, mine new hypotheses from:
   - benchmark proof signatures and memory summaries;
   - ClickHouse query-log/ProfileEvents gaps;
   - optimizer trace skip/no-op metadata;
   - CBE calibration feature medians and false/unknown decisions;
   - compliance failures or visible native-mode gaps;
   - `.pi/feynman/outputs/promshim-optimization-ideas.md` as seed material only.
5. Continue with the next measured attempt instead of stopping.

## Attempt note template

```markdown
# <candidate-id>

- Selected: <date>
- Git revision before edits: <sha>
- Hypothesis:
- Primary signal, units, and expected direction:
- Guardrails:
- Baseline command/artifact:
- Post-change command/artifact:
- Validation commands:
- Rollback path:

## Decision

- Status: accepted | rejected | deferred | split
- Evidence summary:
- Result ledger updated:
- Commit, if any:
- Retry condition:
```

## Compaction policy

This charter should stay small enough for a fresh agent to read quickly.

Compaction trigger: every 5 attempts, or whenever this file grows beyond roughly
400 lines.

Compaction action:

1. Move old detailed attempt summaries to append-only archive records at:

   ```text
   .pi/optimization-loops/layered-optimization-recursive/attempt-archive.ndjson
   ```

2. Keep only the current champion snapshot, 1–3 active hypotheses, and last 3–5
   attempt summaries here.
3. Add one short compaction event with timestamp and archive path.
4. Do not copy raw benchmark JSON, query logs, or long command output into this
   charter.

Suggested helper shape if compaction automation is added later:

```bash
./scripts/loop-compact.sh \
  .pi/plans/layered-optimization-iteration/README.md \
  .pi/optimization-loops/layered-optimization-recursive/attempt-archive.ndjson \
  --keep 5
```

## Commit policy

Do not commit, push, open PRs, or publish artifacts unless the user has granted
permission for that action.

When commit permission is active:

- commit accepted improvements with code, tests/docs, and artifact references in
  the same coherent change;
- include the baseline, accepted artifact, measured signal, scope, rollback, and
  validation in the commit body for non-trivial changes;
- commit rejected/reverted attempts when the negative evidence is durable enough
  to prevent repeated work;
- keep unrelated cleanup and behavior changes separate when that improves review.

## Stop and checkpoint rules

The loop stops only for:

- an explicit user stop or scope change;
- destructive, external, or costly action that needs approval;
- missing credentials, infrastructure, data, or tooling that blocks the next
  safe measurement;
- correctness ambiguity that needs a product decision;
- a workspace safety issue that cannot be isolated from unrelated user changes.

Do not stop merely because a plan section, numbered file, or backlog row is
exhausted. Replenish the rolling window and continue.

## Retired numbered files

The numbered files in this directory are compatibility pointers for old links.
Do not expand them into a second plan. If a decision rule needs to change, update
this README and leave the shard as a pointer.
