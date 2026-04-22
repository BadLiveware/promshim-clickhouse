# 03 — Metric-name-first filtering and ORDER BY leverage

Emit the `__name__` equality as an un-adorned, parameter-bound predicate on the
typed `metric_name` column — first in the `WHERE`, before any tag predicates —
so the tags sub-table's primary-key prefix can narrow the scan. Identify when
the metric-name predicate is regex (doc 01's canonicalization emits an `IN`
list in the common case) so that still holds.

## Problem

For `up{job="api"}` the shim emits (via
`buildMatchedSeriesSQL`, `selector_sql.go:352-401`):

```sql
SELECT src.id, <tags expr> AS tags
FROM timeSeriesTags(`observability`.`prometheus`) AS src
WHERE src.metric_name = {instant_matcher_0_value:String}
  AND src.tags[concat('', {instant_matcher_1_key:String})]
        = {instant_matcher_1_value:String}
  AND src.max_time >= fromUnixTimestamp64Milli({required_start_ms:Int64})
  AND src.min_time <= fromUnixTimestamp64Milli({required_end_ms:Int64})
```

Already good: `metric_name` is a typed column, `=` is used, predicate is
first. But two related questions drive this proposal:

1. **Is `metric_name` the first key in the tags projection's ORDER BY?** The
   deployed schema uses ClickHouse's TimeSeries engine with default sub-tables
   (`001-observability-bootstrap.sql`) and does not override the tags engine's
   sort key. Verify: the default sort key for `timeSeriesTags` in the
   `AggregatingMergeTree` sub-table. If it is `(metric_name, id)` or
   `(metric_name, ...)` then our equality unlocks prefix-scan; if it is
   `(id)` only (id-based), the equality is still useful but doesn't unlock
   prefix-scan and we should instead rely on skip indexes.
2. **What happens when `__name__` arrives as a regex?** PromQL allows
   `{__name__=~"cpu.*|mem.*"}`. Today doc 01's canonicalization (if landed)
   rewrites literal alternations to `IN`, which is prefix-scan friendly;
   non-literal regexes fall through to `match(src.metric_name, '^(?:...)$')`
   which defeats prefix-scan entirely. The shim should at least detect the
   common-prefix case (`cpu.*|memo.*`) and emit a disjunction of prefix
   predicates (`startsWith(src.metric_name, 'cpu') OR
   startsWith(src.metric_name, 'memo')`) because `startsWith` on a
   sort-key-prefix column *is* prefix-scan friendly.

## Current behavior

- `internal/promshim/storage/selector_sql.go:352-401` — `buildMatchedSeriesSQL`
  assembles `whereClauses` starting with the synthesized metric-name matcher
  (when `selector.MetricName != ""`) followed by tag matchers in the order
  they appear in the PromQL selector.
- `internal/promshim/storage/selector_sql.go:406-408` — `compileMatcherClause`
  special-cases `matcher.Name == labels.MetricName` to target
  `src.metric_name` directly; any other name goes through `src.tags[...]`.
- `internal/promshim/native/optimizer.go:499-508` — `inferSourceMatchers`
  infers the `__name__=<selector.MetricName>` matcher but *does not* turn an
  existing `__name__=~<pattern>` matcher into anything friendlier.

## Proposed technique

Two complementary moves:

### A. Guarantee metric-name predicate is emitted first, typed-column, `=` or `IN`

Already mostly the case. Tighten by:

1. In `buildMatchedSeriesSQL`, always emit the metric-name predicate at
   position zero. When `selector.MetricName != ""`, that's what happens today
   (line 360-369). When `selector.MetricName == ""` (bare `{job="api"}`), the
   Prom parser stores the name-free case by leaving `MetricName` empty but
   there may still be `__name__=~"..."` in `selector.Matchers` — pull it to
   the front before emitting tag matchers.
2. When the fragment's inferred matcher
   (`selector.InferredMatchers`, set by `applyCommonMatcherInference`) carries
   a `__name__` equality not present in `selector.Matchers`, ensure it reaches
   `buildMatchedSeriesSQL` (the code currently only consumes
   `selector.Matchers` and `selector.MetricName`, not `PushedMatchers`).

### B. Rewrite `__name__=~"..."` into a prefix-scan-friendly predicate when possible

Beyond doc 01's literal-alternation → `IN` rewrite, add a narrower rule for
`__name__`: when the regex AST is an alternation of literal prefixes followed
by a wildcard tail (`cpu.*|memo.*`, very common in `{__name__=~"http_.*"}`),
emit:

```sql
(startsWith(src.metric_name, 'cpu') OR startsWith(src.metric_name, 'memo'))
```

`startsWith` on the sort-key prefix column is known to be mark-level pruneable
in ClickHouse (verify: check that the version in the chart's ClickHouse image
handles `startsWith` in primary-key analysis; this has been true since ~21.8
but confirm against the exact deployed version).

Keep the original regex as a backup predicate only when there's any chance the
prefix decomposition is not exactly equivalent (e.g. when the tail is not
`.*`). In practice `__name__=~"prefix.*"` is the shape we can fully rewrite;
anything else stays as regex.

### SQL shape we want vs. what we emit today

For `{__name__=~"http_.*"}`:

Today:
```sql
WHERE match(src.metric_name, '^(?:http_.*)$')
  AND src.max_time >= ... AND src.min_time <= ...
```

Proposed:
```sql
WHERE startsWith(src.metric_name, 'http_')
  AND src.max_time >= ... AND src.min_time <= ...
```

For `up{job="api"}`:

Today (unchanged, already optimal once the metric name is also anchored and
the schema is verified):
```sql
WHERE src.metric_name = 'up'
  AND src.tags['job'] = 'api'
  AND ...
```

The open question is whether the deployed tags sub-table actually uses
`metric_name` as the first sort key. If it does not, either (a) we should
recommend a schema change that adds `metric_name` to the front of the sort
key, or (b) rely on a skip index on `metric_name` — which is likely present
by default in `timeSeriesTags` but verify.

## Expected gain

- **Prefix-scan pruning**: selector evaluation converts from a full tags-table
  scan to a contiguous mark range read. Order-of-magnitude cost reduction on
  large tenants.
- **startsWith rewrite**: `{__name__=~"http_.*"}` shrinks from a regex call
  per row to a byte-prefix compare, which the MergeTree planner can evaluate
  without reading the data part.
- These wins compound with doc 01 (literal alternation → `IN`) and with
  selector-side projection pushdown that already runs.

## Risk / PromQL semantics caveats

- **Anchoring equivalence.** Prometheus regex is fully anchored. `http_.*` is
  `^http_.*$`, which matches anything starting with `http_`. `startsWith` is
  exactly that. `http_.+` requires at least one char after the prefix;
  `startsWith(…, 'http_') AND length(src.metric_name) > 6`.
- **Metric-name regex that actually needs regex.** `__name__=~".*_total"` is
  a *suffix* match. `endsWith` works but does NOT participate in primary-key
  prefix-scan (it's evaluated row-by-row). Do not rewrite; keep the `match`
  call.
- **Empty regex / `.*`**: handled by doc 02 (tautology). If it reaches this
  pass, it should already be dropped.
- **Mixed selectors.** `{__name__=~"cpu.*", instance=~"srv-.*"}` — we
  canonicalize the name and leave the instance as regex; still a net win.
- **Do not rewrite `__name__!~` as negative-prefix.** `NOT startsWith(...)`
  over-matches: it includes series whose name is empty, which Prom regex
  anchors would not match. Prom semantics say `__name__` is always present
  (internal name), so this is probably safe; verify.
- **Schema assumption.** If `timeSeriesTags` does not sort on
  `metric_name` first, the `startsWith` rewrite still wins over `match()`
  because tokenbf / set skip indexes handle `startsWith` better than `match`;
  but the largest gain (prefix prune) requires the sort key. Verify before
  claiming the big win.

## Implementation sketch

In the same pass as doc 01 (or an immediately-following one,
`PassMetricNamePrefix`):

```go
func rewriteMetricNameRegex(m *labels.Matcher) *labels.Matcher {
    if m.Name != labels.MetricName { return m }
    if m.Type != labels.MatchRegexp && m.Type != labels.MatchNotRegexp { return m }
    if prefix, ok := literalPrefixOnly(m.Value); ok {
        // marker: emit startsWith in compileMatcherClause
        return &labels.Matcher{
            Type:  m.Type, Name: m.Name, Value: prefix,
            // carry the hint through a parallel map keyed by matcher identity
        }
    }
    return m
}
```

Because `labels.Matcher` has no extension point for op kind, we route the
"prefix" hint through a side-band map held in the optimizer state and consumed
by `compileMatcherClause`. Alternatively, introduce a private matcher wrapper
in the storage package:

```go
type MatcherOp int
const ( OpEq MatcherOp = iota; OpNeq; OpMatch; OpNotMatch; OpIn; OpStartsWith )

type CompiledMatcher struct {
    Op    MatcherOp
    Name  string
    Values []string // single for Eq/Neq/Match/NotMatch/StartsWith, multiple for In
}
```

and have the optimizer produce `[]CompiledMatcher` instead of
`[]*labels.Matcher`. This is cleaner long-term and unlocks docs 01 and 04 too.

Also tighten ordering in `buildMatchedSeriesSQL`:

```go
// Emit metric-name predicates first, tag predicates second.
sort.SliceStable(matchers, func(i, j int) bool {
    return matchers[i].Name == labels.MetricName && matchers[j].Name != labels.MetricName
})
```

## Test coverage idea

- Unit: `TestMetricNameRegexBecomesStartsWith` — `{__name__=~"http_.*"}`
  emits `startsWith(src.metric_name, 'http_')`.
- Unit: `TestMetricNameRegexLiteralAlternationBecomesIn` (shared with doc 01)
  — `{__name__=~"a|b|c"}` emits `IN`.
- Unit: `TestMetricNameSuffixRegexPreserved` — `{__name__=~".*_total"}` stays
  as `match(...)`.
- Unit: `TestMetricNameFirstInWhere` — for
  `foo{a="1", __name__="foo", b="2"}` (pathological but allowed), the
  `__name__ =` predicate appears before `src.tags[...]` predicates.
- Golden: a compliance-harness query with a prefix regex; compare row counts
  and note plan change via `EXPLAIN` (harness already runs against
  clickhouse-local; verify EXPLAIN output reports a mark-range reduction).
- Integration: `verify:` produce an `EXPLAIN` of a representative query
  against the harness to confirm primary-key analysis narrows ranges after
  the rewrite. Cite the exact ClickHouse version.
