# Candidate ranking

## Purpose

Maintain a ranked backlog of optimization opportunities across all layers and
choose the next single experiment by expected value. This document is revisited
after every accepted, rejected, or deferred experiment.

## Inputs

Use the freshest available evidence from:

- sweep manifests under `harness/artifacts/sweeps/`;
- per-query matrices from `scripts/bench-matrix.sh --per-query`;
- artifact summaries from `scripts/bench-artifact-summary.sh`;
- focused `ch-explain.sh` captures;
- `.pi/cost-routing-calibration.md` and `.pi/cost-routing-calibration.json`;
- compliance reports and native gap reports;
- research idea seeds from `06-research-idea-seed.md`, backed by
  `.pi/feynman/outputs/promshim-optimization-ideas.md` and its provenance
  sidecar; and
- code-level knowledge from the relevant renderer, local executor, or settings
  profile files.

## Work items

### 1. Refresh evidence index

**Goal:** Know which artifacts and data profiles are safe to rank from.

**Steps:**

1. Run:

   ```bash
   ./scripts/run-sweep.sh --bench-status
   ```

2. List the most recent relevant sweeps:

   ```bash
   find harness/artifacts/sweeps -maxdepth 2 -name manifest.json -printf '%T@ %p\n' | sort -nr | head -20
   ```

3. For each candidate source sweep, inspect:

   ```bash
   jq '.axes, .benchmarkStack, .bench.status, .bench.memoryReports' \
     harness/artifacts/sweeps/<run-name>/manifest.json
   ```

4. If the current evidence is stale because code, settings profiles, corpus,
   ClickHouse profile, or CBE logic changed, schedule a fresh baseline before
   ranking implementation work.

**Acceptance criteria:**

- Ranking uses artifacts whose git revision/profile/density/settings context is
  known.
- Any stale artifact is marked as context only, not decision evidence.

### 2. Build or update the opportunity backlog

**Goal:** Compare opportunities across layers using the same scoring model.

Research-derived ideas enter this step as seeds, not as pre-approved work. Copy a
seed row into the live backlog only when current artifacts support a concrete
baseline or baseline-capture command. Re-score copied rows using current evidence
instead of preserving the seed scores mechanically.

**Artifact:**

Maintain the current backlog at:

```text
harness/artifacts/optimization-backlog.md
```

Each row must include:

| Column | Meaning |
|---|---|
| Candidate ID | Stable short name such as `rate-instant-local-cbe` or `selector-condition-cache`. |
| Query family | Cost family or corpus category. |
| Layer | One search surface from the README candidate-layer table. |
| Evidence source | Manifest, matrix, explain, compliance report, code path, or research seed plus current artifact. |
| Hypothesis | The expected reason this can improve behavior. |
| Expected signal | Required non-p50 signal. |
| Benefit score | 1 low, 2 medium, 3 high based on likely user/runtime impact. |
| Breadth score | 1 narrow query special case, 2 family-level improvement, 3 fundamental capability shared across families or layers. |
| Evidence readiness | 1 weak, 2 enough for experiment, 3 strong baseline already exists. |
| Correctness risk | 1 low, 2 medium, 3 high. Lower is better. |
| Implementation cost | 1 small, 2 medium, 3 large. Lower is better. |
| Rollbackability | 1 hard, 2 moderate, 3 easy. Higher is better. Per-optimization config is one way to improve this score, not a mandatory property. |
| Next action | `experiment`, `refresh-baseline`, `split`, `defer`, or `reject`. |
| Research source | Optional link to the research seed/final brief section when the row came from research. |
| Caveat | The main semantic, telemetry, cache-state, or adjacent-system adaptation caveat that must be resolved before acceptance. |

Use this ranking score to sort candidates:

```text
score = benefit + breadth + evidence_readiness + rollbackability - correctness_risk - implementation_cost
```

The score is a tie-breaker, not an automatic decision. Prefer a lower-scored
candidate when it has a clearer proof path or removes a blocker for later work.
When two candidates are otherwise close, choose the more fundamental one before a
narrow special case because it is more likely to improve future candidates and
reduce repeated layer-specific work.

**Acceptance criteria:**

- The backlog contains candidates from any layers that current evidence supports;
  it does not invent one candidate per layer.
- Every `experiment` row has a concrete expected signal.
- Every `refresh-baseline` row names the missing artifact or stale dimension.
- Every research-derived row links to its seed or final-brief section and states
  the main caveat that could invalidate the analogy or evidence.
- Every `reject` row states the evidence that makes it not worth retrying.

### 3. Select the next candidate

**Goal:** Choose exactly one candidate for the next experiment.

**Decision rules:**

Select the highest expected-value candidate that meets all of these:

- one safe experiment can prove or disprove it;
- relevant baseline data exists or can be captured cheaply;
- correctness risk has a validation path;
- rollback path is known if serving behavior could change; a dedicated flag is
  required only for risky/narrow changes where broader modes or normal revert are
  not sufficient; and
- implementation can be reviewed as one semantic change if accepted.

Do not select a candidate when:

- the only observed signal is p50 noise;
- it requires expanding compliance allowlists for a shim bug;
- it depends on unowned external deployment changes as a hidden default;
- it broadens served CBE without shadow and negative controls; or
- it combines multiple independent optimizations in one experiment.

**Acceptance criteria:**

- `harness/artifacts/optimization-backlog.md` marks one row as `selected`.
- The selected row names the exact baseline artifact to use or the command to
  capture it.
- All other high-scoring rows remain available for the next ranking pass.

### 4. Create an iteration record

**Goal:** Preserve the hypothesis and decision trail for the selected candidate.

**Artifact:**

Create:

```text
harness/artifacts/optimization-iterations/<candidate-id>/notes.md
```

Include:

- candidate ID;
- selected date and git revision;
- layer;
- query family and corpus rows;
- hypothesis;
- expected signal;
- semantic risks;
- research seed or final-brief source when applicable;
- baseline artifact path;
- experiment command or implementation area;
- rollback path if accepted;
- validation commands; and
- decision field initially set to `pending`.

**Acceptance criteria:**

- A future reviewer can understand why this candidate was selected without
  reading chat history.
- The note contains no raw tenant data, unbounded label values, or secrets.

## Exit criteria

- Current evidence has been inspected for freshness.
- The backlog is ranked using common criteria.
- One candidate is selected for the next experiment.
- The selected candidate has an iteration note with hypothesis, expected signal,
  baseline, validation, and rollback considerations.
