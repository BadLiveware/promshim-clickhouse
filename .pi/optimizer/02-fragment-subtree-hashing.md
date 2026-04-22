# 02 — Fragment subtree hashing for CSE and plan memoization

Compute a structural hash of every `*NativeFragment` subtree keyed on
matchers + time bounds + lineage-stable fields. Use the hash two ways:
(a) detect identical subtrees within one query and render them once as a
`WITH` CTE joined twice; (b) memoize the entire optimized-fragment +
final SQL text across queries in a process-level LRU keyed on a
canonical plan fingerprint.

## Problem

`m{a="x"}[5m] / m{a="x"}[5m]` produces a binary-join fragment whose LHS
and RHS are structurally identical, yet the renderer lowers each side
independently — two full `groupArrayIf` scans of the same `timeseries`
rows, then a join back together. Real-world shapes with this property:

- `rate(http_requests_total[5m]) / rate(http_requests_total[5m] offset 1h)`
  (ratio vs. prior, shared selector base).
- Alerting rules computing `foo - foo offset 5m`.
- Binary operators with the same subexpression on both sides of `and`,
  `unless`, or division.

Separately, repeated identical queries (dashboard auto-refresh every
15 s, two replicas of the same ruleset) re-run all nine passes against
an identical logical plan every time. For the query above — 5 fragment
nodes — the pipeline today allocates roughly 20 fragment structs
(`BuildFragment` clone at `builder.go:23`, optimizer entry clone at
`optimizer.go:133`, plus two rebuild walks in
`normalizeTrivialSourceExpressions` / `flattenRedundantWrappers`) for a
result whose plan identity could be captured in a 64-bit hash.

## Current behavior

- `internal/promshim/native/optimizer.go:128-148` — `OptimizeFragment`
  clones on entry then runs nine passes.
- `internal/promshim/native/builder.go:23` — `BuildFragment` already
  clones the analysis-owned fragment.
- `internal/promshim/native/builder.go:26-107` — `CloneFragment` is a
  structural recursive copy with no identity preservation; two identical
  subtrees never share storage.
- Renderer (`internal/promshim/native/renderer/`) emits one SQL block
  per subtree; no CTE extraction for repeated subtrees.
- No cache exists between the Prom handler and the ClickHouse client;
  every `/query` or `/query_range` invocation rebuilds the fragment.

## Proposed technique

**Part A — Structural fragment hashing.** `FragmentHash(f) uint64`
folds every semantically load-bearing field into a 64-bit FNV-1a hash:

- `Kind`, `OutputKind`, `DropsMetric`, `ValueExpr`, `TagsExpr`.
- For `Selector`: `MetricName`, sorted `Matchers`
  (`Type|Name|Value`), `Lookback.Nanoseconds()`,
  `Offset.Nanoseconds()`, `StartOrEnd`, `Timestamp` (if set),
  `RequireFullTags`, sorted `RequiredTagLabels`. Exclude
  `InferredMatchers` / `PushedMatchers` — those are optimizer-derived
  and must not affect identity.
- For each typed pointer (`Aggregation`, `BinaryJoin`, `RangeFunction`,
  etc.): its scalar fields plus recursive hashes of its children.

Hashes are stable across `CloneFragment` because clone preserves every
hashed field. Memoize per-node on `map[*NativeFragment]uint64` in a
single post-order walk.

**Part B — Renderer-level CSE via CTEs.** Before SQL emission, walk the
hashed tree and for each hash that appears ≥ 2× with `OutputKind` in
`{InstantVector, RangeMatrix}` (scalars aren't worth CTE overhead),
materialize the subtree as a named CTE at the top of the query, replace
occurrences with CTE references:

```sql
WITH cse_0 AS (
  SELECT tags, groupArrayIf((ts, value), ts BETWEEN ? AND ?) AS time_series
  FROM timeseries
  WHERE metric_name = 'm' AND tags['a'] = 'x'
  GROUP BY tags
)
SELECT ... FROM cse_0 lhs ANY INNER JOIN cse_0 rhs ON lhs.tags = rhs.tags
```

**Part C — Process-level plan cache.** Key:

```
fingerprint = FNV64(
    canonicalPromQL(expr),  // whitespace normalized, parenthesized
    renderMode, stepMS,
    bucket(startMS, B), bucket(endMS, B), bucket(evaluationMS, B),
    schemaEpoch,
)
```

Value: `{sql, params, report}`. LRU with a byte-budgeted capacity (e.g.
4096 entries). Bucketing the time axes to e.g. 30 s absorbs Grafana's
10-s refresh into reusable cache entries — fragments with a
`HasFixedTemporalAnchor` (`types.go:288`) attach the resolved literal ms
instead of a bucketed one.

## Expected gain

- **Planner µs.** `OptimizeFragment` + render is typically 150–400 µs
  today; an LRU hit becomes a map lookup plus param rebind
  (sub-millisecond). For steady Grafana workloads with a 30 s bucket,
  target hit rate > 40%.
- **GC pressure.** Fragment structs (~200 B, 12 pointer fields) allocate
  in the tens of thousands per minute on an 8-panel dashboard with 10 s
  refresh; memoization removes nearly all of those on hit.
- **ClickHouse CPU.** CTE-based CSE is the bigger cardinality win: for
  `a - a offset 1h` we halve the `timeseries` read.

## Risk / caveats

- **Time-dependent folding.** Fragments that embed absolute timestamps
  (via `@ <timestamp>`, `start()`, `end()`) must cache on the resolved
  literal, not the symbolic marker. Use `HasFixedTemporalAnchor` to
  route — fixed anchors key on resolved ms; everything else keys on
  bucketed bounds.
- **Hash collisions.** FNV-1a is fine for in-process identity but not
  cryptographic. For CSE, verify structural equality on hash match
  (cheap — we already walked it). For the LRU, store canonical PromQL
  as a tiebreak string.
- **Schema invalidation.** Any change to `QueryConfig` (table, schema
  version) must invalidate. Attach a `schemaEpoch` to the key, bumped
  on config reload.
- **Matcher identity.** `mergeMatchers` (`optimizer.go:867`)
  constructs fresh `*labels.Matcher` on each pass. The hash must
  canonicalize on `Type|Name|Value`, not pointer identity. (See doc
  03 for matcher interning.)
- **CSE row-order assumptions.** Joins are keyed on `tags`, so double
  reads of one CTE are safe. A future renderer that pairs positionally
  must opt out.

## Implementation sketch

New file `internal/promshim/native/fragment_hash.go` with a
`FragmentHash(*NativeFragment) uint64`, plus per-field helpers
(`hashSelector`, `hashAggregation`, …) each feeding an `io.Writer`
wrapper around `fnv.New64a()`.

In the renderer, prepend a pass
`extractCommonSubexpressions(frag) (frag', []cteDef)` that returns the
rewritten fragment and the CTE definitions. The SQL emitter prepends
`WITH cseN AS (...)` blocks when `len(ctes) > 0` and emits CTE
references in place of deduplicated subtrees.

Process-level cache:

```go
type planCacheKey   struct { fingerprint uint64; canonical string; schemaEpoch uint32 }
type planCacheValue struct { sql string; params map[string]string; report *OptimizationReport }
var planCache = newLRU(4096)
```

`OptimizeFragment` checks the cache with the canonical PromQL + ctx; on
miss runs the pipeline and stores; on hit bypasses all nine passes.

## Test coverage idea

- Unit: `TestFragmentHashIsStableAcrossClone` — hash, clone, hash; equal.
- Unit: `TestFragmentHashIgnoresInferredMatchers` — mutating
  `InferredMatchers` / `PushedMatchers` must not change the hash.
- Unit: `TestFragmentHashDistinguishesTimeBounds` — `[5m]` vs. `[10m]`
  hash differently.
- Renderer: `TestCSEExtractsDuplicateRangeSelector` — assert
  `m{a="x"}[5m] / m{a="x"}[5m]` emits exactly one `groupArrayIf` and
  two `cse_0` references.
- Cache: `TestPlanCacheHitsOnRepeatedQuery` — same query with
  bucket-aligned eval ms skips the pass loop (verify via test counter).
- Cache: `TestPlanCacheMissOnEvaluationTimeBucketRollover` — eval ms
  crossing a bucket boundary misses.
- Cache: `TestPlanCacheInvalidatedOnSchemaEpoch` — bump epoch, prior
  entries unreachable.
- Harness: run compliance suite with cache on and off; results identical.
