# Eliminate `__name__` synthesis when not observed

## Problem

Every native selector unconditionally synthesizes a `__name__` tag tuple
at the front of its `tags` array:

```go
// selector_sql.go:332-339
base := sqlb.Call{Name: "arrayConcat", Args: []sqlb.Expr{
    sqlb.RawLit{V: "[tuple('__name__', " + metricColumn + ")]"},
    sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
        sqlb.RawLit{V: "(k, v) -> tuple(k, v)"},
        sqlb.Call{Name: "mapKeys", Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}}},
        sqlb.Call{Name: "mapValues", Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}}},
    }},
}}
```

The `src.metric_name` column is read at every selector and wrapped into
a tuple, even when the final output drops `__name__` at the very next
operator. Worse: downstream aggregation/join `arrayFilter`s then
explicitly strip `__name__` again (`join_sql.go:284`, `sql.go:699`).
The synthesis is paid on every row, the filter is paid on every
surviving row.

Examples where `__name__` is demonstrably unobservable:

```promql
# Outer sum drops the metric name; __name__ is filtered by arrayFilter
# in buildAggregationTagsExpr (sql.go:699-706).
sum(rate(http_requests_total[5m]))

# Binary arithmetic drops the metric name unconditionally (except for
# comparison ops without `bool`). __name__ is filtered by
# buildBinaryResultTagsExpr (join_sql.go:283-285).
foo + bar

# Aggregations with "without (...)" strip __name__ explicitly
# (sql.go:699: `labels := append([]string{labels.MetricName}, grouping...)`).
avg without (instance) (process_cpu_seconds_total)

# Scalar arithmetic drops metric name (DropsMetric=true at the
# ValueTransform level).
http_requests_total * 8
```

## Current behavior

- **Synthesis site**: `selector_sql.go:331-349` (`selectorTagsExpr`) —
  emitted from `buildMatchedSeriesSQL` at lines 385-388 whenever
  `selector.NeedTags` is true.
- **Explicit strip sites**:
  - `internal/promshim/storage/sql.go:698-706` — aggregation `without`
    includes `labels.MetricName` in the filter-out list. `by(G)` path
    at 708-717 only keeps `G`, which implicitly excludes `__name__`
    unless it's in `G`.
  - `internal/promshim/storage/join_sql.go:283-285` — comparison ops
    without `bool` keep `__name__`; everything else
    `arrayFilter(tag -> tag.1 != '__name__', …)`.
  - `internal/promshim/native/renderer/range.go:81` — range-window
    `resolvedFinalTagsExpr` strips `__name__` when the window drops
    metric name.
  - `internal/promshim/native/renderer/join.go` — value transforms with
    `DropsMetric` use `tagsTemplate = "arrayFilter(tag -> tag.1 !=
    '__name__', {tags})"` (line 222).
- **Lineage**: `LabelLineage.MetricName` tracks exactly this. Leaves
  set it to `Original` (`lineage.go:22`). Aggregations set it to
  `Dropped` (`lineage.go:83,95`). Comparison joins keep it, non-
  comparison joins and `bool` variants drop it
  (`analysis_binary.go:45-50`). `withMetricNameState(..., Dropped)`
  is applied on the result lineage of every metric-dropping transform.
- **`NeedTags`**: `selectorNeedsTags` (`sqlutil.go:132-137`) already
  short-circuits to skip tags entirely when neither `RequireFullTags`
  nor `RequiredTagLabels` are set. In that fast path
  (`buildInstantSelectorSourceSQL:190`), `selectorTagsExpr` is not
  called at all and `EmptyTagsArrayExpr()` is substituted.

## Proposed technique

Split selector projection into two orthogonal axes:

1. **Which user labels are needed** (doc 01's subject).
2. **Is `__name__` observable downstream?**

Add `SelectorSource.RequireMetricName bool` (default `true` for
safety). The optimizer walks top-down tracking whether any operator
*above* the selector will read `__name__`. `__name__` is observable
when:

- The top-level render mode is the user's final result **and** no
  `DropsMetric` intervenes between the selector and the top
  (rendered `tags` goes to the caller as-is).
- A comparison binary op without `bool` consumes the selector output
  (preserves metric name — `analysis_binary.go:45-50`).
- A `LabelTransform` reads `__name__` (e.g., `label_replace(x, "dst",
  "$1", "__name__", "(.*)")`). Check `spec.Src == "__name__"` and
  `spec.SrcLabels` for `label_join`.
- A `VectorMatching.On` list includes `__name__`, or `MatchingLabels`
  for `ignoring` does not include `__name__` *and* the
  many-side result tags include `__name__` because it's a comparison
  op.
- An `InfoJoin` relies on `__name__` for the `infoNameMatchers` path
  — but that selector is synthesized separately (`join.go:160`); the
  original selector doesn't need it.

In every other case, `__name__` is unobservable. When
`RequireMetricName=false` **and** `RequireFullTags=false`:

- Omit the `[tuple('__name__', metricColumn)]` from the `arrayConcat`
  entirely.
- If `RequiredTagLabels` is empty (and `NeedTags` still true because
  some label is needed elsewhere — e.g., from the wildcard doc 01
  fallback), emit only `arrayMap((k,v) -> tuple(k,v), mapKeys, mapValues)`
  (or even the narrowed subset).

When `RequireFullTags=true`, the optimizer may still strip
`__name__` synthesis if lineage says it's dropped — the arrayConcat
becomes a plain `arrayMap` over the tags Map.

## Expected gain

- **Per-row work**: drops one extra `tuple(…)` construction and one
  `arrayConcat` call per row. On a 10M-sample scan this is
  cumulatively nontrivial (CH's arrayConcat allocates a new
  `Array(Tuple)`).
- **`src.metric_name` column read**: this is the big one. `metric_name`
  is a `LowCardinality(String)` or plain `String` column in the
  TimeSeries schema. When no matcher references `__name__` *and*
  `__name__` is unobservable, the optimizer can also drop it from
  the matched-series projection — then ClickHouse's vectorized
  storage engine can skip the column entirely. Today every selector
  reads it because `metricColumn` is hard-coded into
  `compileMatcherClause` and `selectorTagsExpr`
  (`selector_sql.go:356,369`). Gain: one fewer column read per
  matched series.
- **Downstream `arrayFilter`**: the aggregation/join tag-filter
  sites already strip `__name__` defensively; they could be
  suppressed when the optimizer has provably omitted `__name__`.
  Less SQL, but more importantly one fewer pass over `tags` per row.
- **Wire bytes**: `__name__` typically adds 10–40 bytes per row in
  the JSONEachRow payload — negligible individually but compounds
  when the top-level `tags` contains only `__name__`.

## Risk / semantics caveats

- **Instant selector that is the final result**: `up`, `foo`, or any
  bare selector as the PromQL root. `__name__` is part of the user-
  visible response. `RequireMetricName=true` at the top level covers
  this.
- **Comparison operators that keep metric name**: `foo > 5` preserves
  `__name__`. The analysis layer already tracks this
  (`analysis_binary.go:45-50`, used via
  `nativeVectorJoinLabelLineage`). A top-down pass must reflect
  this: the child of a comparison op sees
  `RequireMetricName=true` unless an outer operator drops it.
- **`on(__name__)` / `ignoring()` without `__name__` mention**:
  promql-engine semantics say `ignoring()` does not exclude
  `__name__` from the matching side (binary ops strip metric name
  via the op itself, not via `ignoring`). `buildJoinGroupExpr`
  (`join_sql.go:239-255`) emits
  `tag -> NOT has(['__name__', ...ignoring], tag.1)` already; so
  the join-group key is always `__name__`-free — correct. However,
  the `result_tags` in `buildBinaryResultTagsExpr` may include
  `__name__` for comparison ops without bool — this is where we
  must not prematurely strip it.
- **`label_replace` / `label_join` writing to `__name__`**: e.g.,
  `label_replace(metric, "__name__", "renamed", "", "")`. The
  `LabelTransform` fragment's `Dst` would be `__name__`. When this
  pattern is detected, `RequireMetricName=true` on the child even
  though the *outer* result will eventually observe a renamed name.
  `mutateDestinationLabel` (`lineage.go:51-61`) sets
  `lineage.MetricName = Mutated` for this case — the optimizer can
  key off that state.
- **histogram_quantile**: reads `le` label, not `__name__`. Safe to
  drop `__name__` on the child if no other branch observes it.
- **absent / absent_over_time**: `outputMetricTagsSQL`
  (`sqlutil.go:16-30`) synthesizes the output labels directly; the
  child's `__name__` is irrelevant. Safe to drop.

One subtle case: `count_values`. The fragment synthesizes a label
whose *value* is the stringified series value. It doesn't read
`__name__` but the selection-aggregation path calls
`forceFragmentFullTags` (`join.go:67-73`) which would also force
`RequireMetricName=true`. Narrow that helper to just set
`RequireFullTags=true` without touching the new `RequireMetricName`
flag — the metric name itself is not needed.

## Implementation sketch

1. Add `RequireMetricName bool` to `storage.SelectorSource` next to
   `RequireFullTags`. Default `true` on construction paths
   (`selectorSourceFromMatchers` at `selector_sql.go:469-471` and the
   renderer's `source.go:275-284`).
2. Add an optimizer pass (or extend `applySelectorProjection`) that
   walks top-down and sets the flag `false` when lineage says the
   metric name is `Dropped` at every ancestor. Rules:
   - At the root, if `ctx.Mode` is Instant/Range and the fragment's
     own `DropsMetric==false`, set `RequireMetricName=true`.
   - Otherwise descend; at each child, propagate
     `requireNameDownstream = parentRequires && !nodeDropsMetric`.
   - `BinaryJoin` comparison ops without `bool`: push
     `requireNameDownstream=true` to the LHS (result keeps lhs
     name). `bool` or non-comparison: push `false`.
   - `LabelTransform` reading `__name__`: push `true`.
3. In `selector_sql.go`, gate the `[tuple('__name__', metricColumn)]`
   fragment on `selector.RequireMetricName`. When `false`, reduce
   `base` to the `arrayMap` over `mapKeys/mapValues` only.
4. Also gate the `metricColumn` inclusion in
   `buildMatchedSeriesSQL`'s `WHERE` clauses and `SELECT` projection
   when no matcher references `__name__` *and*
   `RequireMetricName=false`. `compileMatcherClause` only references
   it for the `labels.MetricName` matcher — if no such matcher
   exists, the column can be omitted from the projection entirely.
5. Optionally simplify downstream `arrayFilter(tag -> tag.1 !=
   '__name__', …)` when lineage confirms `__name__` never entered the
   array. Low-priority; ClickHouse evaluates the filter cheaply when
   the predicate is always false.

## Test coverage idea

- Add `TestSelectorSQL_OmitsMetricNameWhenNotObserved` in
  `storage/selector_sql_test.go` that builds a selector with
  `RequireMetricName=false, RequireFullTags=false,
  RequiredTagLabels=["job"]` and asserts the generated SQL:
  - Contains `arrayFilter(tag -> has(['job'], tag.1)`.
  - Does **not** contain `tuple('__name__'`.
  - Does **not** contain `src.metric_name` in the SELECT clause
    (when no `__name__` matcher).
- Add optimizer-pass test that covers:
  - `sum by (job) (http_requests_total)` → selector has
    `RequireMetricName=false`.
  - `http_requests_total > 0` (comparison, no `bool`) → selector has
    `RequireMetricName=true`.
  - `http_requests_total > bool 0` → selector has
    `RequireMetricName=false`.
  - `label_replace(http_requests_total, "x", "$1", "__name__", "(.*)")`
    → selector has `RequireMetricName=true`.
- Harness equivalence test: compare JSONEachRow `tags` fields for
  `sum(rate(x[5m]))` before/after the optimization — the set of
  labels in the response must be byte-identical.
