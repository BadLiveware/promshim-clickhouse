# Experiment design

## Purpose

Turn the selected candidate into the smallest safe experiment that can prove or
disprove its expected signal. The experiment may be code, configuration, a
settings-profile comparison, a reference-profile comparison, or a calibration
change, but it must test one hypothesis at a time.

## Prerequisites

- `harness/artifacts/optimization-backlog.md` has exactly one selected
  candidate.
- `harness/artifacts/optimization-iterations/<candidate-id>/notes.md` exists
  with hypothesis and expected signal.
- The relevant playbook has been read:
  - `running-sweep` for sweeps or artifact inspection;
  - `measuring-ch-optimizations` for SQL/ClickHouse performance claims;
  - `running-compliance` for compliance runs or failures.

## Work items

### 1. Define the proof boundary

**Goal:** Make the experiment small enough to decide.

**Steps:**

Update the iteration note with:

- the single hypothesis being tested;
- the owning layer;
- the query family and rows included;
- excluded related ideas;
- research source and caveats when the candidate came from
  `06-research-idea-seed.md`;
- baseline artifact path;
- post-change artifact path to create;
- exact expected signal and minimum meaningful movement;
- telemetry caveats or adjacent-system assumptions that must be validated before
  treating the idea as applicable to promshim;
- correctness risks and tests; and
- rollback path if the experiment is accepted, including whether existing broad
  modes are sufficient or whether a dedicated config gate is justified.

Examples of acceptable proof boundaries:

| Candidate | Good boundary | Bad boundary |
|---|---|---|
| Native expression reuse | One repeated expression shape with EXPLAIN/ProfileEvents proof. | General native CSE for all binary expressions. |
| Session setting | One allowlisted setting in one named profile with version behavior. | Enable several ClickHouse settings at once. |
| CBE family serving | One family gate with shadow, negative prefer, and warmed prefer artifacts. | Turn on local serving for all families with good p50. |
| Local executor reuse | One request-scoped memoization shape with copy-safety tests. | Cache all local execution results globally. |

**Acceptance criteria:**

- The experiment can fail without requiring a broad revert.
- The expected signal is measurable with existing tools or a clearly specified
  small addition to those tools.

### 2. Capture or verify the baseline

**Goal:** Ensure the before state is comparable to the after state.

**Steps:**

If a baseline already exists, verify its axes:

```bash
jq '.axes, .benchmarkStack.git.revision, .benchmarkStack.promshim.imageId' \
  harness/artifacts/sweeps/<baseline>/manifest.json
```

If a fresh baseline is needed, run a focused sweep such as:

```bash
./scripts/run-sweep.sh \
  --name <candidate-id>-baseline \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer,off \
  --routing-policies strict \
  --corpus-set optimization \
  --memory summary
```

For SQL-shape candidates, capture focused explain evidence:

```bash
./scripts/ch-explain.sh '<promql>' \
  --mode instant \
  --out-dir harness/artifacts/ch-explain/<candidate-id>-baseline \
  --log-comment optimization:<candidate-id>:baseline \
  --native-mode prefer \
  --routing-policy strict
```

Use `--mode range` for query-range candidates.

**Acceptance criteria:**

- Baseline and planned post-change artifacts use matching profile, density,
  corpus, transport, settings profile, and reference profile unless the
  experiment intentionally changes one of those dimensions.
- Memory summaries have zero missing log comments for claims that depend on
  query-log attribution.

### 3. Pick the minimum implementation form

**Goal:** Prefer the cheapest reversible test that gives a valid signal.

**Experiment forms:**

| Form | Use when | Completion condition |
|---|---|---|
| No-code settings/profile comparison | Testing ClickHouse reference or session settings. | Paired artifacts differ only in the intended profile axis. |
| Instrumentation-only change | The expected signal is not currently observable. | Artifacts expose the needed bounded field without changing serving. |
| Code change with rollback path | Testing renderer, IR, local executor, or CBE logic. | Existing broad mode, normal revert, profile/family gate, or a dedicated feature/env gate can restore previous behavior; choose a dedicated gate only when risk or operational needs justify it. |
| Calibration-only regeneration | Logic already changed and artifact recommendations need refresh. | Generated calibration reflects the intended recommendation and tests cover rule behavior. |
| Prototype branch change | Risk is high or proof is uncertain. | Clear accept/reject evidence exists before hardening. |

**Acceptance criteria:**

- The chosen form changes only what is necessary to test the selected
  hypothesis.
- Fundamental improvements are preferred over narrow special cases when they have
  comparable proof cost and correctness risk.
- Served behavior remains strict/reference unless the selected candidate is a
  CBE serving experiment with the required validation controls.
- The rollback design explains why existing broad controls are enough or why a
  per-optimization config gate is needed.

### 4. Define validation before editing

**Goal:** Prevent post-hoc acceptance criteria.

**Validation matrix:**

| Layer | Required validation |
|---|---|
| ClickHouse reference profile | paired sweeps, manifest profile labels, query-log/ProfileEvents signal, docs scan. |
| Session settings | settings profile tests, version/skip behavior, explain/query-log visibility, paired sweeps. |
| IR/native SQL | unit tests, renderer tests, focused explain before/after, compliance or targeted differential checks. |
| CBE | calibrator tests, regenerated calibration, shadow/negative/warmed prefer artifacts, safe fallback checks. |
| Tier 1 delegation | classifier tests, compliance/differential checks, explain fallback visibility. |
| Tier 3/4 local/subtree | local executor tests, copy/mutation safety where caching is involved, benchmark round-trip/transfer evidence. |

Always include:

```bash
git diff --check
```

**Acceptance criteria:**

- Validation commands and expected signals are written into the iteration note
  before implementation starts.
- Any validation that cannot be run is recorded as a blocker or explicit gap.

## Exit criteria

- The selected candidate has a narrow proof boundary.
- Baseline evidence exists or the exact baseline command is ready.
- The experiment form, validation commands, expected signal, and rollback path
  are documented before implementation.
