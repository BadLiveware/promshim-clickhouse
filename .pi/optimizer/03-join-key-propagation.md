# 03 — Join-key propagation from `on(...)` to upstream selectors

Use the `on(...)` clause to inject structural matchers (`label!=""`) into
each side's selector, and — when one side is demonstrably small in its
join-key cardinality — use the small side as a semi-join seed to restrict
the big side via an `IN (...)` predicate.

## Problem

```promql
http_requests_total * on(service,region) service_tier
```

The `on(service,region)` clause asserts a non-trivial fact: every series
that can possibly contribute to the output must have both a `service`
label **and** a `region` label, and both must be present with non-empty
values (in PromQL, missing label == empty string, so "non-empty" and
"present" are the same check). Today the shim does not exploit that.

A second, higher-leverage case:

```promql
pod_cpu_usage * on(pod) pod_spec_cpu_limit
```

`pod_spec_cpu_limit` is typically a metadata-like metric with a few
thousand series; `pod_cpu_usage` can easily have millions of samples per
step. Asymmetric cardinality on the join key means we could fetch the
distinct `(pod,)` tuple set from the small side and constrain the big
side's scan to *only* those tags via a ClickHouse `IN` subquery — a
classic semi-join optimization.

## Current behavior

- `renderBinaryJoinFragment` (`renderer/join.go:16-60`) calls
  `renderFragmentSubquery` for each side independently. Neither side
  sees the other's join-key value set.
- The join-key materialization happens at `buildJoinGroupExpr`
  (`storage/join_sql.go:239-255`) *inside* the join SQL — it runs
  `arrayFilter` + `arraySort` on each row after the scan. Nothing before
  the scan is aware of `on(...)`.
- Selector push-down (`optimizer.go:195-203`) knows nothing about the
  binary-join parent. The `on(...)` clause lives on
  `BinaryJoinFragment.VectorMatching` (`types.go:80-87`) and is only
  consulted by `buildJoinGroupExpr` and the label lineage walker
  (`analysis_binary.go:52-98`).
- `JoinShape` is filled in at analysis time but only tells the renderer
  whether to enforce duplicate-detection (`buildPreparedJoinSideSelect`
  at `storage/join_sql.go:173-215`). It does not inform matcher
  inference.

## Proposed technique

Split into two sub-optimizations that share a pass:

### 3A — Structural `label!=""` injection (unconditional)

For every `BinaryJoinFragment` where `matching.On == true` and
`JoinShape != many_to_many`:

- For each label `L` in `matching.MatchingLabels`, add a
  `MatchNotEqual(L, "")` matcher to both sides' selector
  `InferredMatchers`.
- Skip labels where the selector already has an equality matcher on `L`
  (which is strictly stronger than `!=""`).
- Skip `__name__` — it's always present.

This is always sound: the join-group expression
(`arrayFilter(tag -> has(MatchingLabels, tag.1))` at
`storage/join_sql.go:245-249`) will filter a row where `L` is absent to
an empty-or-reduced tag array, and the subsequent
`lhs.join_group = rhs.join_group` comparison cannot succeed unless the
*other* side also happens to match the same reduced key. In most
realistic workloads the missing-label rows will never join against
anything and are wasted scans.

### 3B — Cardinality-directed semi-join

Annotate each selector at analysis time with a conservative `HLL`-style
estimate of `(MatchingLabels)` cardinality, sourced from ClickHouse
table statistics (or a cached sample). If the ratio
`small.keyCardinality / big.keyCardinality < 0.01` (or another tuned
threshold) and the small side fits in memory, rewrite the join render to:

1. Render the small side first.
2. `SELECT DISTINCT` the join key tuples from the small side into a
   ClickHouse subquery or `CTE`.
3. Inject a tag-presence `WHERE` filter on the big side:
   `(tags['service'], tags['region']) IN (subquery)`.
4. Perform the original join on the now-reduced big side.

Note: the filter form depends on the schema — the existing
`selector_sql.go` already knows how to render tag-indexed lookups;
extend that helper to accept a multi-key `IN` list against a rendered
subquery. The subquery result set must materialize before the big-side
scan begins; ClickHouse's query planner generally achieves this, but
the safe encoding is as a `WITH` CTE or a scalar-subquery-hoisted
`GLOBAL IN` for distributed tables.

## Expected gain

- **3A** cuts the trivial case where a metric has partial label coverage
  (common with recording rules that add labels on some series but not
  others — e.g., ServiceMonitor relabeling creating `pod=""` rows).
  Gain is whatever fraction of series lacks the label; often 0–20% on
  tidy clusters, 50%+ on messy ones.
- **3B** is the real win. On the pod-metadata join example, the big
  side's scan drops by `(distinct pods in usage) /
  (distinct pods total)` — typically an order of magnitude or more when
  the small side is cluster-scope metadata. Combined with
  ClickHouse's tokenbf/set skip indexes on the tag columns this turns
  a full-table scan into an index-guided fetch.

## Risk / PromQL semantics caveats

- **`!=""` vs missing** — in PromQL, `label=""` matches series where
  `label` is absent. Therefore `label!=""` on a selector correctly
  excludes *both* empty and missing. This is exactly what `on(L)`
  needs: a row that would contribute `""` to the join key cannot match
  any row with a non-empty `L` via `on(L)`. The injection is sound.
- **`ignoring(...)` joins are excluded.** `on` asserts presence of
  specific labels; `ignoring` says "match on everything except these."
  A row with an unusual label set could still contribute under
  `ignoring`, so 3A does not apply.
- **Set operators (`and`, `or`, `unless`)** — excluded. `LOR` in
  particular is a union: rows that don't match the rhs still appear
  in output, and injecting `label!=""` onto the rhs side would delete
  those rows from the lhs contribution under bugs that cross-pollute
  sides. Safe stance: never apply under any set operator.
- **Cardinality estimates can be stale.** 3B must have a safety
  fallback: if the distinct-key subquery exceeds a threshold (e.g.,
  50 000 rows or 2 MiB serialized), fall through to the current
  unoptimized plan. ClickHouse's `SET max_rows_in_distinct` can
  enforce this during execution.
- **Label lineage is unaffected.** The injected matchers are invisible
  to `nativeVectorJoinLabelLineage` because lineage is computed from
  output labels, not input filters.
- **NaN propagation:** unchanged — value filtering runs in
  `buildBinaryValueExpr` and is orthogonal.
- **Empty-vector results.** If 3B's small side produces zero rows, the
  rewritten plan must still return an empty result (not an error), and
  the big side's scan must be skipped entirely. The `IN (empty)` form
  in ClickHouse correctly returns empty, but regression-test this path
  — historically similar optimizations have tripped on it.

## Implementation sketch

Two places, one pass:

```go
// optimizer.go

func applyJoinKeyPropagation(state *optimizerState) error {
    forEachBinaryJoin(state.fragment, func(j *BinaryJoinFragment) {
        if isSetOperator(j.Op) || j.JoinShape == JoinShapeManyToMany { return }
        matching := normalizeVectorMatching(j.VectorMatching)
        if !matching.On { return }
        injectPresenceMatchers(BaseSelectorSource(j.LHS), matching.MatchingLabels)
        injectPresenceMatchers(BaseSelectorSource(j.RHS), matching.MatchingLabels)
    })
    return nil
}

func injectPresenceMatchers(sel *SelectorSource, joinKeys []string) {
    if sel == nil { return }
    existing := map[string]bool{}
    for _, m := range sel.Matchers {
        if m != nil && m.Type == labels.MatchEqual {
            existing[m.Name] = true
        }
    }
    for _, key := range joinKeys {
        if key == labels.MetricName || existing[key] { continue }
        sel.InferredMatchers = append(sel.InferredMatchers,
            labels.MustNewMatcher(labels.MatchNotEqual, key, ""))
    }
}
```

Register as `PassJoinKeyPropagation` immediately after
`PassLabelPredicatePushdown` and before `PassProjectionPushdown`. Re-run
`applyLabelPredicatePushdown` logic on each side's selector afterwards so
the inferred matchers materialize into `PushedMatchers`.

For 3B, extend `BinaryJoinConfig` (`storage/join_sql.go:12-17`) with
an optional `SemiJoinSeed *SemiJoinSeedConfig` and teach
`buildBinaryVectorJoinSQL` to wrap the big side's source SQL with a
`WHERE (tags_at('service'), tags_at('region')) IN (...)` filter drawn
from the seed. Drive the choice from a small
`cardinalityEstimator` interface that defaults to "unknown → disabled"
and gets a real implementation later from ClickHouse
`system.column_statistics` or a cached sample.

## Test coverage idea

- Unit: `up * on(job) notify_config` — assert both selectors end up with
  `job!=""` in `InferredMatchers`, verify duplicate suppression when the
  user already wrote `job="api"`.
- Unit: `up * on() scalar_like` — zero-key `on()` clause → no
  presence matchers injected, pass is a no-op.
- Unit: `up * ignoring(job) notify_config` — no injection (not `on`).
- Unit: `up and on(job) notify_config` — set operator, no injection.
- Golden SQL: snapshot the selector SQL emitted by `selector_sql.go` for
  an `on(a,b)` join and confirm two extra `has(tags, 'a') AND tags[...]
  != ''` predicates appear on each side.
- Conformance: feed a fixture where the rhs has a series with
  `region=""`; verify the rewritten plan returns the same rows as
  Prometheus (which would also drop that series via the join-group
  mismatch).
- 3B regression: a fixture where the seed subquery is empty; assert the
  wrapped plan returns 0 rows without erroring, and assert a cost
  sensor (query bytes-read metric from ClickHouse
  `system.query_log`) stays below the unoptimized baseline.
