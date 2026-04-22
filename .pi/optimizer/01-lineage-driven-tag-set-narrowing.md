# Lineage-driven tag-set narrowing

## Problem

`applyProjectionPushdown` today narrows selector tag projection only for
`aggregation → selector` trees where the aggregation has a non-empty
`Grouping` and is `by(...)` (not `without`). Every other shape falls back
to either `RequireFullTags=true` (materialize the full label map) or the
empty-tags fast path. The label lineage information that the analysis
pass already computes (`LabelLineage.Known`) is not consulted.

Examples that pay the full-tags cost today but only ever expose a small
fixed subset of labels:

```promql
# Inner aggregation dropped to subset "job", outer sum also only needs "job".
# But lineage intersects to {job} at leaf.
sum by (job) (avg by (job) (http_requests_total{status="200"}))

# Binary join on `on(job, instance)`: only job+instance survive the join
# group, and any per-selector tags outside {job, instance} are discarded.
(rate(http_requests_total[5m])) / on(job, instance) (rate(http_errors_total[5m]))

# label_replace only reads __name__ + job from the input tag map; every
# other label is preserved but the transform itself never inspects them.
label_replace(http_requests_total, "dst", "$1", "job", "(.*)")

# topk preserves all labels per-series but the outer ignores them.
count by (team) (topk(5, http_requests_total))
```

In all four cases the renderer currently forces `RequireFullTags` on the
leaf selector — either because the fragment isn't a direct
`aggregation → selector`, or because `containsLabelTransform` / the
absent-grouping branch triggers (`optimizer.go:533–538`), or because the
join path calls `forceFragmentFullTags` on every descendant
(`source.go:302`, called from `join.go:72,155`).

The SQL shape produced when `RequireFullTags=true` is the full
`arrayConcat([tuple('__name__', metric_name)], arrayMap((k,v) ->
tuple(k,v), mapKeys(tags), mapValues(tags)))` tuple construction inside
`buildMatchedSeriesSQL` (`selector_sql.go:332–349`), which reads *every
column* of the `tags Map(String,String)` and synthesizes a
`Array(Tuple(String,String))` whose cardinality is the full label set
of each series. For series with 30–40 labels that is a ~40x blow-up on
a column that downstream operators will immediately filter to 1–4
labels.

## Current behavior

Relevant anchors:

- `internal/promshim/native/optimizer.go:205-209` runs `applySelectorProjection`.
- `internal/promshim/native/optimizer.go:521-550` is the pushdown logic.
  It only recurses on `fragment.Aggregation.Source`; it does not descend
  into `RangeFunction`, `BinaryJoin`, `LabelTransform`, `HistogramFunction`,
  `Subquery`, `InfoJoin`, etc. Non-aggregation fragments fall through to
  lines 546-549, which unconditionally set `RequireFullTags=true`.
- `internal/promshim/native/analysis.go:53,66,...,282` already populates
  `info.LabelLineage` for every node shape including binary joins
  (`analysis_binary.go:52-98`), `label_replace`/`label_join`
  transforms, and selection aggregations. `LabelLineage.Known` is the
  post-operator surviving label set, with states
  `original / copied / mutated / dropped / synthetic / unknown`.
- `internal/promshim/native/renderer/source.go:302-352`
  `forceFragmentFullTags` is a hammer that walks the whole sub-tree
  turning every `Selector.RequireFullTags` back to `true`. Join and
  info-join rendering invoke it defensively (`join.go:72,155`).
- `internal/promshim/storage/selector_sql.go:331-350` `selectorTagsExpr`
  is where the narrowed-vs-full decision actually reaches SQL. The
  narrowed path already wraps `arrayFilter(tag -> has([...], tag.1), …)`
  around the base `arrayConcat` — so the infrastructure is in place;
  the optimizer just never feeds it a non-trivial
  `RequiredTagLabels` list.

## Proposed technique

Walk the fragment tree top-down carrying a *required-upstream-labels*
set, starting from the caller contract (for `RenderModeInstant` /
`RenderModeRange` the response protocol surfaces `tags` verbatim so
the top-level set is "all known labels"; however the moment we cross
an aggregation boundary, `LabelLineage.Known` tells us exactly which
labels can survive). At each node:

1. Intersect the incoming required set with `info.LabelLineage.Known`
   keys whose state is not `Dropped`.
2. For each child, compute what labels that child must emit:
   - **Aggregation `by(G)`**: child must emit `G ∪ {__name__ if kept}`.
     Already handled, but extend to `without(W)` by subtracting `W`
     from the child's lineage-known labels *when* lineage is fully
     enumerated (Wildcard != Original means we know the full set).
   - **BinaryJoin one-to-one `on(L)`**: both children must emit
     `L ∪ Include`. One-to-one `ignoring(I)`: children must emit
     `(child.lineage.Known \ I) ∪ Include`.
     Many-to-one/one-to-many: the "many" side must additionally emit
     any label the downstream required set refers to.
   - **LabelTransform (`label_replace` / `label_join`)**: child must
     emit `Src` (for replace) or `SrcLabels` (for join), plus the
     downstream required set minus `Dst` if `Dst` is the only way
     downstream reads into that label.
   - **HistogramFunction**: child must emit `le` plus any grouping
     surviving out of the quantile aggregation. (Today `fragmentRequiresTags`
     forces this; narrowing it requires `le` always to be present.)
   - **RangeFunction / Subquery / ValueTransform / ClampTransform /
     ScalarConvert / Absent / SortTransform**: pass through unchanged.
   - **InfoJoin**: child must emit the identifying labels
     (`instance`, `job` per `join.go:170,181`) plus downstream.
   - **`topk` / `bottomk` / `limit_ratio`**: preserve per-series label
     set, so union with full set unless the outer operator narrows
     further.
3. When the leaf selector is reached, set `RequireFullTags=false` and
   `RequiredTagLabels = sorted(required)` if `required` is finite;
   fall back to `RequireFullTags=true` only when the required set
   includes a wildcard (i.e., some upstream operator has unknown
   lineage, such as `count_values` with a runtime-computed label).

Because `selectorTagsExpr` (`selector_sql.go:340-348`) already emits
`arraySort(tag -> tag.1, arrayFilter(tag -> has([L...], tag.1), …))`,
the end-to-end SQL shape stays the same; the optimizer simply feeds
it a larger class of queries.

## Expected gain

- **Tag-map deserialization**: ClickHouse reads the `tags` Map column
  as `mapKeys(tags)` + `mapValues(tags)` in `selectorTagsExpr`. When
  narrowed, both those calls are still made (the filter is applied
  post-materialization), so the raw read cost doesn't change — but
  the subsequent `arrayMap(...)` that constructs `Array(Tuple)` still
  materializes only the kept tuples, and the entire downstream join
  key / `GROUP BY tags` / wire payload compresses dramatically.
- **`GROUP BY tags`**: `buildInstantSelectorSourceSQL` groups by the
  full tags tuple (`selector_sql.go:187`). Narrowing from 30 labels to
  4 roughly cuts the group-by hash payload by 7–10× for typical
  `http_requests_total`-class metrics.
- **Join key hashing**: `buildJoinGroupExpr` (`join_sql.go:239-255`)
  builds an `arraySort(arrayFilter)` over the LHS/RHS tags at join
  time; today, because `forceFragmentFullTags` pushes the full map
  through, the arrayFilter re-scans every label. Narrowing at the
  leaf lets the filter operate on a pre-shrunk array.
- **Wire bytes**: `tags` is in the final `SELECT` list
  (`renderedColumnsForMode`, `optimizer.go:804-812`); fewer labels
  means fewer JSON bytes to the shim.

These compounds: a `sum by (job)` over a 40-label metric today
serializes 40× the tag tuples at the inner group-by, then drops 39/40
at the outer aggregation. Narrowing cuts the inner work by ~40×.

## Risk / semantics caveats

- **`without(...)`**: requires knowing the *full* source label set.
  `LabelLineage.Wildcard == Original` means "every original label
  survives except those enumerated"; we cannot enumerate the original
  label set statically unless the lineage has been narrowed earlier.
  Fallback rule: if encountering `without` and the child lineage is
  wildcarded, use `RequireFullTags=true`. Today's optimizer already
  does this (`optimizer.go:533-534`).
- **`__name__` preservation** interacts with this pass — e.g., a
  binary join that keeps `__name__` (non-comparison op, no `bool`)
  needs `__name__` in the required set. See doc 02 for the precise
  rules.
- **count_values** synthesizes a label whose name is known but whose
  value set is data-dependent. `forceFragmentFullTags` is applied in
  `join.go:67-73` specifically to avoid the empty-tags fast path.
  Narrowing via lineage should skip count_values sources by
  inspecting `fragment.Aggregation.Op`.
- **Regex matchers on labels**: `selectorTagsExpr` narrowing happens
  at *projection* time, after `WHERE` clauses are applied against
  `src.tags[key]`. Narrowing to a smaller set does not remove any
  rows — matchers already executed on the tag Map directly in the
  `timeSeriesTags` scan — so semantics are preserved.
- **InfoJoin identifying labels**: the renderer hard-codes
  `{instance, job}` (`join.go:170,181`). Narrowing the child must
  keep both, even if the caller didn't ask for them.

## Implementation sketch

1. Add `pushRequiredLabels(fragment, required)` in `optimizer.go`,
   invoked from `applyProjectionPushdown` instead of
   `applySelectorProjection`.
2. Seed the top-level `required` with:
   - `RenderModeInstant` / `RenderModeRange`: start with "wildcard"
     (callers see the full tags array verbatim).
   - Any context-supplied "drop __name__" downstream of `DropsMetric`
     is already handled by lineage's `MetricName = Dropped`.
3. For each node, compute per-child required sets per rules above.
   When `required` is a wildcard (unknown), propagate wildcard.
4. At each selector, resolve to `RequireFullTags` if wildcard,
   else `RequiredTagLabels = uniqueSortedStrings(required)`.
5. Remove the `forceFragmentFullTags` calls in `join.go:72,155` where
   lineage now gives precise information. Keep the helper only as a
   fallback for `count_values` and selection aggregations whose output
   label set is runtime-synthesized.

Side-effect: `requiredColumnsForFragment` (`optimizer.go:718-771`)
should report the narrowed `tags` column (report a label list, not
just "tags") so the OptimizationReport differentiates
"tags" vs "tags({job, instance})". Purely a reporting concern.

## Test coverage idea

- Extend `TestOptimizer_ProjectionPushdown` in `optimizer_test.go` to
  assert `RequiredTagLabels == [job, instance]` on the selector for:
  `sum by (job,instance) (...) / on(job,instance) sum by (job,instance) (...)`.
- Add a `renderer/builder_test.go` case asserting the rendered SQL
  contains `arrayFilter(tag -> has(['instance','job'], tag.1)` and
  does **not** contain `arrayConcat([tuple('__name__'` for both join
  sides. Use the existing substring-check style already in that file
  (`builder_test.go:325`).
- Harness regression: add a PromQL query
  `sum by (job) (rate(http_requests_total{service="api"}[5m]))` and
  assert the series-tags column in the JSONEachRow payload shows only
  `{job}` at the top and does not include arbitrary labels from the
  source (indirect proof the narrowing took effect). This is also an
  equivalence check against the scalar Prometheus engine.
