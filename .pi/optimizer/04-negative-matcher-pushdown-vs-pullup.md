# 04 — Negative-matcher pushdown vs. pull-up

PromQL's negative matchers (`label!="x"`, `label!~"regex"`) have a subtle
semantic: they match only series where the label is *present* with a
non-matching value. Series missing the label are excluded. The shim's current
SQL treats the tags map subscript on a missing key as `''` and then compares,
which gives *the opposite* of the PromQL rule. This doc is primarily a
correctness discussion dressed as an optimization — with a follow-up about
whether to push the (corrected) negative predicate into the series-pruning
subquery or keep it outside.

## Problem

Given `up{env!="dev"}`, Prometheus returns series with an `env` label whose
value is not `"dev"`. Series that lack the `env` label are **excluded**.

Current shim SQL (`buildMatchedSeriesSQL` → `compileMatcherClause`,
`selector_sql.go:425-426`):

```sql
src.tags[concat('', {..._key:String})] != {..._value:String}
```

ClickHouse `Map(String, String)` subscript on an absent key returns `''`.
Therefore:

- `env != 'dev'` for a series missing `env`: evaluates `'' != 'dev'` → **true**.
  Series is included.
- That is the opposite of Prom semantics — we leak series that should be
  filtered out.

Same issue for the regex case:
```sql
NOT match(src.tags[concat('', {..._key:String})], {..._value:String})
```
Evaluated on a missing `env`: `NOT match('', '^(?:dev)$')` → `NOT false` →
**true** → series included. Again opposite of Prom.

Verify: run the conformance harness with a selector like
`metric_with_optional_label{optional!="x"}` and compare against upstream Prom
behaviour. If tests pass today, either (a) the underlying data always carries
the label, or (b) the conformance suite doesn't cover this edge.

## Current behavior

- `internal/promshim/storage/selector_sql.go:425-426` — `MatchNotEqual` →
  `col != value`. No guard that the label is present.
- `internal/promshim/storage/selector_sql.go:429-430` — `MatchNotRegexp` →
  `NOT match(col, value)`. Same gap.
- `internal/promshim/storage/selector_sql.go:406-408` — tag lookup uses
  `concat('', {key:String})` to force a `String` key type but performs a
  straight `Map` subscript. No `mapContains(...)` check.
- Because `compileMatcherClause` is called directly from
  `buildMatchedSeriesSQL` (line 365-380), all matchers — positive and negative
  — are pushed into the `timeSeriesTags` subquery that prunes series. No
  distinction between predicates safe to push and predicates that must run on
  the joined result.

## Proposed technique

Two parts: (a) fix negative-matcher semantics, (b) decide where they run.

### A. Correct the semantics

For `label != "value"` emit:
```sql
mapContains(src.tags, {..._key:String})
  AND src.tags[{..._key:String}] != {..._value:String}
```
For `label !~ "regex"` emit:
```sql
mapContains(src.tags, {..._key:String})
  AND NOT match(src.tags[{..._key:String}], {..._value:String})
```
For the value `""` case (e.g. `label!=""`), the first conjunct collapses
because we explicitly want "label present" — which `mapContains` expresses
exactly, and `tags[...] != ''` is already redundant. Simplify:
```sql
mapContains(src.tags, {..._key:String})
```

Verify: confirm ClickHouse function name in the deployed version —
`mapContains(m, k)` has been stable since 21.x; alternatives are
`has(mapKeys(m), k)` (older) and `m[k] IS NOT NULL` (incorrect for
`Map(String, String)` because the return type is not `Nullable`).

### B. Pushdown vs. pull-up

In PromQL, positive matchers are *always* safe to push into the series-pruning
step (they can only further restrict the series set). Negative matchers are
safe to push *once we have the present-key guard* — the guard makes the
predicate monotone in the series set. Before the guard was added, pushing the
broken predicate was actively harmful (it admitted series it should have
rejected, so more-series was the wrong direction).

So: keep negatives in the `buildMatchedSeriesSQL` WHERE (continue to push
down), but only after `A.` is in place. This reduces the cardinality of the
series set joined against `timeSeriesData` — which is the expensive join.

There is one case where pull-up might still be the better choice: if the label
cardinality is extreme (millions of values) and the negative predicate rejects
only a handful, the optimizer could choose to skip the pushdown and filter
post-join. In practice the cardinality of tags is already bounded by the
tags table size; the pushdown is always a win. Don't pull up.

## Expected gain

- **Correctness first.** Negative matchers currently produce incorrect
  results for series missing the referenced label. Fixing this closes a
  conformance gap.
- **Fewer joined rows.** Pushdown of the now-correct predicate prunes the
  series list before it reaches the expensive INNER JOIN with
  `timeSeriesData`. The join is on `id`, so scanning the data table is
  proportional to the series set size.
- **`mapContains` is a hash lookup.** Cheaper than `Map[k]` subscript + string
  compare against `''` for the `!=""` special case.

## Risk / PromQL semantics caveats

- **`label!=""` vs. `mapContains`**. PromQL: `label!=""` matches series where
  the label is present with any non-empty value. `mapContains(...)` matches
  series where the label is present, *including empty value*. Not quite the
  same.
  Full translation of `label!=""` is `mapContains AND tags[label] != ''`.
  The simplification in A only applies when we're canonicalizing
  `label!=""` to mean "label present, value non-empty"; keep both conjuncts.
- **Double-filtering confusion with `=""`.** Doc 02 explains that `label=""`
  matches missing-or-empty. That does NOT need a `mapContains` wrap; the
  current `tags[k] = ''` correctly matches both cases because missing
  subscripts already return `''`.
- **Regex negation and absent labels.** Prom: `label!~"x"` excludes series
  without `label`. With correction it becomes:
  `mapContains AND NOT match(tags[k], '^(?:x)$')`. Without the guard the
  current shim incorrectly *includes* absent-label series unless `''` happens
  to match the regex. For patterns like `!~"dev|qa"`, `''` doesn't match, so
  the absent-label series *are* currently included — wrong.
- **`__name__` is different.** `__name__` is always present; don't guard it
  with `mapContains`. The `selector_sql.go:406` special-case already targets
  the typed column, so the fix is naturally narrow to tag matchers.
- **Short-circuit interaction.** Doc 02 defines an "unsatisfiable" selector.
  `label!=""` combined with `label=""` is unsatisfiable; detect this in the
  simplification pass rather than relying on the SQL to evaluate to no-rows.

## Implementation sketch

Modify `compileMatcherClause` to branch on `matcher.Name != labels.MetricName`
and `matcher.Type in {MatchNotEqual, MatchNotRegexp}`:

```go
// Tag-column negative matcher. Guard with mapContains.
if matcher.Name != labels.MetricName &&
    (matcher.Type == labels.MatchNotEqual || matcher.Type == labels.MatchNotRegexp) {
    containsExpr := sqlb.Call{
        Name: "mapContains",
        Args: []sqlb.Expr{sqlb.RawLit{V: tagsColumn}, sqlb.Param{Name: keyName, Type: "String", V: matcher.Name}},
    }
    containsSQL, _, _ := sqlb.BuildExpr(containsExpr)
    base := ... /* existing != or NOT match output */
    return "(" + containsSQL + " AND " + base + ")", params
}
```

To make the SQL cleaner when the matcher value is `""` and the matcher is
`MatchNotEqual`, special-case to just `mapContains(...)` — the second
conjunct is redundant because an absent key already reads as `''`, so any
present key passes `!= ''` trivially *except* for the pathological case of
a present-empty-valued key. That case should still be excluded per Prom
semantics (`label!=""` matches only non-empty). So keep both conjuncts for
correctness; no shortcut here.

If docs 01 / 02 have landed, this pass runs after them and benefits from the
cleaner matcher set (no `.*` regexes to guard, `!~".+"` already folded to
`!=""`, etc).

## Test coverage idea

- Unit: `TestNegativeMatcherGuardsAgainstAbsentLabel` — series `{foo="a"}`
  and `{foo="a", env="dev"}` and `{foo="a", env="prod"}`; selector
  `foo_metric{env!="dev"}` returns only the `prod` series (not the
  label-absent one).
- Unit: `TestNegativeRegexMatcherGuardsAgainstAbsentLabel` — same fixture,
  `env!~"dev|qa"` returns only `prod`.
- Unit: `TestNegativeMatcherSQLShape` — assert that the emitted SQL contains
  `mapContains(src.tags, ...)` AND (the existing `!=` / `NOT match`).
- Unit: `TestNameEqualityRemainsUnguarded` — `__name__` path does not get a
  `mapContains` wrap; it continues to target the typed column.
- Conformance: enable upstream Prom's negative-matcher-absent-label cases in
  the compliance harness — these should currently fail and pass after the
  fix. Verify: check
  `harness/compliance/prom-compliance/...` for the existing case inventory
  before landing.
- Golden: ensure the rendered SQL for every negative matcher test in
  `selector_sql_test.go` is updated in lockstep.
