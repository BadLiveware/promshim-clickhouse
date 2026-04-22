# 04 — Scalar-binary identity folding and bool-comparison simplification

Two related simplifications on the `FragmentKindBinaryScalarSourceExpr`
and `FragmentKindValueTransform` paths: fold algebraic identities
(`m + 0 → m`, `m * 1 → m`, `m * 0 → 0`, etc.) and collapse redundant
`if(...) 1 : 0` wrapping for `bool`-modifier comparisons when the output
is then thresholded again downstream.

## Problem

```promql
m{foo="bar"} + 0         -- identity: should be the same plan as m{foo="bar"}
1 * rate(http[5m])       -- identity: extra multiplication baked into the value expr
m - m                    -- always 0 (or NaN when m is absent); possibly foldable
m > bool 0.5             -- emits 1.0 / 0.0 via if(); fine in isolation, but see below
(m > bool 0.5) == bool 1 -- the outer bool-comparison is now a tautology on {0,1}
(m > bool 0.5) > 0       -- equivalent to `m > bool 0.5`; the > 0 re-filters
```

Each scalar arithmetic step today mints a `FragmentKindBinaryScalarSourceExpr`
(`analysis.go:179-187` and `:216-224`) or, when there's no base selector,
wraps the child in a `ValueTransformFragment`
(`analysis_binary.go:153-191`). The resulting SQL carries an extra
`(value) + 0.0` term — harmless numerically but wasted CPU per row and
a missed opportunity to collapse back to the original selector's
`ValueExpr = "{value}"` fast path. And because the optimizer's
`normalizeTrivialSourceExpressions` (`optimizer.go:242-280`) only
collapses `{value}`/`{tags}` wrappers that have `DropsMetric=false`, any
identity op that *does* drop the metric name (which ADD does, per
`applyBinarySourceTransform` `:160-163`) stays un-collapsed.

## Current behavior

- `foldBinaryScalarLiteral` (`analysis_constant.go:27-80`) handles
  `literal ⊕ literal` but not `vector ⊕ literal-identity`.
- `applyBinarySourceTransform` (`analysis_binary.go:153-191`) always
  emits `(value) ⊕ (scalar_literal)` regardless of whether the scalar
  is an identity element.
- `applyScalarValueTransform` (`analysis_binary.go:215-241`) similarly
  always wraps.
- `applyComparisonBoolTransform` (`analysis_binary.go:243-265`) emits
  `if(filter, 1.0, 0.0)` and sets `DropsMetric = true`. The resulting
  fragment's `ValueExpr` is `if(({value}) > 0.5, 1.0, 0.0)`.
- `applyComparisonFilterTransform` (`analysis_binary.go:267-294`) is
  the non-bool form: emits `FilterExpr = "(value) > 0.5"`, leaves
  `ValueExpr = "{value}"`.
- `normalizeTrivialSourceExpressions` only unwraps the exact
  `{value}` / `{tags}` identity on `FragmentKindUnarySourceExpr` —
  it never looks inside `BinaryScalarSourceExpr`.

The `ReturnBool` modifier case is particularly worth dissecting:

- `m > bool 0.5` → `applyComparisonBoolTransform` → a vector whose
  value is 1.0 or 0.0 per sample; metric name dropped. Label set
  preserved.
- `m > 0.5` (no bool) → `applyComparisonFilterTransform` → value
  unchanged, series filtered. Metric name preserved.

These are semantically different — the first produces 1/0 on every
input point, the second drops points entirely. That matters for
downstream functions like `count_over_time`, `sum`, aggregation
existence, etc. Any fast-path must preserve the distinction.

## Proposed technique

Three orthogonal folds, applied in `PassTrivialExpressionNormalization`
(already the earliest fragment-layer pass) so the later passes see the
canonical form:

### 4A — Scalar arithmetic identities

Detect and short-circuit when the scalar operand is an identity element
for the operator:

| Operator | Identity condition | Fold to |
|----------|--------------------|---------|
| `ADD` | scalar == 0 | child |
| `SUB` | scalar == 0 and scalar-on-right | child |
| `MUL` | scalar == 1 | child |
| `MUL` | scalar == 0 | synthetic-literal 0 (vector×0) **see caveat** |
| `DIV` | scalar == 1 and scalar-on-right | child |
| `POW` | scalar == 1 and scalar-on-right | child |
| `POW` | scalar == 0 and scalar-on-right | synthetic-literal 1 **see caveat** |

"Fold to child" means: replace the `BinaryScalarSourceExpr` fragment
with its wrapped child fragment, carrying through the child's
`ValueExpr`, `TagsExpr`, and crucially *without* setting
`DropsMetric = true`. `ADD`/`SUB` normally set `DropsMetric` because
they're arithmetic on a metric series, but the identity result is
semantically the original series — metric name should survive.

**Caveat on `m * 0` and `m ** 0`:** these are only safe if `m` cannot
produce NaN. NaN × 0 = NaN in IEEE 754 (and PromQL inherits this).
Folding `m * 0` to the constant 0 would turn NaN samples into 0, which
is wrong. Either gate the fold behind a "no-NaN" flag (we don't have
that analysis yet) or skip it. Recommendation: skip for now, revisit
when we have a NaN-freedom lattice on `ValueExpr`.

### 4B — Vector self-subtraction and self-division

`m - m` and `m / m` pattern-match on `LogicalBinaryPlan` where both
sides resolve to the same `SelectorSource` under the same matchers
(after canonicalization) and the join is identity-matching
(one_to_one with no `on`/`ignoring`, or `on(...)` covering every label).
Foldable results:

- `m - m` → a one-per-series zero, *except where `m` is NaN* → yields NaN.
- `m / m` → one-per-series 1.0, *except for NaN → NaN and 0 → NaN*.

These are rare enough in practice that the complexity probably isn't
worth it. Recommendation: **do not implement** in the first cut.
Document it here so the reasoning is preserved.

### 4C — `bool`-comparison simplification

Detect `(X > bool Y) ⋈ Z` where `⋈` is another `bool`-comparison
against a literal `Z` that's outside `{0, 1}`:

- `(X > bool 0.5) == bool 2` → synthetic 0 (tautologically false).
- `(X > bool 0.5) != bool 2` → synthetic 1 (tautologically true).
- `(X > bool 0.5) < bool 0` → synthetic 0.
- `(X > bool 0.5) >= bool 0` → synthetic 1 (with caveats around NaN:
  the inner `bool` comparison treats a NaN input as ... let's check:
  `if(isNaN({value}) ... > 0.5, 1.0, 0.0)` — actually the current
  emitter uses raw `>` (`comparisonFilterTemplate` at
  `analysis_binary.go:296-310`), which for NaN produces 0. So the
  result is always in `{0, 1}`, no NaN leak. The fold is safe.)

More usefully: `(X ⋈ bool C) > 0` is equivalent to the original
`X ⋈ C` (without bool) as a filter. Detect that and collapse. Concretely:

- Input: `ValueTransformFragment { ValueExpr: "if(({value}) > 0.5, 1.0, 0.0)", DropsMetric: true }` wrapped inside another `ValueTransformFragment { FilterExpr: "(value) > 0" }`.
- Output: single `ValueTransformFragment { FilterExpr: "(value) > 0.5" }` with metric-name preservation flipped back on.

This is a peephole on the `FragmentKindValueTransform` shape — detect
a specific pair of wrappers and compose them.

## Expected gain

- 4A — small per-row CPU savings on ClickHouse, bigger gain is plan
  simplification: `m + 0` re-becomes a pure selector, which re-opens
  all the downstream passes that look for "is this a bare selector?"
  (projection pushdown, late materialization). Qualitatively: users
  writing `foo + 0` or `rate(foo[5m]) * 1` — common in dashboards
  assembled by templating — get the same performance as writing the
  clean form.
- 4B — intentionally skipped.
- 4C — two stacked `ValueTransform` wrappers collapse to one, which
  removes an entire SQL subquery level in the rendered output (see
  `renderValueTransformFragment` at `renderer/join.go:208-299` — each
  transform becomes a nested SELECT).

## Risk / PromQL semantics caveats

- **Metric-name preservation.** `m + 0 → m` must *not* drop `__name__`.
  Today `applyBinarySourceTransform` sets `dropsMetric=true` for ADD
  (`analysis_binary.go:161-163`); the fold branch needs to override to
  keep the child's `DropsMetric` flag. Equivalent care for `MUL` with
  scalar 1.
- **NaN semantics for MUL/POW with zero:** skip these folds (see 4A
  caveat). They can be revisited once we have NaN-freedom tracking on
  `ValueExpr` — e.g., a `{value}`-only template on a leaf selector
  over non-histogram data is typically NaN-free, but
  `rate()` / `irate()` can legitimately produce NaN on undefined
  windows.
- **Integer vs float literal.** Literal 1 in PromQL is always float64.
  The folds compare with `==`, which is exact for integral floats.
  Edge case: `m + 1e-300` technically isn't an identity, but bit-exact
  comparison doesn't fire. Only fold on literal 0 and 1.
- **`ReturnBool` output semantics.** The 4C fold must preserve the
  boolean output-set distinction: the input to the outer comparison is
  a 0/1 vector, so any threshold check is decidable. But the *output*
  of the combined form differs: `(m > bool 0.5)` keeps all series (with
  value 0 or 1), whereas `m > 0.5` as a *filter* drops series where
  every sample is ≤ 0.5. The 4C fold from
  `(m > bool C) > 0` to `m > C` changes whether all-zero series survive
  — originally yes (as value 0), after fold no. Only fire 4C when the
  caller's context makes the distinction irrelevant: e.g., feeding
  into `sum()`, which treats absent the same as 0-valued via
  `EmitZeroOnEmpty` semantics. A safe first-cut: do *not* implement
  4C's `(> bool) > 0` rule. Limit 4C to the tautology cases where
  the result is a constant.
- **Label lineage.** Folding `m + 0` to `m` restores the metric-name
  to the lineage. `nativeVectorJoinLabelLineage` isn't involved since
  no join is present, but the fragment's `LabelLineage` on the parent
  `LoweringInfo` must be recomputed — easy enough because the replaced
  fragment is the child fragment whose lineage is already known.
- **Scalar fragments.** `scalar(m) + 0` is still a scalar; the fold
  from `BinaryScalarSourceExpr` back to child must carry the
  `OutputKind` through unchanged.

## Implementation sketch

Extend `normalizeTrivialSourceExpressions` (`optimizer.go:242-280`)
with a new arm that fires on `FragmentKindBinaryScalarSourceExpr`:

```go
if normalized.Kind == FragmentKindBinaryScalarSourceExpr {
    if child := identityFoldBinaryScalar(normalized); child != nil {
        return child
    }
}
```

Where `identityFoldBinaryScalar` inspects the fragment's
`SourcePromQL` (a `*parser.BinaryExpr`) to recover the operator and
scalar operand, matches against the identity table above, and returns
the child fragment extracted from the pre-fold representation.

But actually that's awkward because the fragment no longer holds the
pre-folded child explicitly — `BinaryScalarSourceExpr` *is* a flat
selector fragment with the scalar inlined into `ValueExpr`. Two
options:

1. **Detect identity at analysis time**, not post-hoc. In
   `analysis.go:172-245` (the `lhsIsScalar` / `rhsIsScalar` branches),
   short-circuit before calling `applyBinarySourceTransform` when the
   scalar is an identity element. Emit the child fragment directly,
   preserving `DropsMetric` as it was in the child.
2. **Keep a tag on the fragment** so the normalizer can unwind. More
   invasive.

Recommendation: option 1. Add a helper
`identityOperandForBinary(op, scalar, scalarOnLeft)` returning
`(isIdentity, preservesMetricName bool)`, call it in the four scalar
arms of the `LogicalBinaryPlan` case, and skip the wrapping entirely
when identity.

For 4C tautology folds, add a peephole in
`normalizeTrivialSourceExpressions` that detects the
`ValueTransform` with `ValueExpr == "if(..., 1.0, 0.0)"` wrapped by a
`ValueTransform` whose `FilterExpr` or bool-comparison against a
constant outside `{0,1}`. Replace with the appropriate synthetic
literal.

## Test coverage idea

- Unit table (analysis layer): for each operator ⊕ identity combo in
  the 4A table, feed `m ⊕ <identity>` / `<identity> ⊕ m` and assert
  the resulting fragment kind is the child's kind (typically
  `FragmentKindLeafSource`) and `DropsMetric == false`.
- Unit: `m * 0` returns `BinaryScalarSourceExpr` unchanged (NaN
  safety gate).
- Unit: `m + 0.0`, `m + 0`, `m + (-0)` all fold (float literal
  normalization).
- Golden SQL: snapshot `SELECT value` where today's output is
  `SELECT (value) + 0.0` — confirm the identity is gone.
- Unit for 4C tautology: `(m > bool 0) == bool 5` returns a
  `SyntheticSeriesFragment` with value 0.
- Conformance: any dashboard query of the form `foo + 0` —
  byte-identical to `foo` from Prometheus.
- Regression: `rate(m[5m]) * 1` — confirms the identity fold applies
  *through* a `RangeFunction` child, preserving `DropsMetric=true`
  set by rate itself.
