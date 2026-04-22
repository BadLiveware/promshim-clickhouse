# Native optimizer — research catalog

Research proposals for extending `internal/promshim/native/optimizer.go`. Each doc covers one
distinct technique: problem → current behavior (with line refs) → proposed change → expected
gain → semantics risk → implementation sketch → test idea.

The docs are independent research, not a coordinated plan. Numeric prefixes are per-author and
**not** an execution order — group by domain first, then weigh gain vs. risk within a domain.

## Selector & matcher pushdown

Rewrite selector-side predicates so they hit ClickHouse indexes and prune series early.

- [matcher-canonicalization](01-matcher-canonicalization.md) — rewrite `=~"foo|bar"` → `IN(...)`, single-literal regex → equality, handle the Prom `^(?:...)$` anchor wrap.
- [empty-matcher-and-tautology-elimination](02-empty-matcher-and-tautology-elimination.md) — drop `=~".*"`, fold `=~".+"` → `!=""`, detect unsatisfiable conflicts. Careful with `label=""` absent-label semantics.
- [metric-name-first-filtering](03-metric-name-first-filtering.md) — order `__name__` equality first, use typed column, convert `__name__=~"prefix.*"` → `startsWith(...)` for primary-key prefix pruning.
- [negative-matcher-pushdown-vs-pullup](04-negative-matcher-pushdown-vs-pullup.md) — correctness gap: negative matchers on Map-typed labels wrongly admit absent-label series; guard with `mapContains`.

## Projection & tag-column shape

Reduce bytes read and per-row work by shaping what columns/labels survive to each layer.

- [lineage-driven-tag-set-narrowing](01-lineage-driven-tag-set-narrowing.md) — use `LabelLineage.Known` to push precise `RequiredTagLabels` through binary joins, `label_replace`, `topk`, info joins — not just direct-child aggregations.
- [eliminate-name-synthesis-when-unobserved](02-eliminate-name-synthesis-when-unobserved.md) — skip the unconditional `arrayConcat([tuple('__name__', metric)], ...)` when no downstream operator observes `__name__`; drop the `src.metric_name` column read when possible.
- [tag-array-deferred-sort](03-tag-array-deferred-sort.md) — five `arraySort` sites redundantly re-sort an already-sorted tag array; introduce a `TagsSortedByKey` invariant and sort once at the top.
- [time-series-value-only-mode](04-time-series-value-only-mode.md) — when `NeedTags=false` and output collapses to a single series, the redundant `GROUP BY tags` on a constant collapses parallelism to one bucket; short-circuit to a scalar path.

## Aggregation pushdown & fusion

Avoid materializing per-series arrays when the outer operator can fuse.

- [fuse-sum-rate-single-group-by](02-fuse-sum-rate-single-group-by.md) — fuse `sum/avg/count/min/max by(g) (rate(...))` into one per-series-per-step window + one outer GROUP BY on `(g, eval_ts)`; skip the intermediate series `groupArray`.
- [count-from-series-cardinality](03-count-from-series-cardinality.md) — `count(m)` / `count by(l)(m)` reroute to `timeSeriesTags` alone; skip the value JOIN entirely.
- [topk-bottomk-early-cutoff](04-topk-bottomk-early-cutoff.md) — replace the `row_number() OVER (...)` pattern with `ORDER BY value DESC LIMIT k BY group_key`. Reject approximate `topK` (loses series identity).
- [absent-short-circuit](05-absent-short-circuit.md) — `absent(m{...})` / `absent_over_time` → `EXISTS` probe against tags (instant) or grid × extents anti-join (range); skip the full child pipeline.

## Range-function SQL shape

Reduce emitted SQL text size and per-row duplicated work inside range functions.

- [rangefn-common-subexpression-elimination](03-rangefn-common-subexpression-elimination.md) — `rangeFunctionValueExpr` re-renders `valuesExpr`, `finiteValues`, `hasNaN`, `seriesLength` 3–4× per column; bind once via column aliases / `WITH`.
- [direct-window-join-cost-model](04-direct-window-join-cost-model.md) — `preferDirectSelectorWindowJoin` uses a hardcoded `overlapSlots ≤ 4`; replace with cost function over (cardinality, samples-per-series, step, overlap).
- [counter-reset-delta-formula-reuse](05-counter-reset-delta-formula-reuse.md) — `rate`/`irate`/`increase`/`resets`/`changes` rebuild the same pairwise-neighbour iteration; push `counter_delta_sum` / `changes_count` into the selector-row columns; swap `arraySlice` → `arrayPopBack`/`arrayPopFront`.
- [stale-nan-filter-pushdown](06-stale-nan-filter-pushdown.md) — storage WHERE already applies `staleNaNFilterSQL` for range-vector selectors; propagate a `ValueNaNFree` provenance tag so the value-expr can drop its redundant `arrayFilter(v -> NOT isNaN(v), ...)`.
- [instant-first-last-over-time-argmax](07-instant-first-last-over-time-argmax.md) — instant-mode `last_over_time` / `first_over_time` / `ts_of_*` materialize a full sorted array to pluck one element; route through `argMax(value, timestamp)` / `argMin` aggregates (the shape the plain instant-selector path already uses).

## Join & binary-op optimization

Commute and prune predicates across join boundaries; short-circuit scalar shapes.

- [one-sided-predicate-commutation](02-one-sided-predicate-commutation.md) — commute `MatchEqual` label matchers across join boundaries when `on`/`ignoring` guarantees label-set equivalence. Negative matchers unsafe; set ops (`unless`) excluded.
- [join-key-propagation](03-join-key-propagation.md) — (a) inject `label!=""` matchers for every `on(...)` key into both sides (always sound); (b) cardinality-directed semi-join — small-side distinct keys fed as `IN` to the big side.
- [scalar-binary-fast-paths](04-scalar-binary-fast-paths.md) — identity folds (`m+0`, `m*1`, …) in analysis. `m*0` / `m**0` unsafe (NaN propagation). Collapsing `(m > bool C) > 0` unsafe in general — restrict to pure tautology against constants outside `{0,1}`.

## Planner performance & pass scheduling

Don't spend planner cycles on work the query doesn't need.

- [fragment-subtree-hashing](02-fragment-subtree-hashing.md) — FNV-1a structural hash per subtree → renderer CTE-based CSE + process-level LRU keyed on canonical PromQL + bucketed time bounds + schema epoch. Exclude `InferredMatchers`/`PushedMatchers` from hash.
- [stop-cloning-every-pass](03-stop-cloning-every-pass.md) — four deep clones per `OptimizeFragment` call for a 3-node tree; classify passes as analytical vs. mutating, clone once at entry, intern shared `*labels.Matcher`.
- [cost-model-driven-pass-ordering](04-cost-model-driven-pass-ordering.md) — compute a `fragmentShape` fingerprint once; gate each pass with a precondition. For `up{job=...}`, 4 of 9 passes skip. Preserve audit trail via `:skipped` suffix.

## Cross-cutting themes

Several techniques compound when stacked:

- **Matcher canonicalization** (regex → IN) + **metric-name-first** (prefix key prune) + **negative-matcher mapContains** fix a whole slice of selector latency *and* a correctness bug.
- **Lineage narrowing** + **eliminate __name__ synthesis** + **deferred sort** all chip away at the same `selectorTagsExpr` and would likely land as one PR.
- **Fuse sum(rate)** + **counter-reset formula reuse** + **stale-NaN pushdown** all target the same `BuildRangeWindowSelectorQuerySQL` path and should be sequenced carefully.
- **Fragment hashing** unlocks several downstream wins (CSE, plan cache, skip-if-unchanged) — likely the highest-leverage planner-infrastructure change.

## How to use this catalog

1. Pick a doc, read **Problem** and **Current behavior** first — that's the grounding.
2. Weigh **Expected gain** vs. **Risk / semantics caveats**. The semantics caveats are the load-bearing part; several proposals explicitly flag forms that are *unsafe* to apply.
3. The **Implementation sketch** names the pass slot and shape, not a finished patch. Treat it as a prompt for writing-plans, not a spec.
4. Each doc stands alone — you don't need to execute them in order or as a bundle.

## Suggested commit sequence

One optimization per commit. Ordered by correctness → low-risk mechanical wins → medium
selector/plan wins → range-function shape → short-circuits → larger architectural plays.
Earlier tiers introduce scaffolding (shape flags, lineage plumbing, provenance tags) that
makes later-tier diffs easier to read, so this order also minimizes review surface per commit.

### Tier 1 — correctness (not optional)

1. [negative-matcher-pushdown-vs-pullup](04-negative-matcher-pushdown-vs-pullup.md) — PromQL semantics bug: negative matchers on Map-typed labels wrongly admit absent-label series.

### Tier 2 — quick wins (small diffs, low semantic risk, measurable)

2. [stop-cloning-every-pass](03-stop-cloning-every-pass.md) — 4 deep clones per `OptimizeFragment` → 1.
3. [cost-model-driven-pass-ordering](04-cost-model-driven-pass-ordering.md) — precondition-gate the 9 passes on a one-shot fragment shape.
4. [tag-array-deferred-sort](03-tag-array-deferred-sort.md) — 5 redundant `arraySort` sites; sort once at the top.
5. [empty-matcher-and-tautology-elimination](02-empty-matcher-and-tautology-elimination.md) — simple AST peepholes on matcher lists.
6. [eliminate-name-synthesis-when-unobserved](02-eliminate-name-synthesis-when-unobserved.md) — skip the unconditional `arrayConcat([tuple('__name__', metric)], ...)`.

### Tier 3 — selector I/O wins (medium scope)

7. [matcher-canonicalization](01-matcher-canonicalization.md) — regex → `IN(...)`, single-literal regex → equality.
8. [metric-name-first-filtering](03-metric-name-first-filtering.md) — order `__name__` first, typed column, prefix-regex → `startsWith`.
9. [lineage-driven-tag-set-narrowing](01-lineage-driven-tag-set-narrowing.md) — generalize projection pushdown through joins / label-transforms / topk.
10. [stale-nan-filter-pushdown](06-stale-nan-filter-pushdown.md) — `ValueNaNFree` provenance tag; drop redundant `arrayFilter(v -> NOT isNaN(v), ...)`.

### Tier 4 — range-function shape

11. [rangefn-common-subexpression-elimination](03-rangefn-common-subexpression-elimination.md) — `WITH` / column-alias bindings for `values_finite`, `len`, `has_nan`.
12. [counter-reset-delta-formula-reuse](05-counter-reset-delta-formula-reuse.md) — swap `arraySlice` → `arrayPopBack`/`arrayPopFront`; share pairwise iteration across rate/irate/increase/resets/changes.
13. [instant-first-last-over-time-argmax](07-instant-first-last-over-time-argmax.md) — route through `argMax(value, timestamp)` streaming aggregate.

### Tier 5 — short-circuits & scalar

14. [absent-short-circuit](05-absent-short-circuit.md) — EXISTS probe against `timeSeriesTags`.
15. [count-from-series-cardinality](03-count-from-series-cardinality.md) — skip the value JOIN entirely for `count`/`count by`.
16. [scalar-binary-fast-paths](04-scalar-binary-fast-paths.md) — identity folds; **skip the unsafe ones flagged in the doc** (`m*0`, `m**0`, `(m > bool C) > 0`).
17. [topk-bottomk-early-cutoff](04-topk-bottomk-early-cutoff.md) — `ORDER BY value DESC LIMIT k BY group_key`.

### Tier 6 — bigger plays (plan carefully; brainstorm first)

18. [fragment-subtree-hashing](02-fragment-subtree-hashing.md) — infrastructure; unlocks plan cache + CSE.
19. [fuse-sum-rate-single-group-by](02-fuse-sum-rate-single-group-by.md) — highest single-query gain, most semantic surface.
20. [one-sided-predicate-commutation](02-one-sided-predicate-commutation.md) — commute `MatchEqual` across join boundaries.
21. [join-key-propagation](03-join-key-propagation.md) — inject `label!=""` for every `on(...)` key; cardinality-directed semi-join.
22. [time-series-value-only-mode](04-time-series-value-only-mode.md) — scalar-output fast path when `NeedTags=false`.
23. [direct-window-join-cost-model](04-direct-window-join-cost-model.md) — needs a cardinality probe; save for last once other work has landed.
