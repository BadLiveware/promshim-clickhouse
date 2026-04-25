# 02. IR logical optimizer

## Purpose and scope

Use the generalized tier-2 IR as the main place for conservative,
semantics-preserving PromQL optimization. The goal is to make optimization work
less ad hoc than renderer-only SQL tweaks and to give CBE better candidates,
estimates, and safety metadata.

This slice should start with low-risk IR transformations and metadata derivation.
High-risk semantic rewrites, especially around vector matching, staleness,
histograms, and extrapolation, remain out of scope until the evidence loop is
strong enough.

## Prerequisites

- Stage 01 query-family labels, IR invariants, explain contracts, and baseline
  artifacts are available.
- Rewrite passes can expose preconditions, applied/skipped status, and preserved
  invariants in explain/debug output.
- CBE can consume candidate metadata or has a planned field for doing so.

## Affected areas

- PromQL AST to IR lowering.
- IR annotations and semantic facts.
- IR rewrite pass framework.
- Candidate generation inputs.
- Explain output and tests for optimized IR.

## Requirements

- Rewrites must be semantics-preserving within declared preconditions.
- Rewrites must be deterministic so before/after artifacts are reviewable.
- Rewrites must expose why they ran or skipped.
- Rewrites must not silently expand supported PromQL semantics.
- Rewrites should improve candidate quality even when SQL rendering does not yet
  exploit every annotation.
- External optimizer examples may inform structure, but PromQL semantics and the
  local IR contract decide what is valid.

## Implementation tasks

### 1. Add a pass framework for IR optimization

- [ ] Compare pass organization patterns from `~/code/external/datafusion` and
  `~/code/external/calcite`, especially rule preconditions, pass ordering,
  statistics propagation, physical planning boundaries, and explain output.
- [ ] Adapt only the patterns that fit PromQL's value kinds, label semantics,
  time/range behavior, and CBE candidate model.
- [ ] Define pass ordering and pass dependencies explicitly.
- [ ] Require each pass to declare:
  - name;
  - query families it may affect;
  - preconditions;
  - semantic invariants preserved;
  - metadata produced;
  - expected measurement signal.
- [ ] Add explain/debug output for pass application and skip reasons.
- [ ] Keep a way to disable optimized IR for rollback or differential testing.

### 2. Semantic normalization passes

Start with transformations that reduce surface-area differences without changing
execution meaning.

- [ ] Normalize matcher ordering and selector fingerprints.
- [ ] Canonicalize aggregation grouping metadata.
- [ ] Normalize scalar constants and scalar-only subtrees where safe.
- [ ] Normalize label-operation metadata without dropping labels required by
  later vector matching or output.
- [ ] Represent instant/range/scalar requirements explicitly and uniformly.

Expected signals:

- more stable fingerprints;
- cleaner candidate grouping;
- fewer duplicate selector/candidate entries;
- no change in results.

### 3. Time-bound derivation

- [ ] Derive the exact selector time interval required by each query shape,
  including query start/end, step, range windows, lookback, and any required
  Prometheus compatibility slop.
- [ ] Push derived time bounds to selector nodes as metadata.
- [ ] Emit the derived bounds in explain output.
- [ ] Keep conservative bounds when semantics are uncertain.

Expected signals:

- reduced `SelectedRows`, `SelectedBytes`, or `ReadCompressedBytes` for queries
  where current SQL scans wider intervals than necessary;
- unchanged result rows and values;
- clearer CBE sample-count estimates.

### 4. Selector deduplication metadata

- [ ] Fingerprint equivalent selectors and range selectors after normalization.
- [ ] Mark repeated selector scans that can share a source when semantics allow.
- [ ] Distinguish exact reuse from reuse blocked by different time ranges,
  offsets, lookback requirements, or label requirements.
- [ ] Feed reuse metadata to candidate generation without requiring immediate SQL
  CTE emission.

Expected signals once exploited:

- fewer ClickHouse round trips or fewer duplicate SQL subqueries;
- lower `SelectedRows`/`FunctionExecute` for repeated-subexpression families;
- stable output.

### 5. Projection-pruning metadata

- [ ] Derive labels required by each downstream operator.
- [ ] Preserve labels required for output, grouping, vector matching, and
  correctness-sensitive behavior.
- [ ] Mark labels/columns that native SQL or local transfer may omit.
- [ ] Keep pruning disabled for ambiguous vector matching or label manipulation
  cases.

Expected signals once exploited:

- lower transferred bytes;
- smaller intermediate rows;
- lower local memory for aggregation and joins;
- no output label regressions.

### 6. Conservative simplification passes

- [ ] Fold scalar-only constant expressions when Prometheus semantics are clear.
- [ ] Detect impossible matcher sets and represent empty results explicitly.
- [ ] Remove redundant conversions or wrappers only when value kind and labels are
  unchanged.
- [ ] Do not rewrite aggregation order, binary vector matching, histogram
  operations, staleness handling, or rollup extrapolation in this stage unless a
  focused correctness task is approved.

## Validation tasks

Unit and differential checks:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
```

Baseline and optimized-mode sweeps once an optimized-IR gate exists:

```bash
./scripts/run-sweep.sh \
  --name post-cbe-ir-baseline-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary

./scripts/run-sweep.sh \
  --name post-cbe-ir-optimized-7d-sparse \
  --profile 7d \
  --density sparse \
  --seed reuse \
  --skip-compliance \
  --shim-modes prefer \
  --corpus-set native \
  --memory summary
```

Focused optimization claims must use the signals from
`.pi/skills/measuring-ch-optimizations/SKILL.md`:

- `EXPLAIN SYNTAX` to distinguish cosmetic SQL from executor-visible changes;
- `SelectedRows`/`SelectedBytes`/`ReadCompressedBytes` for pruning;
- `FunctionExecute` and `ArrayMap`-family counters for expression work;
- `MemoryTrackerUsage` for memory claims;
- strategy/candidate fields to detect silent fallback.

Compliance must remain clean. Do not add expected-failure entries for optimizer
regressions.

## Exit criteria

- [ ] IR rewrite pass framework exists with explainable pass results.
- [ ] Low-risk normalization is deterministic and tested.
- [ ] Time-bound metadata is available to candidate generation and explain.
- [ ] Selector reuse and projection-pruning metadata are available even if not
  fully exploited by SQL rendering yet.
- [ ] Conservative simplifications have focused tests.
- [ ] Optimized IR can be disabled for rollback/differential testing.
- [ ] No unrelated semantic coverage was added.

## Handoff to next file

After this slice, stage 04 can exploit IR metadata for query-family optimization.
Stage 03 can proceed independently to define scoped ClickHouse execution
profiles and safety settings.
