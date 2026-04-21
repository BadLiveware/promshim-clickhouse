# 10 — Phase 7 rollout and shadow comparison

## Goal
Promote the native lowering feature safely, and run the whole-query
delegation classifier that decides between path 1 and path 2 at query
entry.

## Routing model

The router picks the highest rung on the execution priority ladder that
the query qualifies for (see
[00-status-and-drift.md](./00-status-and-drift.md)):

1. **Entire-query delegation** — whole AST → path 1.
2. **Native SQL only** — path 2 owns the whole execution.
3. **Local execution with native-SQL matrix source** — path 2 emits
   selector SQL, path 3 iterates the matrix.
4. **Local execution with delegated matrix source** — path 1 returns
   a matrix, path 3 iterates.

The classifier decides only whether **rung 1** applies: a whole-AST
walker keyed on a ClickHouse-version capability map returns yes/no.
It does **not** forbid subtree delegation in the non-qualifying case —
rungs 2-4 depend on `prometheusQuery` as an inner-source primitive.
Within the non-qualifying case, the per-subtree choice between rung 2,
3, and 4 is the planner's job.

Adding support for a new upstream PromQL operator means updating the
capability map. Whether that change moves any queries onto rung 1
depends on the shape of real-world corpora; features that only enable
more subtree delegation without tipping whole queries onto rung 1 are
less valuable than features that move whole families onto rung 1.

## Scope
Add request / config controls such as:
- `nativeLoweringMode = off`
- `nativeLoweringMode = explain`
- `nativeLoweringMode = shadow`
- `nativeLoweringMode = prefer`
- `nativeLoweringMode = force_supported`

Shadow mode should:
- execute the selected native subtree
- compare against delegated/local result for supported comparison cases
- log divergence details
- avoid serving native results until confidence is high

## Distinct tasks

1. **Add native lowering mode controls**
   - support off / explain / shadow / prefer / force-supported operation modes

2. **Implement shadow execution**
   - execute native lowering alongside delegated/local execution for supported comparisons
   - do not serve native results by default in shadow mode

3. **Log and summarize divergences**
   - record mismatch shape, timing, and likely semantic category
   - make this visible enough to drive promotion decisions

4. **Use explain as rollout tooling**
   - surface selected strategy, rules applied, pushed predicates, and rendered SQL when appropriate

5. **Implement the entire-query delegation classifier**
   - capability map keyed on ClickHouse version
   - single boolean pass over the PromQL AST: all nodes supported →
     rung 1 (entire-query delegation), otherwise the planner picks
     between rung 2 (native SQL) and rung 3 (subtree-delegated local)
     per subtree
   - does **not** forbid `prometheusQuery` as an inner source in
     rungs 2 and 3
   - expose the "would delegate the whole query" answer in explain so
     operators can see which queries are on the cusp of rung 1
   - the value metric for any upstream PromQL feature is the fraction
     of real-world queries that qualify for rung 1 when it lands

## Validation
- service-level tests for explain and mode behavior
- controlled corpus promotion based on comparison results
- unit tests for the delegation classifier: each capability-map entry
  has explicit yes/no coverage in the classifier's test corpus
