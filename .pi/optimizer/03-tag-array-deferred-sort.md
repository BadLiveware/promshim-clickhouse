# Tag-array deferred sort (emit raw, sort once at top)

## Problem

Every stage that touches the `tags` array wraps it in `arraySort(tag ->
tag.1, …)` even when the stage's output is about to be re-grouped or
re-filtered. This pays `O(n log n)` per tag-array per row, for
determinism that is only actually required at the top of the plan
(where `GROUP BY tags` and `ORDER BY tags` need byte-identical
payloads across rows of the same series).

Examples of redundant sorts in a single pipeline:

```promql
sum by (job) (rate(http_requests_total[5m])) / on(job) max by (job) (up)
```

Today's SQL fans out like this:

1. Each leaf `timeSeriesTags` scan emits
   `arraySort(tag -> tag.1, arrayFilter(tag -> has([...], tag.1),
    arrayConcat([tuple('__name__', metric_name)], arrayMap(…))))`
   (`selector_sql.go:340-348`).
2. The range-window source groups by `grid.tags` and then its outer
   uses `arraySort(item -> item.1, groupArray((timestamp, value)))`
   for `time_series` (`selector_sql.go:102`).
3. The aggregation builder computes `result_tags = arraySort(tag ->
   tag.1, arrayFilter(tag -> has([grouping], tag.1), tags))`
   (`sql.go:700-717`) — another sort over the already-sorted input.
4. The binary-join side-prep computes `join_group = arraySort(tag ->
   tag.1, arrayFilter(tag -> has([matching], tag.1), tags))`
   (`join_sql.go:245-248`) — third sort, on a still-sorted array.
5. The join result computes `result_tags = arraySort(tag -> tag.1,
   arrayFilter(…))` (`join_sql.go:271-287`) — fourth sort.
6. `arraySort(tag -> tag.1, arrayConcat(…include…))` applied when
   `Include` is non-empty (`join_sql.go:287-293`) — fifth sort.

Every one of these operates on a tag-array that was already sorted by
key at step 1. ClickHouse's `arraySort` is a full sort; it doesn't
detect pre-sorted input. For a series with ~10 labels, that's ~10·log₂(10)
≈ 34 comparisons per row per redundant sort. With 5 redundant sorts
across a plan, that's ~170 wasted comparisons per row; at 10M rows that
is ~1.7B key comparisons.

## Current behavior

Relevant anchors:

- **Leaf selector emits sorted tags**: `selector_sql.go:341-348`
  inside `selectorTagsExpr`. The sort is only applied in the narrowed
  path; the full-tags path (lines 331-339) emits in `mapKeys` order,
  which ClickHouse documents as ascending for `Map(String, String)`
  iteration — so it's *already sorted* lexicographically by key, but
  no downstream code relies on this.
- **Aggregation tag expr**: `sql.go:696-718`
  `buildAggregationTagsExpr` always wraps in `arraySort`. The input
  `column` is typically `series.tags` (already sorted) or `tags`
  (already sorted).
- **Join group / result**: `join_sql.go:239-255, 257-296`. Always
  `arraySort`.
- **Label transform**: `label_transform.go:83` — after filtering and
  `arrayPushBack(… tuple(dst, value))`, re-sorts. This one *is*
  semantically necessary (pushing a new tuple to the back breaks key
  order); cannot defer.
- **Range-window final sort**: `selector_sql.go:341` does **not** sort
  the `time_series` array itself inside the narrowed tag filter —
  `time_series` is sorted by `item.1` in step 2 of pipeline. Separate
  concern from tag sort.

Two invariants the renderer assumes:

1. `GROUP BY tags` and comparison `tags_a = tags_b` require identical
   byte representations, which for `Array(Tuple(String, String))` in
   ClickHouse means structurally equal → same elements in same order.
2. Downstream `arrayFilter(tag -> tag.1 != '__name__', tags)`
   preserves order, so if input was sorted, output is sorted.

## Proposed technique

Establish and propagate a **"tags-sorted by key"** invariant through
the fragment tree. Two tiers:

1. **Stage-local invariant**: every operator that produces a `tags`
   value documents whether it preserves, breaks, or establishes the
   sort. The renderer tracks this as a boolean on the internal
   `renderedFragment` (`SQL` stays as-is; a flag
   `TagsSortedByKey bool`).
2. **Apply one final sort** at the single place where sorted tags
   are semantically required: just before the outermost
   `GROUP BY tags` or `ORDER BY tags` of the terminal SELECT.
   Everywhere else, omit the sort.

Classification:

| Operator                             | Preserves | Needs input sorted | Output sorted |
|--------------------------------------|-----------|--------------------|---------------|
| `arrayFilter(tag -> …, tags)`        | yes       | no                 | as input      |
| `arrayConcat([__name__], mapSort)`   | no        | n/a                | sorted iff mapSort |
| `arrayMap((k,v)->(k,v), mapKeys…)`   | n/a       | n/a                | sorted (Map iterates by key)  |
| `arrayPushBack(filtered, new tuple)` | no        | no                 | unsorted — must sort |
| `GROUP BY tags`                      | n/a       | yes (for equality) | n/a           |
| Inner JOIN `lhs.join_group=rhs.join_group` | n/a  | yes (equality)     | n/a           |

The ClickHouse documentation states that `Map(K, V)` iterates in
insertion order, not sorted; however the TimeSeries engine stores
`tags` with sorted keys by convention (see `schema/schema.go` and
the experimental TimeSeries docs). **This claim needs a quick
empirical verification** (see Test coverage) — if verified, the leaf
`arrayMap(mapKeys, mapValues)` can skip the outer `arraySort`
entirely.

If mapKeys ordering is *not* guaranteed, we still get wins by
deferring re-sorts at aggregation, join-group, and join-result
sites: the leaf sort happens once, subsequent `arrayFilter` stages
preserve it, and `GROUP BY tags` consumes sorted rows.

## Expected gain

- **Aggregation**: removes one `arraySort` per row inside
  `buildAggregationTagsExpr`. For a `sum by (G)` aggregation where
  the filter keeps ~`|G|` labels, the sort cost per row drops from
  `|G|·log|G|` comparisons to zero.
- **Binary join**: four `arraySort` sites in `join_sql.go`
  collapse to zero if the input is sorted.
- **Downstream `GROUP BY tags` hash**: if `tags` is the canonical
  sorted form, the hash-group's `ColumnArray` compare is already
  byte-identical; no change. If we re-order the array we *must* keep
  the outer grouping consistent, hence the single final sort at the
  outermost `GROUP BY tags`.
- **Join equality check**: `lhs.join_group = rhs.join_group` compares
  `Array(Tuple)` byte-wise. If both sides descend from selector
  leaves with the same sort order, the equality holds without a sort
  at the join. Savings: one `arraySort` per side per row.

For a plan with 3 aggregations and 1 binary join, this removes
roughly 7 `arraySort` calls per row.

## Risk / semantics caveats

- **Map iteration order (mapKeys/mapValues)**: if ClickHouse changes
  Map iteration to unsorted, the leaf invariant breaks. Mitigation:
  always emit `arraySort` at the leaf (the current behavior in the
  narrowed path), but *skip* all subsequent sorts.
- **`arrayPushBack`**: `label_replace` uses it to append the new
  `Dst` tuple (`label_transform.go:83`). The re-sort there **must
  stay**. The optimizer must mark the output as `TagsSortedByKey=true`
  only because of that sort.
- **`arrayConcat(lhs_tags, rhs_include_tags)` in join result**:
  `buildBinaryResultTagsExpr` (`join_sql.go:286-293`) concats two
  pre-sorted arrays but the merge isn't order-preserving in general
  (two sorted arrays concatenated don't form a sorted array).
  This is where the final sort happens today. Keep this sort; this
  is the "one sort at the top" per plan branch.
- **Byte-equality vs semantic equality**: ClickHouse may represent
  `Array(Tuple(String,String))` with identical content in different
  byte layouts if string allocation differs. Structural equality over
  tuples is well-defined; row-level hash (`GROUP BY`) uses the
  logical representation. This is safe.
- **`ORDER BY tags`**: if we don't sort, the ORDER BY still produces
  a deterministic result (ClickHouse orders by tuple-array
  lexicographic comparison, which is the same regardless of internal
  tuple ordering within a row — except that tuple-array ordering
  itself depends on tuple order within the array). So two rows with
  the same semantic label set but different internal tuple orders
  would sort differently. **Critical**: final `ORDER BY tags` must
  operate on a canonicalized sort form. This reinforces "one final
  sort at the top."
- **Caller-facing `tags`**: the response protocol is JSONEachRow of
  `Array(Tuple(String,String))`. Clients that assume sorted-by-key
  would break if we emit unsorted. The top-of-plan sort preserves
  this contract.

## Implementation sketch

1. Introduce `TagsSortedByKey bool` on `renderedFragment` (in
   `internal/promshim/native/renderer/types.go` — currently has
   `SQL`, `QueryParams`).
2. Teach each renderer path to set the flag correctly:
   - Selector leaf (full or narrowed): `true` (leaf's `arraySort`
     stays).
   - `arrayFilter(tag -> tag.1 != '__name__', tags)`: inherit.
   - Aggregation: omit the outer `arraySort` inside
     `buildAggregationTagsExpr` when input is flagged sorted. Output
     stays sorted (filter preserves order).
   - Join-group: same as aggregation filter. Output sorted.
   - Join-result: `arrayConcat` breaks sort → must sort once. Output
     sorted.
   - LabelTransform `arrayPushBack`: must sort. Output sorted.
3. At the topmost operator (the one producing the user-visible SQL
   at `trimRenderedQuerySQL` time), assert the flag is `true`; if
   not, wrap in a final `arraySort`. Given the rules above, every
   path naturally ends sorted, so the final sort is a cheap
   invariant check.
4. Remove `arraySort` from:
   - `sql.go:699-717` (aggregation).
   - `join_sql.go:245-248` (join-group LHS).
   - `join_sql.go:251-254` (join-group RHS).
   - `join_sql.go:271-274, 277-280` (join-result single-side).
5. **Keep** `arraySort` at `join_sql.go:287-293` (arrayConcat of
   include labels), `label_transform.go:83`, and the leaf.

## Test coverage idea

- `TestClickHouse_MapIterationOrder`: a small integration test that
  inserts a known `Map(String,String)` with keys
  `[z,a,m]` (NOT insertion-sorted) and queries
  `arrayMap((k,v) -> tuple(k,v), mapKeys(tags), mapValues(tags))`,
  asserting the result array order is `[(a,...),(m,...),(z,...)]`.
  This establishes or falsifies the assumption that Map iteration is
  key-sorted.
- `TestOptimizer_DefersTagSort`: plan `sum by (job) (foo)` renders
  SQL that does **not** contain
  `arraySort(tag -> tag.1, arrayFilter(tag -> has(['job'], tag.1)`
  (the aggregation sort). Must still contain the leaf's
  `arraySort`.
- Join-specific: `foo / on(job) bar` renders SQL with exactly one
  `arraySort(tag -> tag.1, arrayConcat(`-style sort (the result-
  tags sort) plus two leaf sorts, zero join-group sorts.
- Harness differential: run before/after the optimization against
  the full PromQL compliance suite and confirm byte-identical
  JSONEachRow output — in particular that `tags` arrays come out in
  identical order.
