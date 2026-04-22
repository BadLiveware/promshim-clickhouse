# Path 2 native SQL 100% compliance plan

## Goal

Move **Path 2 — native SQL lowering** from a selective subset to a **full PromQL-compliant execution path** backed by repo-owned ClickHouse SQL.

The main body of this plan is organized by **difficulty in descending order**, but the target is still **100% compliance** rather than selective usefulness. A secondary **usefulness-first execution view** is included later so the work can also be driven from practical impact if desired.

## What “100% compliance” means here

For this plan, Path 2 is considered complete only when all of the following are true:

1. Every PromQL operator/function/modifier that the repo’s vendored Prometheus parser accepts is either:
   - executed by Path 2, or
   - explicitly rejected only because Prometheus itself rejects the input.
2. Served results for supported expressions come from **repo-owned native SQL lowering**, not Path 3 local execution.
3. Path 2 matches Prometheus for:
   - instant queries
   - range queries
   - error classes
   - label/metric-name retention rules
   - staleness / lookback behavior
   - boundary inclusivity / exclusivity behavior
4. The current README matrix stops saying `subset`, `mostly yes`, or `no` for Path 2.
5. There are **no intentional keep-local exceptions** left for PromQL language support. That means the current `quantile_over_time` keep-local note must be retired if the 100% target is to be met.

Scope boundary:

- this plan targets full Path 2 compliance for the current ClickHouse `TimeSeries`-backed metrics model
- that includes scalar samples and classic histogram series (`_bucket`, `_sum`, `_count`)
- that does **not** include Prometheus native histogram sample support, because current `TimeSeries` / remote-write / remote-read handling does not store or round-trip native histogram payloads

## Important planning note

The current `README.md` matrix is too coarse to measure 100% precisely. “Aggregations”, “range/counter family”, and “histogram helper family” are umbrella buckets, not a compliance checklist.

So the first prerequisite is to replace the coarse family matrix with a **granular feature inventory** derived from:

- aggregation operators
- binary operators
- vector matching modes
- function list from the vendored Prometheus parser version
- modifiers (`offset`, `@`, subquery step/range)
- classic histogram helper semantics over the current `TimeSeries` storage model
- expected error behavior

Without that, the repo cannot honestly claim 100%.

## Current baseline from README

Path 2 currently stands at:

| Family | Current Path 2 status |
|---|---|
| Simple selectors | Yes |
| Aggregations | Yes, subset |
| Scalar/vector arithmetic | Yes, subset |
| Vector-vector joins | Yes, subset |
| Pointwise math/trig/date transforms | Mostly yes |
| Scalar/date builtins | Yes |
| `scalar(v)` | Yes |
| `info(...)` | Yes, subset |
| Range/counter family | Broad supported subset |
| `quantile_over_time` | No, intentionally local |
| Sort family | No |
| `round` | No |
| `label_replace`, `label_join` | No |
| Histogram helper family | No |
| `absent`, `absent_over_time` | No |

Relevant implementation hotspots today:

- `internal/promshim/native/analysis.go`
- `internal/promshim/native/renderer.go`
- `internal/promshim/native/types.go`
- `internal/promshim/storage/*.go`
- `internal/promshim/planner.go`
- `internal/promshim/exec/*.go`
- `harness/corpus/*`
- `harness/README.md`

## Measurement and acceptance prerequisites

These are not difficulty-ranked feature buckets; they are required so the 100% target can be measured.

### P0 — Replace the coarse README family matrix with a granular Path 2 compliance matrix

Create a machine-reviewable matrix that breaks Path 2 support down by:

- every aggregation operator
- every built-in function in the vendored Prometheus parser version
- unary/binary operators
- vector matching modes and modifiers
- selector forms
- subquery forms
- `offset` and `@` combinations
- classic histogram helper behavior over the current `TimeSeries` storage model
- expected error classes

Deliverables:

- a generated or hand-maintained granular compliance matrix under `.pi/` or `README.md`
- links from each matrix row to planner/renderer/executor/harness coverage
- explicit `path2_status = yes | partial | no`

### P1 — Establish a native-only acceptance mode

Use the existing native-only machinery (`force_supported`-style behavior) as the hard gate:

- if Path 2 cannot serve the query, the test must fail
- no silent Path 3 fallback in the acceptance path

The 100% target is only met when the granular compliance suite is green in native-only mode.

### P2 — Expand parity validation so Path 2 is tested on semantics, not just syntax

Use and extend:

- `harness/corpus/native-lowering-starter.json`
- `harness/corpus/common-dashboard-subset.json`
- themed shortlist corpora
- dataset variants (`baseline`, `resets_gaps`, `churn_stale`, `histogram_burst`)
- upstream conformance suite plan in `.pi/promql-conformance-suite-plan.md`

Acceptance must include:

- successful result parity
- expected-error parity
- staleness/lookback probes
- counter-reset probes
- histogram probes
- duplicate-labelset and vector-matching error parity

---

## Difficulty groups (descending)

## Group 1 — Hardest

### 1. Full temporal semantics foundation

This is the hardest bucket because it is cross-cutting and correctness-sensitive. Many remaining Path 2 gaps are really temporal-semantics gaps in disguise.

Scope:

- exact lookback semantics
- staleness behavior
- sparse/disappearing-series handling
- left-edge / right-edge window behavior
- `offset` interactions
- `@ <timestamp>`, `@ start()`, `@ end()` interactions
- subquery step-grid semantics
- instant vs range mode differences
- boundary parity for matrix-producing and matrix-consuming operators

Why this is hardest:

- it underpins `absent`, `absent_over_time`, range functions, counter functions, subqueries, and several histogram cases
- off-by-one and stale-sample mistakes can look correct on most happy-path queries while still being Prometheus-incompatible
- the repo already has evidence that edge inclusion/exclusion and step-grid behavior are subtle

Primary code areas:

- `internal/promshim/native/analysis.go`
- `internal/promshim/native/renderer.go`
- `internal/promshim/storage/*`
- local reference behavior in `internal/promshim/exec/*`
- harness dataset variants and staleness probes

Acceptance:

- Path 2 reproduces Prometheus on targeted staleness/lookback corpora
- subquery and range-function boundaries match Prometheus in native-only mode
- no remaining matrix-length or boundary-only parity mismatches in accepted corpora

### 2. Full histogram semantics

The README currently places the entire histogram helper family outside Path 2. For 100% compliance, this entire family must become native.

Scope:

- `histogram_quantile`
- `histogram_count`
- `histogram_sum`
- `histogram_avg`
- `histogram_fraction`
- classic bucket grouping / bucket sorting / bucket merging behavior
- monotonicity enforcement before quantile interpolation
- empty / sparse bucket behavior
- mixed-input behavior and error handling

Why this is hardest:

- `histogram_quantile` is not just an aggregation; it is semantic post-processing over bucketized input with Prometheus-specific behavior
- correctness depends on preserving histogram invariants across grouping, joins, rate-family inputs, and sparse windows
- this family is highly sensitive to dataset shape and interpolation details

Primary code areas:

- new native fragment kinds in `internal/promshim/native/types.go`
- lowering logic in `internal/promshim/native/analysis.go`
- SQL generation in `internal/promshim/native/renderer.go`
- specialized SQL builders in `internal/promshim/storage/*`
- local reference behavior in `internal/promshim/exec/histogram.go`

Acceptance:

- every histogram helper has native lowering
- parity holds for grouped, multi-label, sparse, and rate-family-fed cases
- README matrix flips histogram helper family from `No` to `Yes`

### 3. Full range/counter/subquery parity

README says Path 2 already supports a broad subset. For a 100% target, the word `subset` has to disappear.

Scope:

- close all remaining gaps in aggregate-over-time functions
- close all remaining gaps in counter/rate family lowering
- support every valid subquery shape accepted by the parser version in use
- support subquery arguments under range/counter functions where Prometheus allows them
- exact reset/extrapolation behavior
- exact missing-sample and sparse-window behavior
- parity for instant and range query modes

This includes retiring any remaining “supported only for selector-backed children” or “supported only for current subset” constraints.

Why this is hardest:

- range and counter semantics depend on the temporal foundation above
- subqueries multiply every boundary/step-grid problem
- exact Prometheus parity for resets/extrapolation is one of the easiest places to be “close enough” but still non-compliant

Primary code areas:

- range-function support in `internal/promshim/native/analysis.go`
- subquery rendering in `internal/promshim/native/renderer.go`
- SQL builders for windows/arrays in `internal/promshim/storage/*`
- local reference behavior in `internal/promshim/exec/*`

Acceptance:

- no Path 2 `subset` caveat remains for range/counter families
- native-only mode succeeds across the full range-function and counter-function checklist
- conformance and harness cases for subqueries go green without local fallback

---

## Group 2 — Very hard

### 4. Full label-mutation semantics

Path 2 currently does not support `label_replace` or `label_join`.

Scope:

- `label_replace`
- `label_join`
- regex capture and replacement semantics
- empty / missing source-label handling
- destination-label overwrite behavior
- metric-name retention/dropping rules after mutation
- duplicate-labelset collision behavior and parity with Prometheus errors
- interactions with aggregation, joins, and set operators after mutation

Why this is very hard:

- the functions themselves are not enormous, but exact Prometheus-compatible behavior includes string, regex, and collision semantics that affect downstream operators
- once label mutation becomes native, it must compose correctly with the rest of the native pipeline

Primary code areas:

- new native fragment kinds / transforms
- SQL string/regex building in `internal/promshim/storage/*`
- parity references in local label-mutation execution paths

Acceptance:

- both functions have native lowering
- parity holds for capture groups, unmatched regexes, empty labels, overwritten labels, and downstream duplicate-labelset behavior
- README matrix flips `label_replace`, `label_join` from `No` to `Yes`

### 5. Complete vector matching, binary, comparison, and set semantics

README says scalar/vector arithmetic and vector-vector joins are only partially supported natively.

Scope:

- every binary arithmetic operator supported by the vendored Prometheus version
- every comparison operator and `bool` form
- complete vector matching semantics for `on`, `ignoring`, `group_left`, `group_right`, and the full legal matching matrix
- metric-name propagation / dropping rules
- cardinality failure parity
- duplicate-series failure parity
- set operators with exact label matching semantics

Why this is very hard:

- vector matching is one of the densest parts of PromQL semantics
- correctness depends on both label behavior and temporal alignment behavior
- exact error parity matters as much as successful result parity

Primary code areas:

- join shape analysis in `internal/promshim/native/analysis.go`
- join SQL builders in `internal/promshim/storage/join_sql.go`
- planner integration in `internal/promshim/planner.go`
- local reference behavior in `internal/promshim/exec/vector_matching.go`

Acceptance:

- README matrix loses the `subset` qualifier for arithmetic and joins
- native-only mode passes both successful and expected-error vector-matching cases

### 6. Complete aggregation operator coverage

Path 2 pushdown currently supports a subset of aggregation operators. The 100% target requires native coverage for the full aggregation operator set exposed by the parser version in this repo.

Scope:

- current pushdown-safe set already present (`sum`, `count`, `min`, `max`, `avg`, `stddev`, `stdvar`, `quantile`, `group`)
- current local-only aggregation operators such as `topk`, `bottomk`, `count_values`
- any newer sampling/selection aggregators present in the vendored parser version
- `by(...)` and `without(...)` parity for the full set
- parameter validation and error parity
- nested aggregation composition in native fragments

Why this is very hard:

- some aggregators require ordered selection semantics rather than simple grouped reduction
- `count_values` introduces synthetic-label behavior that must compose with downstream operations
- parser-version drift means the implementation target should be derived from the actual vendored version, not memory

Primary code areas:

- aggregation support classification in `internal/promshim/native/analysis.go`
- SQL builders in `internal/promshim/storage/sql.go`
- planner and aggregation executor references

Acceptance:

- every aggregation operator in the parser-version matrix is native
- README matrix flips `Aggregations` from `Yes, subset` to `Yes`

---

## Group 3 — Hard

### 7. Native `quantile_over_time`

This function is currently an intentional Path 3 exception. That exception must go away.

Scope:

- exact Prometheus quantile semantics over matrix windows
- sparse/missing-point handling
- range-mode and instant-mode parity
- subquery-fed cases

Why this is hard:

- exact quantile behavior over uneven matrices is more subtle than simple grouped aggregates
- the repo currently treats this as an explicit keep-local design choice, so the plan must reverse both code and documentation

Acceptance:

- remove keep-local notes and fallback-only treatment
- native-only mode passes `quantile_over_time` cases
- README flips from `No` to `Yes`

### 8. Complete `info(...)` semantics

README already says Path 2 supports a subset. The subset boundary has to disappear.

Scope:

- all legal `info(...)` forms supported by the parser/runtime version in use
- info metric selection forms
- copied-label semantics
- unmatched-row behavior
- range and instant parity

Why this is hard:

- `info(...)` is join-like, label-sensitive, and sensitive to metric-shape assumptions
- the current native implementation is deliberately constrained

Acceptance:

- subset qualifier removed
- native-only mode passes full `info(...)` matrix

### 9. Finish the pointwise/math/trig/date/function closure

README says this family is “mostly yes”. For 100%, “mostly” must become “yes”.

Scope:

- every pointwise numeric transform supported by the parser version
- every trig/hyperbolic/date transform supported by the parser version
- all literal-parameter and edge-case variants
- exact metric-name retention behavior
- range and instant parity where applicable

Why this is hard:

- individually these functions are usually straightforward, but there are many of them and they need exhaustive, parser-version-driven completion
- small semantic differences accumulate across nested expressions

Acceptance:

- matrix row is complete for every function in this family
- README flips from `Mostly yes` to `Yes`

---

## Group 4 — Moderate

### 10. Sort family

README currently marks the sort family as unsupported natively.

Scope:

- `sort`
- `sort_desc`
- exact ordering parity
- tie behavior parity where Prometheus defines it
- range-query semantics per evaluation step

Why this is moderate:

- SQL is naturally good at ordering
- the main work is ensuring the PromQL result model is preserved rather than just ordering rows in an internal intermediate form

Acceptance:

- README flips `Sort family` from `No` to `Yes`
- native-only parity covers instant and range cases

### 11. Native `round`

README currently marks `round` as unsupported natively.

Scope:

- `round(v)`
- `round(v, to_nearest)`
- parity for negative values, fractional boundaries, `NaN`, `Inf`, and sparse data

Why this is moderate:

- implementation is much smaller than the families above
- the main task is exact parity, not algorithmic novelty

Acceptance:

- README flips `round` from `No` to `Yes`

### 12. Close remaining small built-in and wrapper gaps

Scope:

- any remaining gaps in `scalar(v)`
- any remaining synthetic/date builtin edge cases
- any wrapper/operator combinations that are still marked `subset` only because composition is incomplete

Why this is moderate:

- these are usually cleanup items once the large semantic families above are complete
- they still need exhaustive checklist-driven validation

Acceptance:

- no remaining `subset`, `mostly`, or `no` cells for Path 2 in the granular matrix

---

## Execution order views

### View A — foundation-first / hardest-first

This is the best order if the main goal is to eliminate architectural rework and avoid painting Path 2 into a corner:

1. **P0/P1/P2 measurement prerequisites**
2. **Temporal semantics foundation**
3. **Histogram family**
4. **Range/counter/subquery closure**
5. **Label mutation**
6. **Vector matching / binary / set closure**
7. **Aggregation closure**
8. **`quantile_over_time`**
9. **`info(...)` closure**
10. **Pointwise/math/date cleanup**
11. **Sort family**
12. **`round` and remaining small gaps**

Reason: the hardest items also create the foundations the rest depend on.

### View B — usefulness-first while still aiming for 100%

This is the best order if the goal is to improve the broadest real-query coverage first, without giving up the eventual 100% target:

1. **P0/P1/P2 measurement prerequisites**
2. **Range/counter/subquery closure**
3. **Aggregation closure**
4. **Vector matching / binary / set closure**
5. **Histogram family**
6. **Label mutation**
7. **Pointwise/math/date cleanup**
8. **Sort family**
9. **`round`**
10. **`info(...)` closure**
11. **`quantile_over_time`**
12. **Residual parser-version edge cases until the granular matrix is fully green**

Reason: this order tends to move the largest visible portions of the current README matrix and common PromQL shapes earlier, while still preserving the requirement that the final state reaches full compliance.

## Validation ladder

### Inner loop

- targeted `go test ./internal/promshim/...`
- targeted `go test ./integration/promshim`
- focused native-only harness corpora
- explain/native analysis inspection for each newly lowerable fragment kind

### Checkpoints

- `go test ./...`
- `./scripts/run-harness.sh --corpus native-lowering-starter.json --subjects shim`
- `./scripts/run-harness.sh --corpus common-dashboard-subset.json --subjects shim`
- themed shortlist runs
- dataset-variant runs (`baseline`, `resets_gaps`, `churn_stale`, `histogram_burst`)

### Full acceptance for the 100% target

- zero remaining Path 2 `partial` / `no` rows in the granular matrix
- native-only mode green for the accepted corpus set
- expected-error parity coverage present
- upstream conformance breadth run reduced to zero unsupported Path 2 cases for the target parser/version surface
- README updated to show full Path 2 support rather than family-level caveats

## Main risks

1. **False 100% from coarse accounting**
   - Mitigation: replace the family matrix with a parser-version-derived checklist.
2. **Near-parity instead of exact parity on temporal and counter semantics**
   - Mitigation: native-only gating plus dedicated dataset variants and error probes.
3. **Histogram correctness drift**
   - Mitigation: dedicated classic-histogram corpus and explicit checklist coverage for bucket, `_sum`, and `_count` semantics.
4. **Compositional gaps**
   - A function may work alone but fail once nested under joins, aggregations, or label mutation.
   - Mitigation: nested-expression matrix rows, not just single-operator tests.
5. **Parser-version drift**
   - Mitigation: derive the target matrix from the vendored Prometheus version in the repo, not from memory or outdated docs.

## Definition of done

This plan is done only when:

- the README Path 2 matrix no longer contains `subset`, `mostly yes`, or `no`
- the granular compliance matrix has no unresolved Path 2 gaps
- native-only acceptance is green
- all current intentional keep-local exceptions have been retired
- Path 2 can be described as a full PromQL-compliant native SQL path without qualification

## PS
- Dont name things in the codebase phase 2 or similar