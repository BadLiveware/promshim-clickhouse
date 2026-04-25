# Hardening, commit, and repeat

## Purpose

Turn an accepted experiment into a reviewable optimization pass, preserve its
measurements, commit it with enough explanation for future readers, and then
restart ranking with both positive and negative results included.

An optimization pass is accepted only after the expected non-p50 signal is
observed and correctness validation passes. Rejected or deferred experiments do
not become optimization passes, but they still update the negative-results log.

## Prerequisites

- `04-measurement-decision.md` recorded `Accept` for the candidate.
- Baseline and post-change artifacts exist under matching axes.
- The iteration note contains the observed signal, correctness risks, rollback
  path, and whether the accepted change is fundamental or specific.

## Required records

Each accepted optimization pass must leave these records:

```text
harness/artifacts/optimization-iterations/<candidate-id>/notes.md
harness/artifacts/optimization-results.md
```

Rejected, deferred, and split experiments must update:

```text
harness/artifacts/optimization-negative-results.md
harness/artifacts/optimization-backlog.md
```

Research-seed ideas that are merely parked in `06-research-idea-seed.md` do not
need negative-result rows until an experiment is run or an explicit safety
decision rejects a default path.

## Work items

### 1. Harden the accepted change

**Goal:** Move from experiment to maintainable implementation.

**Steps:**

1. Remove temporary instrumentation that is not part of the durable evidence or
   explain/artifact contract.
2. Keep durable instrumentation when it helps future measurement and uses
   bounded names/enum values.
3. Add or update focused tests for:
   - semantic preconditions;
   - skipped/rejected shapes;
   - rollback gate or profile behavior;
   - generated SQL or candidate metadata when relevant; and
   - calibration recommendation logic when CBE inputs changed.
4. Add or update rollback controls when the existing rollback path is not
   sufficient:
   - routing policy;
   - native lowering mode;
   - settings profile;
   - CBE family gate;
   - broad optimizer disable flag; or
   - `PROM_SHIM_DISABLE_*` flag for risky/narrow serving changes.
5. Do not add per-optimization configuration by default. Prefer no new knob for
   fundamental optimizations that are safe by construction, well tested, and
   covered by broader rollback modes or normal revert.
6. Add targeted comments only where a non-obvious compatibility, safety, or
   ClickHouse-version constraint needs to be preserved.

**Acceptance criteria:**

- The change is one coherent semantic unit or split into coherent units.
- Rollback behavior is tested, explicitly covered by an existing mode/profile
  test, or documented as normal revert for a safe fundamental optimization that
  does not need a runtime knob.
- No temporary experiment-only code remains in serving paths.

### 2. Run final measurement for the accepted pass

**Goal:** Preserve accepted-pass measurements after hardening, not only prototype
measurements.

**Steps:**

Run the same measurement pattern used for the decision after final code cleanup.
For a sweep-based accepted pass:

```bash
./scripts/run-sweep.sh \
  --name <candidate-id>-accepted \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer,off \
  --routing-policies strict \
  --corpus-set optimization \
  --memory summary

./scripts/bench-artifact-summary.sh harness/artifacts/sweeps/<candidate-id>-accepted \
  > harness/artifacts/sweeps/<candidate-id>-accepted/artifact-summary.json
./scripts/bench-matrix.sh \
  --sweep harness/artifacts/sweeps/<candidate-id>-accepted/manifest.json \
  --per-query \
  > harness/artifacts/sweeps/<candidate-id>-accepted/matrix-per-query.txt
```

For focused SQL-shape accepted passes, capture final explain evidence:

```bash
./scripts/ch-explain.sh '<promql>' \
  --mode instant \
  --out-dir harness/artifacts/ch-explain/<candidate-id>-accepted \
  --log-comment optimization:<candidate-id>:accepted \
  --native-mode prefer \
  --routing-policy strict
```

Use range mode and matching policies when the selected candidate requires them.

**Acceptance criteria:**

- Final accepted artifact is separate from prototype/post-change artifacts.
- Accepted artifact manifests record the git revision and dirty state.
- The accepted artifact still shows the expected signal after cleanup.
- If the final signal disappears, change the decision back to `defer` or
  `reject` and record why.

### 3. Update accepted-results ledger

**Goal:** Make accepted optimization history easy to scan.

**Artifact:** `harness/artifacts/optimization-results.md`

Append one row per accepted pass:

| Column | Required content |
|---|---|
| Candidate ID | Stable candidate name. |
| Date | Date accepted. |
| Commit | Commit hash after commit is created; use `pending` before commit. |
| Layer | Owning layer. |
| Query family | Cost family/corpus category. |
| Change | One-sentence implementation summary. |
| Scope | `fundamental` for reusable cross-family/layer improvements or `specific` for narrow query/family optimizations. |
| Baseline artifact | Path. |
| Accepted artifact | Path. |
| Measured impact | Required signal before/after, plus p50 only as secondary context. |
| Rollback | Env var/profile/routing gate, broader mode, normal revert, or reason no per-optimization knob is needed. |
| Validation | Commands run. |
| Follow-up | Next candidate or monitoring note, including related research-seed rows if the accepted pass unlocks or invalidates them. |

**Acceptance criteria:**

- The row includes the non-p50 signal that justified acceptance.
- Artifact paths are stable and do not point to overwritten temporary files.
- Commit is updated from `pending` to the actual hash after committing.

### 4. Commit the accepted pass with measurement explanation

**Goal:** Preserve the why, evidence, and rollback path in git history.

**Commit boundary rules:**

- Commit one accepted optimization pass as one semantic commit when it is small
  and coherent.
- Split preparatory plumbing, docs-only guidance, calibration-only updates, and
  the optimization itself when separate review/revert would be clearer.
- Do not commit rejected experiments unless the rejection produced durable
  instrumentation, tests, docs, or backlog notes worth keeping.
- Stage only files for the current semantic unit.

**Commit message requirements:**

Use this structure:

```text
<type>: <actual optimization or support change>

Explain the query family and layer affected, what changed, and why the approach
is safe for Prometheus semantics.

Measurement:
- baseline: <artifact path>
- accepted: <artifact path>
- signal: <before> -> <after>
- scope: <fundamental or specific, with one sentence explaining why>

Rollback/validation:
- rollback: <env/profile/routing gate, broader mode, normal revert, or reason existing gate is sufficient>
- validation: <commands>
```

Examples of acceptable headers:

```text
feat: reuse repeated native rate subexpressions
feat: cache repeated local range expressions
chore: calibrate local serving for instant increase
chore: add benchmark ClickHouse condition-cache profile
```

Avoid source-only headers such as:

```text
fix: address optimization feedback
chore: update plan
```

**Acceptance criteria:**

- Commit body names the optimization layer, measurement artifacts, observed
  signal, rollback, and validation.
- `git show --stat --oneline HEAD` describes one coherent unit.
- `git status --short` is clean or contains only intentionally deferred files.

### 5. Update negative-results ledger for failed attempts

**Goal:** Prevent repeated failed experiments.

**Artifact:** `harness/artifacts/optimization-negative-results.md`

Append one row for every rejected, deferred, or split experiment:

| Column | Required content |
|---|---|
| Candidate ID | Stable name. |
| Date | Decision date. |
| Layer | Owning layer. |
| Hypothesis | One sentence. |
| Baseline artifact | Path if captured. |
| Experiment artifact | Path if captured. |
| Decision | `rejected`, `deferred`, or `split`. |
| Reason | Specific failed signal, safety issue, attribution issue, or missing evidence. |
| Retry condition | Exact new evidence, data profile, ClickHouse version, instrumentation, or scope split needed before retry. |

**Acceptance criteria:**

- Every non-accepted experiment is represented in the ledger.
- The retry condition is concrete. Use `do not retry` only for unsafe ideas.
- Backlog rows link to the negative result instead of repeating the full detail.

### 6. Refresh calibration and docs when accepted change affects routing inputs

**Goal:** Keep CBE and docs aligned with accepted behavior.

**When required:**

Refresh calibration if the accepted pass changes:

- IR rewrite behavior;
- native SQL renderer shape;
- local executor/subtree performance;
- CBE recommendation logic;
- family labels;
- caps/confidence;
- benchmark corpus or fixture; or
- settings/reference profile dimensions.

**Commands:**

```bash
go run ./cmd/promshim-routing-calibrate \
  --sweep harness/artifacts/sweeps/<accepted-or-calibration-source>/manifest.json \
  --out-json .pi/cost-routing-calibration.json \
  --out-md .pi/cost-routing-calibration.md
python3 -m json.tool .pi/cost-routing-calibration.json >/dev/null
```

For served family changes, also run the CBE validation pattern from
`docs/optimization-rollout.md`.

**Acceptance criteria:**

- Calibration artifacts are regenerated from named sweeps, not hand-edited.
- Docs explain operational/rollback behavior if users or operators need to know.
- Docs avoid internal execution bookkeeping terms.

### 7. Run completion validation for the pass

**Goal:** Verify correctness and review readiness before marking the pass done.

**Commands:**

Choose the relevant focused tests, then run the broader checks that apply:

```bash
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh scripts/ch-explain.sh scripts/bench-artifact-summary.sh
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench ./cmd/promshim-routing-calibrate/...
python3 -m json.tool .pi/cost-routing-calibration.json >/dev/null
./scripts/run-sweep.sh --name <candidate-id>-compliance --skip-bench
rg -n "stage|checklist|post-cbe|Post-CBE|\.agents|\.ralph|Ralph|loop" docs README.md
git diff --check
```

**Acceptance criteria:**

- Validation results are recorded in the iteration note and commit message.
- Any skipped validation has a reason and risk assessment.
- Compliance does not add expected failures for shim bugs.

### 8. Restart ranking

**Goal:** Continue optimization from updated evidence.

**Steps:**

1. Update `harness/artifacts/optimization-backlog.md`:
   - mark accepted candidate complete;
   - link accepted or negative result;
   - adjust scores for related candidates using new evidence;
   - add follow-up candidates discovered during the pass;
   - revisit `06-research-idea-seed.md` for newly supported seeds; and
   - park or down-rank seeds whose caveats were reinforced by the latest result.
2. Re-run the ranking process in `01-candidate-ranking.md`.
3. Select the next best candidate or stop if the next candidate requires a user
   decision, credentials, unavailable data, or broader product approval.

**Acceptance criteria:**

- The next selected candidate reflects both wins and negative results.
- No failed idea is re-selected unless its retry condition is satisfied.

## Exit criteria

- Accepted optimization pass has final measurement artifacts, results ledger row,
  tests, rollback path, validation, and explanatory commit.
- Rejected/deferred/split experiments have negative-results ledger rows and
  backlog updates.
- Ranking is ready to select the next best candidate across all layers.
