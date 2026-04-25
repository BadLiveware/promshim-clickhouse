# Measurement and decision

## Purpose

Compare baseline and post-change evidence for one selected candidate, decide
whether the experiment worked, and record enough detail to guide the next ranking
pass.

## Prerequisites

- The experiment design names baseline and post-change artifact paths.
- The expected signal and minimum meaningful movement are written before
  measurement.
- Research-derived candidates name the seed source and the caveat or
  adjacent-system assumption that measurement must confirm or retire.
- The benchmark stack is quiet; avoid ad-hoc `curl`, `docker exec`, or direct
  ClickHouse queries during query-log-sensitive runs.

## Work items

### 1. Capture post-change artifacts under matching axes

**Goal:** Make before/after comparison attributable.

**Steps:**

For sweep-based experiments, run the matching post-change command. Example:

```bash
./scripts/run-sweep.sh \
  --name <candidate-id>-post \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer,off \
  --routing-policies strict \
  --corpus-set optimization \
  --memory summary
```

For CBE serving experiments, use the three artifacts documented in
`docs/optimization-rollout.md`:

```bash
./scripts/run-sweep.sh \
  --name <candidate-id>-shadow-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_shadow \
  --warmup-routing-policies cost_shadow \
  --corpus-set optimization --memory summary

./scripts/run-sweep.sh \
  --name <candidate-id>-prefer-negative-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --warmup-routing-policies cost_prefer \
  --cost-routing-local-families <family> \
  --corpus-set optimization --memory summary

./scripts/run-sweep.sh \
  --name <candidate-id>-prefer-warmed-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer \
  --routing-policies strict,cost_prefer \
  --warmup-routing-policies cost_shadow \
  --cost-routing-local-families <family> \
  --corpus-set optimization --memory summary
```

For focused SQL experiments, run matching explain capture:

```bash
./scripts/ch-explain.sh '<promql>' \
  --mode instant \
  --out-dir harness/artifacts/ch-explain/<candidate-id>-post \
  --log-comment optimization:<candidate-id>:post \
  --native-mode prefer \
  --routing-policy strict
```

**Acceptance criteria:**

- Baseline and post-change artifacts have matching axes unless the experiment is
  specifically about that axis.
- `benchmarkStack.git.revision` and dirty state are recorded for each sweep.
- Memory summaries have zero missing log comments for query-log-sensitive claims.

### 2. Render comparison summaries

**Goal:** Make the decision inspectable without manually opening every report.

**Steps:**

For each sweep artifact:

```bash
./scripts/bench-artifact-summary.sh harness/artifacts/sweeps/<run-name> \
  > harness/artifacts/sweeps/<run-name>/artifact-summary.json
./scripts/bench-matrix.sh \
  --sweep harness/artifacts/sweeps/<run-name>/manifest.json \
  --per-query \
  > harness/artifacts/sweeps/<run-name>/matrix-per-query.txt
```

For focused explain artifacts, write a short note in the post-change directory
that includes:

- baseline path;
- post-change path;
- expected signal;
- observed counters or plan differences;
- result of the decision; and
- next action.

**Acceptance criteria:**

- The iteration note links to rendered summaries.
- The expected signal is visible in artifact summaries, matrices, explain files,
  or query-log JSONL.

### 3. Decide accept, reject, defer, or split

**Goal:** Turn measurement into a clear action.

**Decision table:**

| Decision | Criteria | Required follow-up |
|---|---|---|
| Accept | Expected non-p50 signal moved in the right direction, correctness validation passed, and rollback path is known. | Harden change, add tests/docs/calibration, run broader validation. |
| Reject | Required signal did not move, strategy/candidate regressed, correctness failed, or risk exceeds benefit. | Revert or discard experiment, record rejection reason, update backlog. |
| Defer | Evidence is inconclusive because of missing data, noisy stack, missing instrumentation, or unavailable profile. | Add instrumentation or baseline task; do not claim a win. |
| Split | Candidate combined multiple effects and result cannot be attributed. | Create narrower candidates in the backlog. |

Do not accept when:

- only p50 moved;
- query-log attribution is missing for the claimed signal;
- ClickHouse `EXPLAIN` shows the same executable shape for a SQL-shape claim and
  ProfileEvents do not move;
- strategy changes hide the target path;
- compliance introduces a new unapproved diff; or
- rollback is unavailable for a served behavior change.

**Acceptance criteria:**

- `harness/artifacts/optimization-iterations/<candidate-id>/notes.md` decision
  is no longer `pending`.
- Decision includes observed evidence and exact artifact paths.
- Backlog row is updated with `accepted`, `rejected`, `deferred`, or `split`.

### 4. Preserve failure knowledge

**Goal:** Make losing experiments useful.

**Steps for rejected experiments:**

- Keep the iteration note and artifact paths.
- State the failed expected signal in one sentence.
- If the idea may become valid under different data or ClickHouse version, state
  the exact condition required before retry.
- If the idea is unsafe, mark it `reject` in the backlog with the safety reason.
- For research-seed ideas, preserve whether the result invalidates the whole idea
  or only the current adaptation, data profile, ClickHouse version, or proof
  method.

**Acceptance criteria:**

- Future ranking passes can avoid repeating the same failed experiment without
  new evidence.
- Rejected artifacts are not deleted unless they contain secrets or invalid data.

## Exit criteria

- Post-change evidence is captured and rendered.
- The iteration has an explicit decision with artifact paths.
- The backlog reflects the decision.
- Accepted candidates are ready for hardening; rejected/deferred/split candidates
  return control to ranking.
