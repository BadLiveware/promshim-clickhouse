# 01 — Regex matcher canonicalization

Rewrite `label=~"..."` and `label!~"..."` matchers whose regex is actually
equivalent to an equality, an IN-set, or a negated version of either. Every
regex today flows to ClickHouse as `match(col, '^(?:<value>)$')`, which is a
re2 walk that bypasses the MergeTree skip indexes and token bloom filters that
`=` / `IN` can leverage.

## Problem

A query like `up{job=~"api|worker|cron"}` produces, via
`selector_sql.go:427-430` and `matcherSQLPattern` (`selector_sql.go:436-446`):

```sql
match(src.tags[concat('', {instant_matcher_1_key:String})],
      {instant_matcher_1_value:String})
-- param_instant_matcher_1_value = "^(?:api|worker|cron)$"
```

Prometheus `parser` stores the matcher value as `api|worker|cron` (no anchors)
because Prom always treats it as fully anchored. The shim dutifully wraps
`^(?:...)$` even when the alternation is a pure literal set.

Same story for single-literal patterns: `job=~"api"` compiles to
`match(..., '^(?:api)$')` rather than `... = 'api'`.

The ClickHouse cost: `match` is opaque to tokenbf / set skip indexes and is
scanned row-by-row. `=` / `IN (…)` can hit any minmax/tokenbf/set index the
operator has placed on the tag projection columns, and can participate in
primary-key prefix analysis when the column happens to be a sort key member.

Verify: check whether the deployed TimeSeries tags sub-table carries any skip
indexes on `metric_name` or `tags`. The bootstrap SQL
(`chart/ch-observability-poc/files/clickhouse/init/.../001-observability-bootstrap.sql`)
relies on engine defaults, so the exact index set is whatever
`AggregatingMergeTree` pins. Even without skip indexes, the planner treats
`=`/`IN` constants as candidates for primary-key range narrowing; `match()` is
not.

## Current behavior

- `internal/promshim/storage/selector_sql.go:403-434` — `compileMatcherClause`
  chooses its SQL operator purely from `matcher.Type`. A `MatchRegexp` always
  lowers to `match(...)`; a `MatchNotRegexp` to `NOT match(...)`.
- `internal/promshim/storage/selector_sql.go:436-446` — `matcherSQLPattern`
  wraps every regex value with `^(?:...)$` unconditionally.
- `internal/promshim/native/optimizer.go:185-203` — `applyCommonMatcherInference`
  and `applyLabelPredicatePushdown` touch only the
  `__name__` equality; they never rewrite a regex matcher into a simpler form.

## Proposed technique

Add a canonicalization pass before `applyLabelPredicatePushdown` that walks
`selector.Matchers` and, for every `MatchRegexp` / `MatchNotRegexp`, attempts
the following normalizations (in order):

1. **Empty-alternative literal set.** Parse `matcher.Value` with
   `github.com/prometheus/prometheus/model/labels`'s
   `FastRegexMatcher` (already used by upstream Prom) or with
   `regexp/syntax`. If the AST is an `OpAlternate` whose children are all
   `OpLiteral` with no flags other than case-sensitive, extract the literal
   strings. Deduplicate.
   - If the set has exactly one element, emit a new matcher
     `MatchEqual`/`MatchNotEqual` with that value.
   - Otherwise, mark the matcher with a `canonicalSet` hint (new field on a
     storage-side wrapper, or carry it as a matcher-type tag in a parallel
     struct) so `compileMatcherClause` can emit `col IN (v1, v2, …)` /
     `col NOT IN (…)`.
2. **Single literal.** `foo` (no metacharacters, not a literal-set case) →
   `MatchEqual{Value:"foo"}`.
3. **`".*"` / `""`.** Tautology — see doc 02.
4. **`".+"`.** Equivalent to `label != ""`. Emit `MatchNotEqual{Value:""}`.

Anchor handling: upstream Prom stores regex values unanchored; the shim's
`matcherSQLPattern` adds the anchors. Any rewrite must happen before that
wrapping and must treat the matcher as fully anchored. If we decide to detect
`regexp/syntax` trees that begin with `^` or end with `$` (users can write
them, they're meaningless but allowed), we strip them first.

Verify: Prometheus's `labels.NewMatcher` compiles the regex with
`Regexp.Longest()`; the matcher struct exposes `.GetRegexString()` but the raw
value is also available as `.Value`. Working on `Value` is simpler and matches
the shim's current pipeline.

## Expected gain

- Single-literal rewrite: sequential `match()` scan → hash-based equality. On
  wide `tags` maps this collapses a re2 call per row into a single map lookup
  and string compare.
- Alternation rewrite: `col IN ('a','b','c')` participates in primary-key
  prefix pruning (critical for the `__name__` case — see doc 03) and in
  tokenbf / set skip indexes where present.
- `__name__` equality is the biggest win: `{__name__=~"cpu|mem|disk"}` is an
  extremely common shape (recording rules, dashboards). Today it scans the
  entire tags projection; after rewrite it can prefix-scan.

## Risk / PromQL semantics caveats

- Prometheus regexes are `RE2` with `fullmatch` semantics. The canonicalization
  must *preserve* full-match semantics: an `OpAlternate` with children
  `[OpLiteral("a"), OpLiteral("b")]` at the top level corresponds exactly to
  `IN ('a','b')` only because the surrounding anchors make it a full match. If
  the alternation has any non-literal leaf (e.g. `a|b.*`), we must not rewrite.
- UTF-8: `regexp/syntax` literals carry runes; join with `string(...)`.
- Case-insensitive flags (`(?i)`): Prom's default is case-sensitive. If the
  regex sets `FlagFoldCase` on any node, do not rewrite to equality — the
  resulting SQL would be case-sensitive.
- `[]` character classes over a finite alphabet (`[abc]`) could also canonicalize,
  but the win is modest and re2 handles it well; skip for the first cut.
- Empty alternative (`a||b`) means the empty string is a valid value. Translates
  to `col IN ('a','','b')`; must be preserved for Prom's `label=""` semantics
  (see doc 02).

## Implementation sketch

Place in a new pass, `PassMatcherCanonicalization`, ordered before
`PassCommonMatcherInference`:

```go
func canonicalizeMatcher(m *labels.Matcher) (*labels.Matcher, []string, bool) {
    if m.Type != labels.MatchRegexp && m.Type != labels.MatchNotRegexp {
        return m, nil, false
    }
    syn, err := regexpsyntax.Parse(m.Value, regexpsyntax.Perl)
    if err != nil {
        return m, nil, false
    }
    syn = stripOuterAnchors(syn) // ^ and $ are no-ops given full-match semantics
    if lits, ok := flattenLiteralAlternation(syn); ok {
        switch len(lits) {
        case 0:
            return m, nil, false // unreachable; parser rejects empty regex
        case 1:
            flip := labels.MatchEqual
            if m.Type == labels.MatchNotRegexp { flip = labels.MatchNotEqual }
            return labels.MustNewMatcher(flip, m.Name, lits[0]), nil, true
        default:
            return m, lits, true // caller emits IN / NOT IN
        }
    }
    return m, nil, false
}
```

`compileMatcherClause` grows a branch that, when given a literal-set hint,
emits:

```sql
src.metric_name IN ({k0:String}, {k1:String}, …)
```

binding each literal as its own `param_<prefix>_matcher_<i>_value_<j>` parameter.

The new pass lives in `optimizer.go` and mutates `selector.Matchers` in place
on the cloned fragment.

## Test coverage idea

- Unit: `TestCanonicalizeRegexpToEquality` — matcher `job=~"api"` rewrites to
  `MatchEqual`; verify emitted SQL contains `src.tags[...] = ...` and the
  param is `"api"` (no `^(?:...)$`).
- Unit: `TestCanonicalizeLiteralAlternationToIn` — `job=~"api|worker|cron"`
  produces an `IN (…, …, …)` clause with three bound params.
- Unit: `TestCanonicalizeMetricNameRegexp` — `{__name__=~"a|b"}` rewrites to
  `src.metric_name IN ('a','b')`, not a `match()` call (this is the prefix-scan
  enabler — see doc 03).
- Unit: `TestCanonicalizeDoesNotRewriteNonLiteral` — `job=~"api|.*_probe"`
  stays as `match()` (non-literal leaf).
- Unit: `TestCanonicalizeNotRegexp` — `job!~"api|worker"` rewrites to
  `NOT IN (…)`.
- Golden-SQL: extend
  `TestBuildInstantSelectorQuerySQLAnchorsRegexMatchers` with a canonicalized
  twin case.
- Integration (harness): ensure `up{job=~"a|b"}` returns the same series
  before/after the pass — pure semantics test, no performance assertion.
