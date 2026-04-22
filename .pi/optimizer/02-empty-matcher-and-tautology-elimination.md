# 02 — Empty-matcher and tautology elimination

Normalize PromQL matchers whose semantic is "trivially true", "trivially
false", or redundant with another matcher in the same selector. Drop tautologies
from the emitted SQL; short-circuit selectors whose matchers are mutually
exclusive; fold `label!=""` and `label=~".+"` into a single canonical form so
the rest of the pipeline sees one shape.

## Problem

Four shapes show up regularly and all reach ClickHouse today as full regex
calls or as separate predicates:

1. `up{region=~".*"}` — tautology (always true on present labels; PromQL
   additionally treats missing labels as empty string which `.*` matches, so
   this is *universally* true). Emits:
   ```sql
   match(src.tags[concat('', {..._key:String})], {..._value:String})
   -- param = "^(?:.*)$"
   ```
   That's an unconditional re2 call per row.

2. `up{label=""}` — Prometheus semantics: matches series that *do not have*
   `label`, or have it set to empty. Today this goes through
   `compileMatcherClause` as
   `src.tags[concat('', {key:String})] = ''`. ClickHouse `Map` returns the
   type's default for a missing key; for `Map(String, String)` the default is
   `''`, so this happens to work — but only by accident. Verify: confirm
   behavior of `Map(String, String)` subscript for a missing key (docs say
   "default value of the value type"). If true, the existing SQL is
   semantically correct; if not, this is a latent bug.

3. `up{label!=""}` and `up{label=~".+"}` — same semantic ("label present with
   non-empty value"). Today the first compiles to
   `src.tags[...] != ''` and the second to `match(..., '^(?:.+)$')`. One is
   cheap, one is expensive, and nothing folds them.

4. Conflicting matchers. `up{env="prod", env="dev"}` is accepted by the Prom
   parser but is unsatisfiable; the shim emits both predicates and lets
   ClickHouse run a guaranteed-empty scan.

## Current behavior

- `internal/promshim/storage/selector_sql.go:370-381` — every matcher in
  `selector.Matchers` reaches `compileMatcherClause` without inspection of its
  value.
- `internal/promshim/storage/selector_sql.go:422-434` — no fast paths for
  `".*"` or `""`.
- `internal/promshim/native/optimizer.go:185-203` — neither inference pass
  filters, merges, or detects conflicts. `mergeMatchers` (line 867) only
  de-duplicates on `matcher.String()` equality.

## Proposed technique

Add a pass `PassMatcherSimplification` between
`PassMatcherCanonicalization` (doc 01) and `PassCommonMatcherInference`. It
runs after canonicalization so it only has to reason about `=` / `!=` /
`match` / `not match` (regex rewrites already applied).

Rules, applied iteratively until fixed point on `selector.Matchers`:

1. **Drop `=~".*"`**: for any matcher with `Type=MatchRegexp` and
   `Value==".*"`, drop. (Post-canonicalization the literal-alternation rewrite
   wouldn't fire on `.*`; this rule handles it.) Also drop any matcher whose
   regex AST reduces to `OpAnyCharNotNL*` / `OpAnyChar*` (i.e. functionally
   `.*`).
2. **Drop `!~""`** (same as `!= ""` below) — emitted rarely but harmless.
3. **Fold `!=""` and `=~".+"` into a canonical form.** Pick one internal shape
   (suggest `!=""`, since it compiles to a plain `!=`) and rewrite both to it.
   This keeps the conflict-detection logic simpler.
4. **Detect conflicts**:
   - Two `MatchEqual` on the same `Name` with different `Value` → unsatisfiable.
   - `MatchEqual{label=x}` + `MatchNotEqual{label=x}` same value →
     unsatisfiable.
   - `MatchEqual{label=x}` + `MatchRegexp{label, value}` where the canonical
     set from doc 01 is non-empty and does not contain `x` → unsatisfiable.
   - On conflict: mark the fragment as a short-circuit (e.g. a
     `FragmentKindEmpty` or flip an `UnsatisfiableSelector` bool); the SQL
     renderer emits `WHERE 0` or returns a zero-row source. This is a
     cross-cutting concern — see the "Implementation sketch" below.
5. **Deduplicate identical matchers.** `mergeMatchers` already does this on
   `String()`; extend it to recognize the canonical forms so
   `{a=~".+"} + {a!=""}` dedupe to one matcher.

## Expected gain

- `.*` rewrites: the query loses an entire `match()` call per row, and the
  `__name__`-first prefix-scan heuristic (doc 03) is not blocked by a
  wildcard matcher that follows it. Common in dashboards that generate
  matchers from a template variable when "All" is selected.
- `!=""` fold: lets equality-path optimizations (e.g. skip-index
  participation) kick in on what is currently a regex call.
- Conflict detection: produces a trivially-empty SQL without touching
  ClickHouse's scan — wins are full query cost. Rare, but free.
- Simpler code downstream: doc 04's negative-matcher analysis assumes one
  canonical "label must be present" form.

## Risk / PromQL semantics caveats

- **Absent-label semantics.** `label=""` matches series that don't carry the
  label *and* series that carry it with empty value. The current SQL works
  because ClickHouse `Map` subscript returns `''` for missing keys. Do **not**
  drop or rewrite the `label=""` matcher — it's load-bearing, even though it
  looks tautological at a glance.
- **`=~".*"` technically matches everything including missing labels** — same
  reason: `Map[missing]` returns `''` and `.*` matches `''`. So dropping it is
  semantically safe.
- **`!=""` vs `=~".+"` equivalence is NOT absolute in PromQL** — `=~".+"`
  requires the label to be present with a non-empty value; so does `!=""`
  *except* for PromQL's "missing label is empty string" rule. Both reject
  missing labels only when compared against non-empty. Since the shim's map
  lookup returns `''` for missing, both predicates already filter out missing
  labels. Equivalence holds for our storage.
- **Conflict short-circuit must not elide side effects.** There are none at the
  selector level, so this is safe.
- **Regex `.+` matches multi-line strings.** `=~".+"` with a newline-containing
  tag value: Prom re2 default `FlagFold` settings match `.` against everything
  except newline unless `(?s)` is set. In practice tag values don't contain
  newlines; but when canonicalizing to `!=""` we broaden slightly (any
  non-empty string, newline or not). Verify whether the shim rejects such
  values elsewhere; in the Prom wire format, label values may technically
  include newlines.

## Implementation sketch

```go
func simplifyMatchers(ms []*labels.Matcher) (kept []*labels.Matcher, unsat bool) {
    seen := map[string]*labels.Matcher{}
    for _, m := range ms {
        // rule 1: drop =~".*"
        if m.Type == labels.MatchRegexp && regexIsDotStar(m.Value) {
            continue
        }
        // rule 3: fold =~".+" into !=""
        if m.Type == labels.MatchRegexp && regexIsDotPlus(m.Value) {
            m = labels.MustNewMatcher(labels.MatchNotEqual, m.Name, "")
        }
        // rule 4: conflict detection per (name, type, value)
        key := m.Name + "|" + m.Type.String() + "|" + m.Value
        if _, ok := seen[key]; ok { continue }
        if conflictsWith(seen, m) { return nil, true }
        seen[key] = m
        kept = append(kept, m)
    }
    return kept, false
}
```

`regexIsDotStar` / `regexIsDotPlus` parse with `regexp/syntax` and check for
the AST shapes `.*` and `.+` (treat anchors already stripped). For the
unsatisfiable case, surface it through a new field on `SelectorSource` (e.g.
`Unsatisfiable bool`); both `selector_sql.go:buildMatchedSeriesSQL` and the
outer fragment builders can check it and emit `WHERE 0 = 1` (keeps the SQL
parse-valid and the parameter bindings intact).

## Test coverage idea

- Unit: `TestSimplifyDropsDotStarRegex` — `foo{region=~".*"}` yields selector
  with only `__name__=foo` matcher; rendered SQL contains no `match()` call.
- Unit: `TestSimplifyFoldsDotPlusToNotEmpty` — `foo{region=~".+"}` yields
  `region != ''`; param is empty string, not `^(?:.+)$`.
- Unit: `TestSimplifyPreservesLabelEquEmpty` — `foo{region=""}` keeps the
  matcher; SQL emits `src.tags[...] = ''` and returns the documented Prom
  absent-label semantics.
- Unit: `TestSimplifyDedupesEquivalentPresencePredicates` —
  `foo{region!="", region=~".+"}` collapses to one matcher.
- Unit: `TestSimplifyDetectsEqualityConflict` — `foo{env="prod", env="dev"}`
  marks the selector unsatisfiable; emitted SQL contains `0 = 1` in the
  matched-series WHERE.
- Integration (harness): verify a query with `{x=~".*"}` and without it
  produce identical result sets on a representative dataset.
