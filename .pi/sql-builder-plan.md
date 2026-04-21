# ClickHouse SQL builder — retrofit plan

## Motivation

Today the native renderer builds SQL via `fmt.Sprintf` templates,
`strings.ReplaceAll("{value}", ...)` substitution, and per-fragment
ad-hoc builders. Concretely:

- `internal/promshim/storage/sql.go` — instant selector and aggregation
  builders, each a format string with nested subqueries inlined
- `internal/promshim/storage/join_sql.go` — binary-join assembly with
  string concatenation for operator splicing
- `internal/promshim/native/renderer.go` — fragment dispatch and
  source-wrapper queries, plus range-function value-expression assembly
  via nested `fmt.Sprintf` on array-function strings
- parameter namespacing handled by string replacement across layers

This scales badly once the nesting grows past two levels. The coverage
plan's **windowed-arrays source** plus `predict_linear`,
`mad_over_time`, and `double_exponential_smoothing` all want a shape
like:

```
outer multiIf(
  length(window_values) < 2, NULL,
  <constant-series short-circuit>,
  <tupleElement(<arrayReduce | arrayFold>(... window_values ...), k) * ...>
)
```

Implementing that three-layer shape three times as `fmt.Sprintf` on
`fmt.Sprintf` on `fmt.Sprintf`, with `{value}` substitution hopping
between layers, is the thing this plan is meant to prevent.

## Scope — retrofit

Introduce a small ClickHouse SQL builder package (default name
`internal/promshim/native/sqlb`) and **replace** the existing string
builders in:

- `internal/promshim/storage/sql.go`
- `internal/promshim/storage/join_sql.go`
- `internal/promshim/native/renderer.go` (fragment dispatch and
  source-wrapper paths)

No `fmt.Sprintf` for SQL emission outside the builder package when the
retrofit is done. No `strings.ReplaceAll("{value}", ...)` substitution.
Parameter binding flows through the builder's parameter registry.

The local evaluator (`internal/promshim/exec/`) and the delegation
path (direct `/api/v1/...` proxy) are untouched.

## Non-goals

- not a parser — emission only. ClickHouse's surface is large; we
  cover exactly what the renderer emits
- not a query optimizer — node ordering and simplification stays in
  the planner / Fragment-IR layer
- not a schema modeler — callers still pass table names, column
  names, and type strings as strings
- not a general-purpose library — it lives under `internal/promshim/`
  on purpose and is not intended for reuse outside the shim

## Design sketch

### Node surface (minimum viable)

```go
// internal/promshim/native/sqlb/sqlb.go
package sqlb

type Expr interface { sql(*ctx) }

type Ident    string              // unquoted ident, caller-escaped
type QIdent   string              // backtick-quoted identifier
type Lit      struct{ V any; Type string } // → {pN:Type} bound param
type RawLit   struct{ V string }  // literal SQL (e.g. CAST('nan' ...))
type Call     struct{ Name string; Args []Expr }
type AggCall  struct{ Name string; State []Expr; Args []Expr } // quantileExact(0.5)(x)
type ArrayRed struct{ Agg string; Args []Expr } // arrayReduce('name', ...)
type Binary   struct{ Op string; L, R Expr }
type Unary    struct{ Op string; X Expr }
type Tuple    struct{ Elems []Expr }
type Subscr   struct{ Array Expr; Index Expr } // array[i]
type Lambda   struct{ Params []Ident; Body Expr }
type ArrayFold struct{ Lambda Lambda; Src Expr; Init Expr }
type Map      struct{ Lambda Lambda; Arrays []Expr } // arrayMap
type MultiIf  struct{ Cases []MultiIfArm; Else Expr }
type Case     struct{ Cases []CaseArm; Else Expr }
type Cast     struct{ X Expr; To string }
type Param    struct{ Name, Type string } // {name:Type} placeholder
type TupleElem struct{ X Expr; K int }
```

### Queries

```go
type Select struct {
    With    []CTE
    Columns []ColExpr           // (Expr, alias)
    From    Source              // table, ARRAY JOIN, or *Select
    Where   Expr
    GroupBy []Expr
    Having  Expr
    OrderBy []OrderExpr
    Limit   *Limit
}

type Source interface { source(*ctx) }
type Table      struct{ DB, Name, Alias string }
type SubSelect  struct{ S *Select; Alias string }
type ArrayJoin  struct{ Base Source; Expr Expr; Alias string; Left bool }
```

### Emission contract

```go
func (s *Select) Build() (sql string, params map[string]string, err error)
```

Matches the existing `storage.Build*()` shape so retrofit is
drop-in. Parameters are auto-numbered (`{p0:Float64}`, `{p1:Int64}`,
…) unless the caller wants a stable name (`{duration:Float64}`,
`{metric:String}`) — both supported via `Lit{V, Type}` vs
`Param{Name, Type}`.

### Composition

Nesting is structural: `Source` accepts `*Select`, so the
windowed-arrays source becomes a `*Select` that predict_linear /
mad_over_time / holt_winters each wrap without string splicing.

```go
src := windowedArraysSelect(opts)   // *Select, shared
pred := &Select{
    Columns: []ColExpr{
        {Expr: predictLinearValueExpr(src.Columns), Alias: "value"},
        ...
    },
    From: &SubSelect{S: src},
}
```

No `{value}` substitution, no format-string hopping.

## Phases

Each phase ships with golden SQL tests comparing the builder's output
to the pre-retrofit output on a curated query set (whitespace-
normalized, not byte-equal), and the native harness stays green at
every step.

1. **Package skeleton** — node types, `Select`, `Build()`, param
   registry, whitespace-normalized golden-SQL test helper. Unit
   tests emit a handful of hand-written fixtures covering the node
   surface.

2. **Retrofit instant selector** — replace
   `storage.BuildInstantQuerySQL`. Smallest builder, highest usage;
   proves the drop-in contract.

3. **Retrofit aggregation** —
   `storage.BuildInstantAggregationQuerySQLWithBounds` and siblings.
   First place nested subqueries show up; also first place the
   `{value}` substitution pattern dies.

4. **Retrofit binary join** — `storage/join_sql.go`. Most complex
   existing builder; operator splicing collapses into `Binary`
   nodes.

5. **Retrofit range-function wrapper** —
   `native/renderer.go`'s range-function path. `arrayMap` /
   `groupArray` chains become composed nodes.

6. **Builder support for array-aggregate composition** — confirm the
   node surface (`ArrayFold`, `Map`, `ArrayRed`, `Lambda`, `Subscr`,
   `TupleElem`, `MultiIf`) can express the windowed-arrays shape the
   coverage plan will want. Unit tests emit the expected fragments.
   The windowed-arrays source itself is **not** shipped here — that
   is coverage-plan work, written against the stable sqlb surface
   once this plan is done.

## Validation

- per-phase golden-SQL tests — the builder's output, whitespace-
  normalized, matches the pre-retrofit string output on a curated
  corpus. Byte-exact equality is **not** a goal; semantic equality
  under ClickHouse is.
- native harness green at every phase — same corpus as today
- no new parameter *names* introduced during retrofit phases. Renames
  belong to follow-up slices, not the retrofit.
- lint gate: no `fmt.Sprintf` for SQL emission outside `sqlb` once
  Phase 5 lands (simple `grep` check in CI).

## Risks and mitigations

- **Whitespace drift blows up golden tests.** Mitigation: normalize
  both sides before comparison. Acceptable because ClickHouse parses
  both.
- **ClickHouse grammar corner cases.** Parameterized aggregates
  (`quantileExact(0.5)(x)`), `arrayReduce('name', ...)` where the
  aggregate name is a string literal, tuple literal parenthesization,
  array 1-indexing, lambda param binding inside `arrayFold` — the
  builder needs dedicated nodes, not a generic `Call`. Enumerated in
  the node surface above; extend only when a renderer site needs it.
- **Scope creep.** Policy: the builder supports exactly what the
  renderer emits. Add a node type only when a concrete caller needs
  it. No speculative coverage.
- **Retrofit drift from path 3.** The local evaluator and delegation
  paths are untouched; confirm in review that no accidental changes
  crossed the boundary.

## Relationship to other plans

Execution order across the three tracks is:

1. **[native-sql-lowering-plan](./native-sql-lowering-plan/)** — deepens
   the existing slice. Finishes first.
2. **this plan** (sqlb retrofit) — restructures how SQL is emitted on
   the frozen lowered surface. Runs second, in its entirety.
3. **[promql-coverage-plan](./promql-coverage-plan/)** — widens the
   surface, consuming `sqlb` directly from day one. Runs third.

Notes:

- **native-sql-lowering-plan** — the retrofit phases hit the same
  files the lowering plan touches, so the lowering plan must reach
  its definition-of-done (or an explicit hand-off point) before this
  plan starts. Phases 2–5 of this plan do not change semantics — if
  the lowering plan's harness is green before the retrofit, it must
  be green after.

- **promql-coverage-plan** — every coverage item, not just the
  windowed-arrays ones, builds on top of `sqlb`. Starting coverage
  work on string builders guarantees an immediate rewrite of every
  slice. The coverage plan's ordering section already states it runs
  after the lowering plan; once this plan lands, that ordering
  extends to "lowering → sqlb → coverage". The coverage-plan
  `README.md` and `00-context-and-policy.md` should be amended to
  reflect this in the same slice that declares the sqlb plan done.

  One consequence: the windowed-arrays source lives in the coverage
  plan's territory, not here. Phase 6 below ships the *builder
  support* for composing that source (subqueries as `Source`, array
  aggregates, lambda-bearing expressions); the source itself is
  authored inside the coverage plan, against a stable sqlb surface.

- **Third-party ClickHouse AST packages** — AfterShip's parser and
  sqlc-dev/doubleclick exist but are *parsers* whose AST shapes are
  tuned to preserve parsed input fidelity (quoting, whitespace,
  aliases). Using them as builders means filling many fields per
  node. Not worth the dependency for the narrow emission surface we
  need; revisit only if the hand-rolled node set grows past ~30
  kinds.

## Definition of done

- `internal/promshim/native/sqlb` package exists and hosts the node
  surface plus `Select`
- `storage.Build*` and `storage/join_sql` builders all call into
  `sqlb`; no `fmt.Sprintf` for SQL emission outside the package
- `strings.ReplaceAll("{value}", ...)` substitution removed
- renderer's range-function path composes via `sqlb` nodes, not
  nested `fmt.Sprintf`
- node surface demonstrably covers the array-aggregate composition
  shape the coverage plan will need (unit-tested; the source itself
  is authored in the coverage plan)
- native harness green at every retrofit phase and at the final state
- lint gate in CI rejects `fmt.Sprintf` for SQL emission outside
  `sqlb`
