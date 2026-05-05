# Phase 2 — Selector-variant union for same-shape branches

**Status:** Completed (2026-05-03)

## Purpose

Collapse virtual-rule branches that have the same PromQL structure but differ
only by safe leaf selector matchers and static output labels. This directly
extends the phase 1 static-label union and targets the remaining repeated
subplans in workload-owner rule expansion.

## Evidence and priority

Highest priority future phase because:

- `namespace_workload_pod:kube_pod_owner:relabel` is the dominant dashboard rule
  reference: 98 refs across 5 dashboards.
- Several workload-owner rule branches are structurally similar:
  - `kube_pod_owner{owner_kind="DaemonSet"}` -> `workload_type="daemonset"`
  - `kube_pod_owner{owner_kind="StatefulSet"}` -> `workload_type="statefulset"`
  - `kube_pod_owner{owner_kind="Node"}` -> `workload_type="staticpod"`
  - related exact/empty `owner_kind` branches.
- Current live SQL is compact enough to run but still contains repeated branch
  groups and about 15 `UNION ALL` boundaries for the memory workload query.

## General shape

Candidate input:

```promql
label_replace(metric{kind="A", common="x"}, "out", "$1", "src", "(.*)")
or
label_replace(metric{kind="B", common="x"}, "out", "$1", "src", "(.*)")
```

Branch metadata:

| variant matcher | static labels |
|---|---|
| `kind="A"` | `type="a"` |
| `kind="B"` | `type="b"` |

Desired rendering direction:

- One broader selector/subplan for the shared expression shape.
- A branch table for exact variant matchers and static output labels.
- Static labels attached according to the branch row that matches the original
  selector values.

## Implementation tasks

### 1. Canonicalize branch expression shapes

Work:

- Build a canonical representation for candidate branches that preserves:
  - metric name
  - selector label names
  - matcher types
  - AST function/operator/aggregation structure
  - vector matching labels and cardinality
  - label mutation destinations and sources
  - offsets, timestamps, ranges, and subquery steps
- Replace only safe selector matcher values with placeholders.
- Keep static output labels outside the canonical child shape.

Acceptance criteria:

- Same-shape exact selector variants produce the same canonical key.
- Branches with different vector matching, aggregation grouping, offsets,
  ranges, label mutation destinations, or function arguments produce different
  keys.
- Tests include matching and non-matching canonicalization examples.

Validation:

- `go test ./internal/promshim/native/renderer`

### 2. Prove variant matcher safety

Safe starting cases:

- One or more exact-match selector variants on labels that still exist when the
  branch table is applied.
- Variant values are mutually exclusive within the grouped branch set.
- Static output-label keys are identical across grouped branches.
- No branch mutates a selector-variant label before branch attribution.

Fallback cases:

- Negative matchers.
- Broad or overlapping regexes.
- Different static label key sets.
- Dynamic label mutation of a variant label.
- Multiple branch rows that could match one output sample.

Acceptance criteria:

- Unsafe branches fall back to current static-label union rendering.
- Skip reasons are surfaced in explain diagnostics.
- Tests cover exact variants, overlapping/unsafe variants, and label mutation
  blockers.

Validation:

- `go test ./internal/promshim/native/renderer ./internal/promshim`

### 3. Render selector-variant branch tables

Work:

- Choose a ClickHouse SQL form for branch metadata, likely one of:
  - `ARRAY JOIN` of typed tuples
  - inline `UNION ALL` value table
  - typed `WITH` expression feeding a join/filter
- Ensure selector values and static labels are represented with explicit types.
- Ensure the branch table does not create duplicate output samples for one input
  series unless Prometheus semantics would also allow them.

Acceptance criteria:

- A focused same-shape workload-owner test renders fewer native subplans than
  the existing `UNION ALL` form.
- The known workload memory explain reduces SQL length or `UNION ALL` count
  beyond the phase 1 baseline, or reports precise skip reasons for each
  remaining branch group.
- Live memory and CPU workload queries return HTTP 200.

Validation:

- `go test ./internal/promshim/native/renderer`
- `go test ./internal/promshim/rules ./internal/promshim/native/renderer ./internal/promshim`
- Live query explain/run for memory and CPU workload queries.

## Risks

- Incorrect branch attribution could silently attach the wrong static labels.
- Overlapping matchers could create duplicate labelsets or hide ambiguity.
- Some SQL forms may shrink text but not reduce ClickHouse execution work.

## Exit criteria

- Same-shape selector-variant groups are collapsed with precise safety checks.
- Endpoint explainability captures both applied and skip decisions, including nested unsafe-overlap traces.
- The workload-owner family either improves beyond phase 1 or explains non-collapsible branches with precise skip reasons.
- Representative in-cluster `query_range_explain` checks show rendered size reductions on safe selector-variant trees.

## Verification snapshot (2026-05-03)

- Local and endpoint evidence confirms phase-2 behavior across safe and unsafe nested trees:
  - `owner_selector_variants`:
    - `strategy=native_sql`, `kind=aggregation`
    - `candidateBranches=2`, `collapsedRows=1`, `remainingGroups=2`, `mode=shared_selector_child`
    - `renderedSQL=3612`
  - `owner_selector_nested`:
    - `strategy=native_sql`, `kind=aggregation`
    - `candidateBranches=3`, `collapsedRows=2`, `remainingGroups=3`, `mode=shared_selector_child`
    - `renderedSQL=3705`
  - nested mixed-overlap query (`(A or B) or B`):
    - `strategy=native_sql`, `kind=binary`
    - decisions include `unsafe_selector_overlap` skip + `shared_selector_child` applied
    - `renderedSQL=6654`
- Transition to phase-3 readiness:
  - Phase-2 implementation is treated complete in this loop after full suite and endpoint evidence closure.
