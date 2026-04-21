# PromClick adoption plan

## Why this plan exists

The native SQL lowering plan in `.pi/native-sql-lowering-plan/` assumes
we build a PromQL-over-ClickHouse shim as a bridge until ClickHouse's
own PromQL evaluator on the TimeSeries engine is production-ready. That
plan still describes a coherent architecture, but a cheaper path exists:
adopt [PromClick](https://github.com/PromClick/PromClick), a newly
released Apache-2.0 PromQL-on-ClickHouse product that already covers
~99.4% of our real query corpus and ships downsampling we would
otherwise have to build.

This plan supersedes Phases 2-7 of the native SQL lowering plan. The
old plan is preserved for reference and because parts of it (harness,
Phase 1 types, subquery step-grid in `internal/promshim/exec/`) become
input material to the PromClick path rather than discarded work.

## What changed in our thinking

Three updates, in order:

1. **PromClick exists.** A working PromQL evaluator against ClickHouse,
   with downsampling, has shipped. The build-vs-adopt calculus flipped.
2. **We have no production commitments.** Nothing currently depends on
   the shim. The migration cost is "throw away current metrics and point
   OTel collector somewhere else," which we are willing to do.
3. **The shim was never going to reach CH-native PromQL without a data
   migration anyway.** Retirement to `prometheusQuery` on TimeSeries
   still requires the data to be in TimeSeries, which it is today but
   does not have to stay that way. If we are going to accept a data
   migration eventually, we can accept one now to a product that works.

## Strategic intent

- **PromClick is the near-term and likely long-term metrics product.**
  Not a bridge; not a staging ground. If it holds up, we stay on it.
- **The shim's bridge thesis is retired.** We are not going to build a
  shim whose purpose is to shrink as upstream catches up. The math only
  worked if the shim was cheaper than PromClick, which it is not.
- **Fork-if-needed is the default posture.** PromClick is 2 weeks old
  with one primary contributor. If upstream is responsive, we contribute;
  if not, we fork and maintain. The worst case is still strictly cheaper
  than greenfield shim maintenance.
- **Our verification infrastructure outlives the evaluator question.**
  `harness/` and `internal/promharness/` verify any PromQL evaluator
  against Prometheus. They are the load-bearing asset and stay regardless
  of which evaluator we run.

## What PromClick provides

- **Serving:** `promclick-proxy` on :9099, full Prometheus HTTP API
  (`/api/v1/query`, `query_range`, `labels`, `series`, `metadata`)
- **Ingestion:** `promclick-writer` receiving Prometheus `remote_write`
- **Storage:** custom SigNoz-style MergeTree — `samples` (metric_name,
  fingerprint, unix_milli, value) + `time_series` (fingerprint → labels)
- **Downsampling:** `promclick-downsampler` produces 5m and 1h
  `AggregatingMergeTree` tiers via `REFRESH EVERY` materialized views,
  storing `val_min/max/sum/count`, `counter_total`, argMin/argMax. Proxy
  picks a tier per query from (step, age), splitting long ranges across
  tiers via `UNION ALL`.
- **Evaluator:** from-scratch Go PromQL evaluator using Prometheus's
  `promql/parser`. Prometheus-faithful semantics: counter-reset
  correction, extrapolation, StaleNaN, Kahan-Neumaier summation,
  monotonicity-enforced `histogram_quantile`, 70 functions, full vector
  matching, `label_replace`/`label_join`.
- **License:** Apache-2.0.

## What PromClick does not cover for our corpus

Measured against `scratch/grafana-prometheus-queries/promql-all.txt`
(2128 queries):

| Feature | Uses in corpus | PromClick supports | Impact |
|---|---|---|---|
| Subqueries (`[Nm:Ns]`) | 12 (~0.6%) | No — README lists as "Not yet supported" | Blocker for those 12 |
| `@` modifier | 0 | No | None |
| Native histograms (`histogram_count`/`histogram_sum`/...) | 0 | No | None |
| `histogram_quantile` (classic) | 84 | Yes | None |

Only subqueries are real. Most of the 12 uses are the standard
`irate(sum(...)[Nm:])` idiom; at least one (`avg_over_time(rate(...)[30m:])`)
has a subquery that is load-bearing and cannot be rewritten.

## Feature-closure strategy for subqueries

**Preference:** upstream contribution to PromClick. The work is
tractable:

- PromClick already uses Prometheus's parser, which produces subquery
  AST nodes; no parser changes required.
- PromClick's `eval/` has range-function iteration and matrix handling,
  which is what a subquery reduces to once the inner expression is
  evaluated on a sub-step grid.
- What needs to be added: step-grid expansion in the translator and
  evaluator — evaluate the inner expression at the sub-step grid
  timestamps, then feed the resulting matrix to the outer operator.

**Source material we own:** `internal/promshim/exec/` implements
exactly this pattern today (commits `ffe42d9` "Subquery [i]rate",
`c8be729` "Step", `0d44c2b` "Workaround inclusive vs exclusive left
edge"). These are the reference implementation to port. The
edge-inclusivity workaround is the sort of detail that is easy to get
wrong from first principles.

**Fallback:** maintain a fork. Apache-2.0 permits it, the codebase is
small enough for us to maintain, and we are comfortable being sole
maintainers — that is what the shim path committed us to anyway.

## Migration plan

1. **Stand up PromClick alongside current ingestion.**
   - Deploy `promclick-writer` and `promclick-proxy` in our cluster
   - Add a second `remote_write` endpoint in the OTel collector config
     pointing at PromClick (OTel collector supports multiple
     `remote_write` targets)
   - Keep TimeSeries ingestion running during the trial so nothing breaks

2. **Run the harness against PromClick.**
   - Point `internal/promharness/` at PromClick's HTTP API as the
     "subject under test," keep Prometheus as oracle
   - Run the full corpus, triage divergences
   - Apply Layer 1 (time-window matrix) from
     `.pi/native-sql-lowering-plan/12-harness-parametrization.md` before
     trusting counter-path results; subqueries are currently
     differentially untestable against PromClick, so skip those rows
     until step 4

3. **Targeted testing of downsampled tiers.**
   - Hand-written tests against the 5m and 1h tiers for counter-reset
     handling, gap behavior, series appear/disappear within a tier
     window, and tier-boundary `UNION ALL` stitching
   - The downsampler's `counter_total` accumulation is where semantic
     bugs would hide

4. **Close the subquery gap.**
   - Port subquery step-grid from `internal/promshim/exec/` to
     PromClick's translator + evaluator
   - Open PR upstream; if receptive, merge and track release. If
     unresponsive within a reasonable window, maintain a fork
   - Re-run harness with subqueries enabled

5. **Promote PromClick to primary.**
   - Point Grafana and any other consumers at PromClick's proxy
   - Remove the TimeSeries `remote_write` endpoint from the OTel
     collector config
   - Decommission TimeSeries metrics ingestion and tables (traces/logs
     stay on their OTel ClickHouse path, unaffected)

6. **Retire or freeze the shim.**
   - Archive `.pi/native-sql-lowering-plan/` as historical reference
   - Keep `harness/` and `internal/promharness/` — they are now
     PromClick's verification harness
   - Freeze `internal/promshim/` or retire it entirely, depending on
     whether the proxy adds anything useful on top of PromClick (it
     probably does not)

## What we keep

- **Harness:** `harness/` and `internal/promharness/` — primary
  correctness verification for any evaluator, including PromClick
- **Reference code:** `internal/promshim/exec/` subquery step-grid
  (`ffe42d9`, `c8be729`, `0d44c2b`) and range-function implementations
  — source material for upstream contributions to PromClick
- **Native analysis types:** `internal/promshim/native/` — not load-
  bearing for PromClick, but a clean reference for anyone revisiting
  PromQL lowerability analysis in the future

## What we stop doing

- Shim Phases 2-7 from `.pi/native-sql-lowering-plan/`:
  - generalized `nativeSubtreePlan`
  - RBO optimizer pipeline
  - selector, join, vector-matching lowering
  - Phase 6b native range-function lowering
  - rollout modes (explain/shadow/prefer/force_supported)
- Adapter layer for TimeSeries inner-table column shape changes
  (upstream PR #99083 becomes irrelevant to us)
- Whole-AST entire-query delegation classifier
- The "bridge that shrinks as CH matures" strategic narrative

## Risks

1. **PromClick correctness on downsampled tiers.** Counter-reset
   handling on the `counter_total` aggregate is the highest-risk area;
   bugs would silently distort rates on step >= 5m queries. Mitigation:
   targeted tier-boundary tests in step 3.
2. **Subquery semantics drift when ported.** PromClick's evaluator
   differs from our `exec/` path in shape; porting is not copy-paste.
   Mitigation: differential tests against Prometheus for every
   subquery-using query in the corpus before promoting.
3. **Upstream responsiveness.** If the maintainer disappears or rejects
   the subquery PR, we maintain a fork. Cost is bounded — the codebase
   is small and focused.
4. **Our inertia.** The worst outcome is a half-migrated state where
   both the shim and PromClick exist in partial production use.
   Mitigation: set a hard cutover target once step 3 is green.

## Success criteria

- Harness green against PromClick across the full corpus (modulo the
  documented & accepted 0 divergences if any)
- Subquery support available (upstream or fork), covering all 12 corpus
  queries
- OTel collector configuration has exactly one `remote_write` target
  (PromClick)
- TimeSeries metrics tables dropped
- `.pi/native-sql-lowering-plan/` archived as historical

## Non-goals

- Retargeting PromClick to the TimeSeries engine — defeats the point
  of adopting it
- Running shim and PromClick in parallel production long-term
- Adding features to PromClick that our corpus does not use (native
  histograms, `@` modifier) unless upstream wants them for their own
  reasons
- Preserving the shim's "bridge to CH-native PromQL" strategy

## Open questions

- OTel collector `remote_write` to PromClick: does PromClick's writer
  accept the exact wire format the OTel collector emits? Spot-check
  before step 1.
- Does PromClick's downsampler create tier tables idempotently in a way
  that survives re-runs? The README implies yes (checksum-guarded MV
  recreation); worth verifying before relying on it in deployment.
- Is there any reason to keep the shim's proxy layer (e.g. query
  auth, caching, rate limiting) on top of PromClick? If yes, the shim
  becomes a thin frontend to PromClick rather than being retired
  entirely. Decide this at step 5.
