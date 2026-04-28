# Native row-source reuse optimizer plan

## Goal

Reduce repeated ClickHouse work for native-lowered PromQL shapes that render the same expensive range source more than once, starting with repeated range functions such as:

```promql
rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_cpu_usage_seconds_total[5m])
```

The previously tempting average form `(A + A) / 2` is already simplified by the logical `cancel_repeated_average` pass and is useful as a guardrail, not as the primary row-source reuse target.

The desired end state is an explainable, correctness-preserving native SQL optimization that recognizes structurally identical native subexpressions, renders the shared row source once, and reuses it in parent SQL shapes without changing Prometheus semantics or broadening supported coverage.

## Why this follows the physical strategy work

The physical strategy layer now gives renderer and explain code typed names for shapes such as sparse direct rate aggregation, native-grid rows, range-window aggregates, and query settings. Row-source reuse should build on that layer rather than adding another ad-hoc renderer shortcut.

The new optimization should answer two separate questions:

1. Are two native subtrees semantically and temporally identical enough to share one rendered row source?
2. If yes, where can the existing renderer/storage SQL builders consume the shared source without moving SQL construction into the optimizer?

## Current state

Observed repository areas relevant to this plan:

- `internal/promshim/native/physical/` selects typed physical strategies but does not represent reusable row sources.
- `internal/promshim/native/renderer/` renders each branch independently and carries `RenderedQuery.PhysicalDecisions` for explain output.
- `internal/promshim/native/renderer/lower.go` already has CSE-related wrapping paths, but the current plan should verify exactly what is shared today before relying on it.
- `internal/promshim/local/native_subtree.go` pre-renders native subtree SQL and attaches rendered SQL plus physical decisions to the optimizer report.
- `internal/promshim/local/planner_test.go` already covers native range/subquery explain paths and should receive representative plan/explain tests for row-source reuse.
- `scripts/ch-explain.sh` now surfaces physical decisions, which makes it a useful inspection tool for this work.

## Non-goals

- Do not introduce cost-based routing or change the candidate selected by CBE.
- Do not broaden PromQL semantic support.
- Do not move SQL text construction into `internal/promshim/native/physical/` or `internal/promshim/native/optimizer.go`.
- Do not rewrite renderer/storage builders wholesale.
- Do not share expressions that differ by timestamp, range, step, offset, label mutation semantics, aggregation grouping, filter matchers, or physical strategy guards.
- Do not claim a performance win from wall-clock timing alone; use ClickHouse `ProfileEvents`, query log rows, and SQL/explain artifacts.

## Assumptions to verify before implementation

- Repeated subexpressions can be keyed from native analysis or logical nodes using a canonical representation that includes all semantic and temporal inputs.
- The first useful target is a non-cancelled repeated range expression such as `rate(selector[window]) + rate(selector[window])`; `(A + A) / 2` and equivalent average forms are already simplified by logical optimization and should be kept as guardrails.
- Reuse should initially apply only inside one rendered native query, not across requests or across separately executed subtrees.
- A rejected or unsafe share should leave existing SQL shape and behavior unchanged.

## Design direction

Add a small row-source reuse layer near native planning/rendering. The layer should produce typed reuse decisions and metadata, not SQL strings.

Possible package or file placement:

```text
internal/promshim/native/physical/reuse.go
internal/promshim/native/renderer/reuse.go
```

A conservative API shape could be:

```go
type ReuseKey struct {
    Shape string
    Expr string
    Mode native.RenderMode
    StartMS int64
    EndMS int64
    StepMS int64
    LookbackMS int64
    OffsetMS int64
    Matchers string
    Grouping string
    PhysicalStrategy string
}

type ReuseDecision struct {
    Kind string
    Key string
    Strategy string
    Reason string
    Guards []string
    Rejected []physical.Alternative
}
```

The exact fields should be chosen from current native analysis/render inputs, but the key must be explicit and reviewable. Avoid pointer identity, raw non-canonical map iteration, or partial expression strings as the only source of identity.

Renderer integration should prefer existing SQL constructs that ClickHouse can optimize predictably:

- `WITH` aliases for scalar/expression reuse only where ClickHouse definitely materializes or deduplicates the intended work.
- Common table expressions or subquery aliases when the reused source is row-oriented and needs to be referenced multiple times.
- Existing storage builders for selector/range rows; row-source reuse should wrap or parameterize their outputs rather than duplicating SQL-building logic.

## Execution tasks

### 1. Establish the current repeated-work baseline

Goal: prove what repeated work exists today and capture artifacts before changing code.

Actions:

- Read current CSE-related renderer code in `internal/promshim/native/renderer/lower.go` and related tests to distinguish existing expression aliasing from row-source reuse.
- Use `scripts/ch-explain.sh` on `repeated_sum_rate_average_by_job_range_7d` or the equivalent PromQL query against the isolated benchmark stack.
- Capture `query-clean.sql`, `promshim-explain-summary.tsv`, `promshim-physical-decisions.tsv`, `query-log-summary.tsv`, and ClickHouse explain artifacts under a named artifact directory.
- Inspect `ProfileEvents` for duplicated read/join/function execution signals that the optimization should reduce.

Acceptance criteria:

- A short evidence note is added to the implementation PR description or a plan-adjacent artifact path is recorded in the final summary.
- The current SQL shape and physical decisions for the repeated-rate query are understood well enough to define a safe reuse boundary.

Validation:

```bash
scripts/ch-explain.sh '<repeated-rate-query>' \
  --mode range \
  --range-seconds 604800 \
  --step 3600 \
  --eval-time 2026-03-14T21:45:42Z \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 --ch-user default --ch-pass otel \
  --native-mode prefer --routing-policy strict \
  --output harness/artifacts/explain/row-source-reuse-before
```

Before running benchmark or profile comparisons, read `.pi/skills/running-sweep/SKILL.md` and `.pi/skills/measuring-ch-optimizations/SKILL.md`.

### 2. Define canonical native reuse keys

Goal: create a tested key builder for only the first supported repeated source shape.

Actions:

- Identify the native analysis or logical node information needed to key repeated `rate(selector[window])` and, after the direct repeated-rate case is handled, repeated `sum by (...) (rate(selector[window]))` shapes that are not already simplified by logical optimization.
- Include matchers, label grouping, function, window, offset, step, evaluation bounds, selector kind, physical strategy, and any settings preference that can affect output rows.
- Add unit tests proving equal keys for syntactically equivalent repeated subexpressions and different keys for semantic differences.
- Keep unsupported or uncertain shapes unkeyed with an explainable rejection reason.

Acceptance criteria:

- Key generation is deterministic across map iteration and process runs.
- Tests cover at least: same expression twice, different grouping, different matcher, different lookback, different offset, and different physical strategy.
- No renderer branch consumes the key yet; this task only establishes safe identity.

Validation:

```bash
go test ./internal/promshim/native/physical ./internal/promshim/native ./internal/promshim/logical
```

### 3. Add a reuse decision inventory and explain metadata

Goal: make reuse decisions visible before changing SQL shape materially.

Actions:

- Add typed decisions for `row_source_reuse` with strategies such as `not_applicable`, `eligible_repeated_source`, and `reused_source`.
- Thread decisions into the existing `PhysicalDecisions` explain path so `query_range_explain` and `ch-explain.sh` can show reuse eligibility.
- Add planner or service tests proving repeated-rate explain output reports eligibility while non-identical expressions report no reuse or rejected alternatives.

Acceptance criteria:

- Explain output describes why a repeated source is eligible or rejected.
- No performance or SQL-shape claim is made at this step unless the renderer already changes shape.

Validation:

```bash
go test ./internal/promshim/local ./internal/promshim
scripts/ch-explain.sh '<repeated-rate-query>' --mode range --native-mode prefer --routing-policy strict --output harness/artifacts/explain/row-source-reuse-metadata
```

### 4. Implement row-source reuse for one native range shape

Goal: render the first repeated range source once and reference it from both parent uses.

Initial target:

```promql
rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_cpu_usage_seconds_total[5m])
```

Guardrail target:

```promql
(sum by (job) (rate(demo_cpu_usage_seconds_total[1h])) + sum by (job) (rate(demo_cpu_usage_seconds_total[1h]))) / 2
```

The guardrail should continue to simplify to the single repeated operand rather than exercising row-source reuse.

Actions:

- Add a renderer-level reuse context that records eligible rendered row sources by canonical key during one query render.
- Reuse only exact repeated sources with identical physical decisions and temporal inputs.
- Use an SQL wrapper form that preserves existing output columns, tags, timestamps, stale-marker handling, and grouping labels.
- Keep fallback behavior: if a source cannot be safely wrapped or referenced twice, render both copies as today and emit a rejection decision.
- Add renderer SQL tests for the repeated-rate shape and at least one non-shared near miss.

Acceptance criteria:

- The target repeated-rate query renders one shared expensive source instead of two independent copies.
- Non-identical sources keep existing SQL shape.
- Existing representative SQL tests for sparse direct rate/native-grid/range-window paths still pass.
- Explain output contains `row_source_reuse=reused_source` or an equivalent compact decision.

Validation:

```bash
go test ./internal/promshim/native/renderer ./internal/promshim/local ./internal/promshim/native ./internal/promshim
```

### 5. Prove correctness before measuring speed

Goal: ensure row-source reuse preserves Prometheus-visible behavior.

Actions:

- Add focused correctness tests for the repeated-rate expression and near-miss expressions.
- Run compliance if renderer behavior changed for any general shape.
- If compliance fails, do not edit `harness/compliance/expected-failures.json` for this optimization; fix the shim behavior or disable the unsafe reuse path.

Acceptance criteria:

- Unit tests and focused integration tests pass.
- Compliance prefer mode has zero unexpected failures.
- Native-mode gap report does not introduce diff failures.

Validation:

```bash
go test ./internal/promshim/storage ./internal/promshim/native/physical ./internal/promshim/native/renderer ./internal/promshim/logical ./internal/promshim/local ./internal/promshim/native ./internal/promshim
./scripts/run-compliance.sh
```

Before running compliance, read `.pi/skills/running-compliance/SKILL.md`.

### 6. Measure the focused optimization

Goal: produce evidence that repeated row-source reuse reduces ClickHouse work for the target query without regressing the surrounding focused corpus.

Actions:

- Rebuild/recreate the isolated benchmark promshim service before measuring changed code.
- Run a focused benchmark corpus containing:
  - a non-cancelled repeated range source such as `rate(demo_cpu_usage_seconds_total[5m]) + rate(demo_cpu_usage_seconds_total[5m])`
  - `repeated_sum_rate_average_by_job_range_7d` as a logical-simplification guardrail
  - `sum_rate_by_job_range_7d`
  - `subquery_rate_over_aggregate_5m_range_1d`
  - `max_over_time_gauge_by_3labels_1h_range_7d`
  - `avg_over_time_gauge_by_3labels_1h_range_7d`
- Use `--include-prom false` if Prometheus reference timing is not needed for the claim.
- Compare query-log/ProfileEvents for read rows, selected rows, join rows, function execution, memory, and ClickHouse duration.

Acceptance criteria:

- The repeated-rate query shows reduced duplicated work in ClickHouse query log/ProfileEvents or the optimization is backed out before merge.
- The focused corpus has `regressionCount: 0` and all expected shim rows keep `native_sql` strategy.
- Any wall-clock claim is paired with ProfileEvents and named artifacts.

Validation:

```bash
(cd harness/bench && docker compose up -d --build promshim)
./scripts/run-bench.sh \
  --corpus /tmp/row-source-reuse-focused-corpus.json \
  --eval-time 2026-03-14T21:45:42Z \
  --prom-url http://localhost:29190 \
  --shim-url http://localhost:29191 \
  --ch-url http://localhost:28124 \
  --artifact-dir harness/artifacts/bench/standalone/row-source-reuse-profile-50k \
  --artifact-name bench-report.json \
  --no-baseline \
  --shim-modes prefer,force_supported \
  --routing-policies strict \
  --include-prom false \
  --repeats 1 \
  --warmup 0 \
  --memory summary \
  --clickhouse-profile summary \
  --matrix
```

### 7. Keep future optimizer hooks explicit

Goal: leave the code ready for later subquery and CBE work without implementing those features now.

Actions:

- Keep reuse keys and decisions typed enough to accept future cardinality/step/lookback estimates.
- Add comments only for non-obvious safety constraints, such as why two nearly identical expressions cannot share a source.
- Do not add unused abstractions for cross-request caches, global memoization, or alternative routing candidates.

Acceptance criteria:

- The implementation remains a bounded row-source reuse optimization.
- Later work can add estimate inputs or CBE decisions without rewriting the initial key/decision model.

## Review and rollback points

- After task 2, the key builder can be merged independently if it is useful and has no renderer behavior change.
- After task 3, explain-only metadata can be reviewed independently if SQL-shape changes are riskier than expected.
- Task 4 is the first SQL-shape change and should be easy to disable by turning off reuse eligibility for the initial shape.
- If measurements do not show a ClickHouse-work reduction for the repeated-rate query, keep only the tested metadata/keying pieces if they are still useful; otherwise revert the renderer reuse change.

## Risks

- Canonical keys can accidentally omit a semantic input and share incompatible sources.
- ClickHouse may inline a CTE or alias in a way that does not reduce work; verify with `EXPLAIN`, `ProfileEvents`, and query log rather than assuming SQL text reuse is physical reuse.
- Reusing a source before stale-marker filtering, timestamp alignment, or label mutation is complete can change Prometheus-visible output.
- Reuse inside subqueries may require different timestamp grids than root range queries; keep subquery propagation for a later plan unless the initial target demands it.
- Benchmark results can be noisy; treat small wall-clock deltas as insufficient evidence without ProfileEvents.

## Final acceptance criteria

- Repeated native range source eligibility is represented by deterministic typed keys and explainable decisions.
- The first supported repeated-rate target reuses one row source or the renderer change is not merged.
- Non-identical expressions are rejected safely and remain behavior-compatible.
- Unit tests, compliance, and focused profile-50k benchmark validation pass with named artifacts.
- `scripts/ch-explain.sh` artifacts make the selected physical strategy and row-source reuse decision visible.

## Expected follow-up plan after this work

After row-source reuse is validated, create a separate plan for subquery physical preference propagation and estimate inputs. That later work can feed cost-based candidate choice, but this plan should finish without adding CBE routing.
