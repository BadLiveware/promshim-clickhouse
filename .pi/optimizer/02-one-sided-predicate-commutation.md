# 02 — One-sided predicate commutation across binary joins

Move label matchers across the binary-join boundary: infer matchers that can
be duplicated onto the opposite side of the join, and elide matchers that are
dominated by the join's match set. The goal is to shrink both sides' scans
before the expensive `arraySort(arrayFilter(...))` join-group build kicks in.

## Problem

Consider the three shapes:

```promql
-- (a) redundant-on-matched-side: matcher label is a match key
node_cpu_usage{env="prod"} * on(env,instance) node_cpu_limit

-- (b) pushable across `on(...)`: matcher label is a match key
node_cpu_usage{env="prod"} / on(env,instance) node_cpu_limit
-- env="prod" on lhs implies we can scan rhs with env="prod" too

-- (c) outside `ignoring(...)`: matcher label is preserved by the join
http_requests{env="prod"} + ignoring(instance) http_cache_hits
-- env is kept through the join; no commutation gain, but the matcher stays
-- a valid local filter (current behavior is already correct here)

-- (d) inside `ignoring(...)`: matcher label is thrown away
http_requests{job="api"} / ignoring(job,instance) http_request_duration_seconds
-- job= on lhs does NOT imply anything about rhs, because the join matches
-- every label except job/instance. Cannot commute. But: `job="api"` is still
-- a valid local pre-filter on the lhs scan (what we do today).
```

Today every one of these renders each side as a fully independent selector
subquery (`renderer/join.go:28-52`) which reads the full set of series each
side matches, then joins in SQL. For (a) and (b) that's wasted IO: ClickHouse
sees the rhs scan without the `env="prod"` filter even though the join would
drop every non-prod rhs series at the `join_group = join_group` step.

## Current behavior

- `applyLabelPredicatePushdown` (`optimizer.go:195-203`) only pushes matchers
  into **`BaseSelectorSource`** — the single leaf selector under the root.
  Binary join fragments do not expose their subtree selectors to this pass
  because `BaseSelectorSource` stops at the root's dominant branch
  (`optimizer.go:658-716` walks only the first non-nil child and does not
  recurse into both sides of `BinaryJoin`).
- `applyJoinNormalizationDuplicateDetection`
  (`optimizer.go:220-227`, `joinNormalizationForFragment` at `:855-865`) only
  reports `"required"` vs `"not_applicable"` as a string — it does no rewrite.
- `renderBinaryJoinFragment` (`renderer/join.go:16-60`) renders each side via
  `renderFragmentSubquery`. Each side carries its own `PushedMatchers` from
  its own selector only.
- `normalizeVectorMatching` (`vector_matching.go:26-31`) normalizes the
  matching spec but is only consulted downstream in
  `buildJoinGroupExpr` (`storage/join_sql.go:239-255`) and in the label
  lineage walker (`analysis_binary.go:52-98`).

So the optimizer today has the shape information (`JoinShape`, `On`,
`MatchingLabels`) but never uses it to move matchers.

## Proposed technique

Add a fragment-layer pass `PassJoinMatcherCommutation` between
`PassLabelPredicatePushdown` and `PassProjectionPushdown`. For every
`BinaryJoinFragment`:

1. Collect the combined selector matchers from the lhs and rhs subtrees by
   walking `BaseSelectorSource` on each side (extending it to return *both*
   sides for joins — or introduce `BinarySideSelectors(fragment)`).
2. For each matcher `m` on side `S` with label `L`:
   - **Case (i)** `matching.On && contains(matching.MatchingLabels, L) &&
     m.Type == MatchEqual` → the join requires both sides to share value `L`.
     Inject a clone of `m` as an *inferred* matcher on the opposite side's
     `PushedMatchers`. Record as a commuted predicate in the report.
   - **Case (ii)** `!matching.On && !contains(matching.MatchingLabels, L)` —
     i.e. the label is NOT in the `ignoring(...)` set → same inference, label
     is preserved by the join so the matcher can commute. Same clone action.
   - **Case (iii)** `matching.On && !contains(matching.MatchingLabels, L)` →
     label is irrelevant to the join key; the matcher is already a valid
     local pre-filter (no change needed, but also no redundancy to elide —
     it's genuinely filtering `S`).
   - **Case (iv)** `!matching.On && contains(matching.MatchingLabels, L)` →
     label is in `ignoring(...)` so it cannot be commuted; the matcher is
     a local filter only (no change).
3. Restrict commutation to `MatchEqual` and (eventually) `MatchRegexp` that
   have been canonicalized to an IN-set by pass 01. Never commute
   `MatchNotEqual` / `MatchNotRegexp` — a label absent on one side may still
   carry a matching value on the other, and `!=""` vs missing semantics
   under `on(...)` depend on whether the join key is present at all (see
   doc 03 for the `!=""` case).
4. Only apply when `JoinShape` is `one_to_one`, `many_to_one`, or
   `one_to_many`. `many_to_many` (set operators) have their own semantics
   where commutation would change results under `LUNLESS` — e.g.,
   `A{env="prod"} unless B` is **not** equivalent to
   `A{env="prod"} unless B{env="prod"}`: B's non-prod series may still
   subtract some A series if B happens to share only the `ignoring` labels.
   Skip set operators entirely.

## Expected gain

Qualitative, not benchmarked:

- Scan reduction on the opposite side proportional to the cardinality of the
  commuted label. For `env="prod"` on a two-env cluster the rhs scan halves.
- Eliminates a dominant failure mode where users hand-duplicate matchers
  defensively (`foo{env="prod"} * on(env,instance) bar{env="prod"}`) and
  the shim happily runs the same filter twice in SQL without deduping — but
  *doesn't* add it when the user wrote it once.
- Extra savings stack with doc 03's `a!=""` injection when `on(a,b)` is
  present.

## Risk / PromQL semantics caveats

- **Case (i)/(ii) correctness hinges on `MatchEqual`.** Commuting
  `MatchNotEqual` is incorrect: Prometheus treats `env!="staging"` on the
  lhs as selecting lhs series that either have `env` ≠ staging OR lack the
  label altogether. If the rhs has a series with `env` absent, forcibly
  applying `env!="staging"` filters it out even though it would have
  matched via lhs's `env` being absent as well. (Empty label == absent in
  Prometheus; this is specifically the rule that makes negative commutation
  unsound.)
- **Regex matchers** must be pre-canonicalized by pass 01 before they're
  eligible. Commuting a raw regex is technically sound but may regress
  performance on the target side if the target column isn't indexed for
  regex — push only the canonicalized equality/IN form.
- **Label-set preservation under `one_to_one`/`many_to_one`.** The pass
  never alters output labels; it only prunes input rows. Output tags still
  flow through `buildBinaryResultTagsExpr` (`storage/join_sql.go:257-296`)
  unchanged.
- **NaN propagation.** Adding a matcher does not change how a row's
  `value` is treated — NaN samples are filtered by `valueFilter` in
  `buildBinaryValueExpr`, which is independent of labels.
- **Metric name (`__name__`).** Never commute `__name__="foo"` across a
  join — the lhs and rhs metric names are nearly always distinct and the
  commutation would zero out the opposite side. Matchers with
  `matcher.Name == labels.MetricName` must be excluded.
- **`ReturnBool` mode** (`m1 > bool m2`) does not alter join key
  semantics, so commutation still applies.

## Implementation sketch

```go
// optimizer.go additions (pseudo-diff)

func applyJoinMatcherCommutation(state *optimizerState) error {
    rewrite(state.fragment, func(f *NativeFragment) {
        if f.Kind != FragmentKindBinaryVectorJoin || f.BinaryJoin == nil {
            return
        }
        if isSetOperator(f.BinaryJoin.Op) { return }
        matching := normalizeVectorMatching(f.BinaryJoin.VectorMatching)
        commutableLabel := func(name string) bool {
            if name == labels.MetricName { return false }
            if matching.On {
                return contains(matching.MatchingLabels, name)
            }
            return !contains(matching.MatchingLabels, name)
        }
        lhsSel := BaseSelectorSource(f.BinaryJoin.LHS)
        rhsSel := BaseSelectorSource(f.BinaryJoin.RHS)
        if lhsSel == nil || rhsSel == nil { return }
        commuted := commuteMatchers(lhsSel.Matchers, rhsSel, commutableLabel)
        commuted = append(commuted, commuteMatchers(rhsSel.Matchers, lhsSel, commutableLabel)...)
        state.report.InferredPredicates = mergeUniqueStrings(
            state.report.InferredPredicates, commuted...)
    })
    return nil
}
```

Where `commuteMatchers` only clones `MatchEqual` matchers whose label
passes `commutableLabel`, appends them to the target selector's
`InferredMatchers`, and returns their string form for the report.

Registration is a new `OptimizerPass` const
(`PassJoinMatcherCommutation = "join_matcher_commutation"`) inserted into
`FixedPassOrder` and `optimizerPasses` right before `PassProjectionPushdown`
so `BaseSelectorSource.PushedMatchers` is computed after commutation.

The subsequent `applyLabelPredicatePushdown` call needs to be made
side-aware as well — today it only mutates the root selector. A small
helper that iterates all selectors under a fragment and materializes
`PushedMatchers = merge(Matchers, InferredMatchers)` suffices.

## Test coverage idea

Expand `optimizer_test.go` (or the appropriate existing test file — see
`renderer/builder_test.go:848` for a join fixture already in place):

- Unit: feed `vector(1) * on(env,instance) metric{env="prod"}` and assert
  `state.report.InferredPredicates` contains `env="prod"` duplicated onto
  the synthetic side.
- Unit: `a{env="prod"} / ignoring(env) b` — assert NO duplication onto b,
  because `env` is in the ignoring list.
- Unit: `a{env!="staging"} * on(env) b` — assert NO duplication (negative
  matcher not commutable).
- Unit: `a unless b{x="y"}` — assert the pass is a no-op (set operator).
- Golden SQL: run the rendered SQL through
  `storage.BuildInstantBinaryVectorJoinSQL` and snapshot-diff to confirm
  the rhs side's selector carries an extra `env = 'prod'` predicate.
- Compliance: wire the commuted query into the conformance harness to
  verify byte-identical output with Prometheus for (a), (b), and — as a
  safety negative — a case where the lhs has `env="prod"` but the rhs
  happens to lack the `env` label entirely. Under `on(env,instance)`
  nothing matches anyway, so behavior is unchanged; confirm.
