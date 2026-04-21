# Theme harness task list

Date: 2026-04-21
Source: `.pi/theme-harness-backlog.md`

## Active execution order

1. **Task #55 — Seal range-mode increase planner leak** _(completed)_
   - Goal: stop leaking raw ClickHouse `Function increase is not implemented` errors in range-mode theme queries.
   - Scope:
     - `draft_cand_0295_rate_family_aggregation_selector`
     - `draft_cand_0593_rate_family_aggregation_selector`
     - `draft_cand_0452_rate_family_selector`
   - Acceptance:
     - no raw ClickHouse `NOT_IMPLEMENTED` errors for these rows,
     - rows either pass or fail with a stable shim-side unsupported boundary.

2. **Task #54 — Fix malformed theme corpus success rows**
   - Goal: repair or reclassify invalid Prometheus queries currently counted as success-case failures.
   - Scope:
     - `draft_cand_0151_rate_family_aggregation_selector`
     - `draft_cand_0152_rate_family_vector_matching_aggregation_selector`
     - `draft_cand_0257_rate_family_vector_matching_aggregation_selector`
   - Acceptance:
     - no Prometheus parse errors remain for rows still marked expected-success.

3. **Task #56 — Fix matrix metric-name parity for range-function selectors**
   - Goal: remove the `__name__` label mismatch in `draft_cand_0011_range_function_selector`.
   - Acceptance:
     - theme diff disappears,
     - regression coverage exists for metric-name retention/drop behavior.

4. **Task #57 — Close remaining aggregation theme blockers** _(completed)_
   - Goal: resolve recurring unsupported theme failures by implementation or explicit product-boundary decisions.
   - Scope:
     - `vector(...)`
     - `round(...)`
     - nested aggregation operators
     - rate-family over subquery args
   - Acceptance:
     - rows either pass by implementation or are explicitly documented as intentional unsupported boundaries.

5. **Task #58 — Pass 1 local `increase` support** *(completed)*
   - `increase(...)` in the `rate-family` path now runs through a local planner/executor for instant and range mode.
   - `__name__` is dropped from function output and explicit unsupported boundary behavior is preserved for invalid forms.

6. **Task #59 — Implement `rate`/`irate` over subquery arguments** *(completed)*
   - Scope:
     - `draft_cand_0225_rate_family_subquery_aggregation_selector`
     - `draft_cand_0242_rate_family_subquery_aggregation_selector`
   - Implementation:
     - added local planning and execution for `rate(...)` and `irate(...)` when argument contains a subquery
     - analyzer and planner now accept these as supported `hard` forms
     - preserved deterministic output ordering and metric-name dropping semantics

7. **Task #60 — Finish remaining subquery rate-family support** *(completed)*
   - Child tasks completed:
     - `#61` `increase(... [subquery])`
     - `#62` `delta(... [subquery])`
     - `#63` `idelta(... [subquery])`
     - `#64` `changes(... [subquery])`
     - `#65` `deriv(... [subquery])`
     - `#66` validation and tracker sync
   - Result:
     - the previously deferred remaining subquery rate-family surfaces now have local support in both instant and range modes.

## Progress log

### Task #55 — completed

Implemented hardening for the `increase(...)` range leak:
- range-mode delegated planning now rejects `increase(...)` with a clean shim-side unsupported error before execution reaches ClickHouse,
- native aggregation pushdown also refuses delegated `increase(...)` range leaves.

Validation:
- `go test ./internal/promshim/...`
- `./scripts/run-harness.sh --theme rate-family`

Observed improvement:
- `rate-family` theme still has the same three affected rows failing as expected-success, but they now fail as clean `shim-unsupported` instead of leaking backend `shim-unimplemented`/ClickHouse `NOT_IMPLEMENTED` errors.

### Task #54 — completed

Reclassified malformed draft rows as explicit expected parse errors in the draft shortlist corpus:
- `draft_cand_0151_rate_family_aggregation_selector`
- `draft_cand_0152_rate_family_vector_matching_aggregation_selector`
- `draft_cand_0257_rate_family_vector_matching_aggregation_selector`

Implementation notes:
- added `expectedStatus: error`, `expectedErrorType: bad_data`, and specific `expectedErrorContains` fragments in `harness/corpus/draft-grafana-top-panel-shortlist.json`,
- re-split themed corpora with `python scripts/split-draft-harness-corpus-by-theme.py`.

Validation:
- `./scripts/run-harness.sh --theme vector-matching`
- `./scripts/run-harness.sh --theme label-mutation`

Observed improvement:
- `vector-matching` theme now reports `3 ok / 1 error` instead of parse-error noise,
- `label-mutation` theme is now fully green (`1 ok`).

### Task #56 — completed

Fixed metric-name parity for range-function outputs by dropping `__name__` from local range-function and `quantile_over_time` output metrics.

Validation:
- `go test ./internal/promshim/...`
- `./scripts/run-harness.sh --theme range-function`

Observed improvement:
- `range-function` theme is now fully green (`4 ok`).

### Task #58 — completed

Implemented Pass 1 local support for plain `increase(metric[range])` shapes.

Implementation notes:
- added a local `increase` logical/exec plan,
- delegated matrix-selector fetching still supplies the range-vector input,
- local `increase` now evaluates both instant and range queries,
- reset-aware delta accumulation is implemented for the current PoC scope,
- output metrics drop `__name__` to match Prometheus-style function output shape.

Validation:
- `go test ./internal/promshim/...`
- `./scripts/run-harness.sh --theme rate-family`
- `./scripts/run-harness.sh --theme range-selector`
- `./scripts/run-harness.sh --no-build`

Observed improvement:
- previously failing `increase(...)` theme rows now pass:
  - `draft_cand_0295_rate_family_aggregation_selector`
  - `draft_cand_0593_rate_family_aggregation_selector`
  - `draft_cand_0452_rate_family_selector`
- `rate-family` improved to `71 ok / 7 error`
- `range-selector` improved to `73 ok / 5 error`
- stable default corpus remains green (`86/86 ok`)

## Task #60 — completed remaining subquery rate-family support

Implementation decisions completed:
- added local support for the previously deferred subquery-argument forms:
  - `increase(...)`
  - `delta(...)`
  - `idelta(...)`
  - `changes(...)`
  - `deriv(...)`
- retained deterministic local-output handling across the new transforms:
  - metric-name dropping
  - stable ordering
  - explicit local explain plans
- the theme-covered subquery rate-family surface no longer depends on explicit unsupported boundaries.

Validation completed:
- `go test ./internal/promshim/... ./integration/promshim`
- `go test ./...`
- `./scripts/run-harness.sh --theme subquery`
- `./scripts/run-harness.sh --theme rate-family --no-build`
- `./scripts/run-harness.sh --all-themes --no-build`

Observed status:
- `subquery` theme: `3 ok`
- `rate-family` theme: `78 ok`
- all theme reports green in the full themed harness run.
