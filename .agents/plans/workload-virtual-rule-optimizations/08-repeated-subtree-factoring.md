# Phase 8 — Native repeated-subtree factoring

## Purpose

Evaluate native repeated-subtree factoring after the earlier structural
optimizations have removed the largest duplication sources. This phase is last
because factoring can reduce SQL text without reducing ClickHouse execution work,
and because earlier phases produce simpler, safer factoring candidates.

## Evidence and priority

- The live workload query improved from roughly 6.5 MiB to roughly 234 KiB after
  structural rule optimizations, but still contains repeated native branch
  groups.
- Metadata joins, active-status joins, histogram inputs, and selector variants
  can all create repeated subtrees.
- Factoring is potentially useful, but only if ClickHouse reuses or at least
  parses the factored shape more cheaply.

## Implementation tasks

### 1. Measure repeated rendered fragments after previous phases

Work:

- Add a diagnostic or offline inspection script that counts repeated rendered
  native fragments by normalized hash, size, and occurrence.
- Run it on representative workload, networking, resource quota, CoreDNS, and
  apiserver dashboard queries.

Acceptance criteria:

- The plan has a ranked list of repeated fragment families after phases 1-7.
- Tiny fragments and harmless aliases are filtered out of the ranking.

Validation:

- Script/unit tests if a reusable diagnostic is added.
- Manual evidence artifact for representative queries.

### 2. Prototype safe factoring forms

Candidate SQL forms:

- ClickHouse `WITH` aliases for scalar or table expressions where valid.
- Inline subquery definitions reused through explicit joins.
- Typed `sqlb` helper nodes for repeated fragments.

Acceptance criteria:

- Prototype compares parse behavior and execution behavior, not SQL length only.
- Profile evidence or query-log evidence shows whether ClickHouse reuses work or
  simply inlines the expression.

Validation:

- Focused renderer tests for emitted SQL if implemented.
- Live query comparison with ClickHouse profile/query-log evidence.

### 3. Implement only evidence-backed factoring

Work:

- If the prototype shows benefit, add the smallest renderer-local factoring path
  for the highest-value repeated fragment family.
- If not, record the negative result and leave diagnostics in place if useful.

Acceptance criteria:

- Factoring is guarded by shape and size thresholds.
- Generated aliases are stable, typed, and do not encode plan terminology.
- Existing golden/render tests are updated only for intentional SQL shape
  changes.

Validation:

- `go test ./internal/promshim/native/renderer ./internal/promshim/native`
- Live dashboard query explain/run.
- Benchmark/profile validation if claiming runtime improvement.

## Risks

- ClickHouse may inline CTE-like forms, reducing SQL size but not execution work.
- Factoring could make optimizer behavior worse for some queries.
- Broad SQL-string CSE can obscure semantics and make generated SQL harder to
  review.

## Exit criteria

- Repeated-subtree factoring is either implemented with evidence-backed benefit
  or explicitly rejected for this workload family with captured evidence.
