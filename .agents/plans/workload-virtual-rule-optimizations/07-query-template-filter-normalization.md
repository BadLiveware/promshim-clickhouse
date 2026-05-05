# Phase 7 — Query-template filter normalization

## Purpose

Normalize the common dashboard variable filters and match-all predicates that
appear across the extracted dashboard corpus. This phase is lower risk and broad
in occurrence, but it should follow the structural phases because it mostly
amplifies those optimizations by moving filters to the right place.

## Evidence and priority

- 402 dashboard queries contain regex matchers.
- 112 dashboard queries contain negative matchers.
- Many selectors use template forms such as:
  - `cluster=~"$cluster"`
  - `namespace=~"$namespace"`
  - `instance=~"$instance"`
  - `job=~"$job"`
- Workload queries also include match-all or near-match-all filters such as
  `workload_type=~".*"`.

This is high occurrence but not always high badness by itself. Its main value is
preventing no-op or common filters from duplicating around virtual-rule and join
expansions.

## Implementation tasks

### 1. Normalize match-all regex predicates

Work:

- Treat proven match-all regexes as no-op filters when they do not affect metric
  name selection or absent-label semantics.
- Continue preserving negative matchers and regexes that can distinguish absent
  labels.
- Add tests for `=~".*"`, `=~".+"`, empty regexes, and absent-label behavior.

Acceptance criteria:

- No-op predicates do not generate extra `and on(...)` scaffolding or repeated
  SQL filters.
- Non-no-op regexes still filter correctly.

Validation:

- `go test ./internal/promshim/rules ./internal/promshim/native/renderer`

### 2. Push dashboard filters through label-preserving operators

Work:

- Extend the existing safe matcher pushdown table only when label preservation is
  proven for the operator/function.
- Prioritize labels used throughout dashboards: `cluster`, `namespace`, `pod`,
  `node`, `instance`, `job`.
- Keep destination labels of `label_replace`/`label_join` blocked unless the
  matcher applies before the mutation.

Acceptance criteria:

- Representative dashboard filters reach leaf selectors in explain SQL.
- Unsafe filters are retained as post-expression predicates with a skip reason.

Validation:

- Focused rule expansion tests.
- Live explain for workload, networking, and CoreDNS dashboard queries.

### 3. Surface filter-placement diagnostics

Work:

- Add explain metadata that identifies filters pushed to leaves, skipped as
  no-op, or retained as post-filters.
- Include label names and reasons, not full potentially large regex values unless
  already present in query output.

Acceptance criteria:

- Future SQL blowup investigations can see whether template filters were a
  duplication factor.

## Risks

- PromQL matcher semantics around absent labels are subtle, especially for regex
  and negative matchers.
- A match-all regex may still matter if it forces label existence in a context;
  tests must cover this before skipping anything beyond the already proven safe
  cases.

## Exit criteria

- Common dashboard filters are placed as close to selectors as semantics allow.
- No-op filters no longer contribute to large rendered SQL.
