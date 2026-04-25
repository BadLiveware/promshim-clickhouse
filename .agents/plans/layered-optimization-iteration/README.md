# Iterative optimization search plan

## Purpose

Create a repeatable loop for finding the next best promshim optimization, trying
it, measuring it, accepting or rejecting it based on evidence, and then starting
the loop again with updated knowledge. The loop considers all optimization
layers at each turn instead of forcing one optimization per layer.

## Desired end state

- There is a standing optimization backlog ranked by expected value, evidence
  quality, correctness risk, rollbackability, and implementation cost.
- Completed research outputs are available as non-authoritative idea seeds and
  are normalized before they enter the live backlog.
- Each iteration selects one best candidate across all layers: ClickHouse
  deployment guidance, promshim session settings, IR/native SQL, CBE selection,
  tier 1 delegation, tier 2 lowering, tier 3 subtree pushdown, or tier 4 local
  execution.
- Every attempted optimization records a hypothesis, owning layer, expected
  non-p50 signal, baseline artifact, post-change artifact, decision, and next
  action.
- Losing experiments are valuable: they leave a short rejection record so the
  same idea is not retried without new evidence.
- Winning experiments become minimal, reviewable changes with a clear rollback
  path when serving behavior can change, fresh calibration when CBE inputs
  change, and compliance evidence before completion. Individual per-optimization
  configuration gates are useful for risky or narrow changes, but they are not a
  requirement for fundamental safe optimizations that are already covered by
  broader modes, tests, and rollback paths.

## Core loop

Each iteration follows this loop:

1. Refresh or inspect current evidence.
2. Review the research idea seed when looking for new candidate opportunities.
3. Rank candidate opportunities across all layers.
4. Select exactly one candidate with the best expected value.
5. Design the smallest safe experiment that can prove or disprove the expected
   signal.
6. Capture or verify the baseline artifact.
7. Implement the experiment or run the settings/reference-profile comparison.
8. Capture the post-change artifact under matching axes.
9. Decide: accept, reject, defer for missing evidence, or split into a smaller
   candidate.
10. If accepted, harden the change with tests, rollback/config gates, docs, and
    calibration updates as needed.
11. Record the result and return to candidate ranking.

The loop stops only when the requested time/scope is complete, the next best
candidate requires a user decision, or validation exposes an unresolved
correctness or safety blocker.

## Candidate layers

These layers are search surfaces, not a required execution order. Prefer broad,
fundamental improvements over narrow special cases when the evidence quality and
correctness risk are comparable. A fundamental optimization improves a reusable
planning/execution capability, removes a class of duplicate work, improves a
shared cost model, or strengthens measurement/observability for many future
choices. A specific optimization can still win when its measured impact is large,
its proof path is clean, or it unlocks a broader follow-up.

| Layer | Typical candidate | Required proof |
|---|---|---|
| ClickHouse deployment/reference profile | Optional operator setting, schema/layout guidance, observability profile. | Profile-labeled artifacts and operator-owned risk/rollback notes. |
| Promshim ClickHouse session settings | Named settings profile or allowlisted statement setting. | Version support, explain/query-log settings, before/after artifacts, profile rollback. |
| IR metadata and rewrites | New analysis fact, rewrite pass, skipped reason, or safer SQL-shape precondition. | Semantic tests, explain rewrite visibility, and executor-visible signal when performance is claimed. |
| Native SQL lowering | Renderer change, expression reuse, projection, fusion, predicate shape. | EXPLAIN and ProfileEvents/mark/read/function counters. |
| CBE selection/calibration | Candidate family recommendation, cap, confidence, estimate, served gate. | Shadow, negative prefer, shadow-warmed prefer artifacts and safe fallback reasons. |
| Tier 1 delegation | Whole-query ClickHouse PromQL eligibility change. | Compatibility tests, delegated result parity, and fallback visibility. |
| Tier 3 subtree pushdown | Pushdown boundary or candidate generation improvement for known-correct semantics. | Round trips, transfer width, candidate metadata, and divergence checks. |
| Tier 4 local execution | Local memoization, decoding, CPU/memory, or sample-window reuse. | Go tests/benchmarks or pprof plus benchmark counters such as round trips and CH ms. |

## Non-goals

- Do not optimize one item in every layer just to cover the map.
- Do not broaden CBE serving because an isolated local or native experiment looks
  promising without the CBE validation pattern.
- Do not mutate external ClickHouse deployments automatically.
- Do not add compliance expected-failure entries for shim bugs or missing
  coverage.
- Do not treat p50 movement alone as a proof of optimization.

## Global constraints

- Prometheus semantics and compliance remain mandatory.
- Long-range or dense benchmarks use the isolated benchmark stack, never
  compliance ports.
- `run-sweep.sh` is the primary benchmark workflow and rebuilds buildable
  benchmark services through Docker cache.
- Every performance claim names the expected signal before measurement.
- Served behavior changes need a rollback path through routing policy, native
  lowering mode, settings profile, family gate, a broad optimizer disable flag,
  revertability, or a dedicated disable flag when the risk justifies one.
  Fundamental optimizations do not need one-off configuration if they are safe by
  construction, covered by tests, and reversible through existing broader gates
  or normal rollback.
- Operator-owned ClickHouse tuning is documented as optional guidance, not a
  hidden promshim dependency.
- Product docs must not mention this plan directory or execution bookkeeping.

## Evidence standards

| Claim type | Required evidence |
|---|---|
| Storage pruning or predicate improvement | `SelectedRows`, `SelectedBytes`, `ReadCompressedBytes`, `EXPLAIN PLAN indexes=1`. |
| Fewer ClickHouse functions or operators | `FunctionExecute`, operator-specific ProfileEvents, and relevant `EXPLAIN SYNTAX` or `EXPLAIN PIPELINE` changes. |
| Fewer scans or windows | Counted SQL fragments plus query-log `read_rows`, `read_bytes`, `SelectedMarks`, or equivalent counters. |
| Fewer round trips | `X-Promshim-CH-Roundtrips` and benchmark report candidate metadata. |
| Less transfer | response bytes, output series/points, ClickHouse network/send counters, or decoded sample count. |
| Lower local CPU or memory | Go benchmarks, pprof, allocation counts, and unchanged result semantics. |
| Better CBE route choice | strict/selected/served candidate fields, prediction error, fallback reasons, cap decisions, and zero unexpected divergences. |
| Settings-profile improvement | version check, concrete settings in explain/query log, before/after artifacts under matching axes, and profile rollback. |

## Plan files

- `01-candidate-ranking.md` — maintain the ranked backlog and choose the next
  candidate across all layers.
- `02-experiment-design.md` — design one safe, minimal experiment for the chosen
  candidate.
- `03-layer-playbooks.md` — layer-specific experiment patterns and evidence
  requirements.
- `04-measurement-decision.md` — measure, compare, accept, reject, or defer.
- `05-hardening-and-repeat.md` — turn accepted experiments into reviewable
  changes, then restart the loop.
- `06-research-idea-seed.md` — normalize completed research output into seed
  candidates that can be copied into the live backlog when current evidence
  supports them.

## Overall validation commands

Run the subset relevant to the attempted layer, then run broader checks before
claiming an accepted implementation is complete:

```bash
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh scripts/ch-explain.sh scripts/bench-artifact-summary.sh
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench ./cmd/promshim-routing-calibrate/...
python3 -m json.tool .pi/cost-routing-calibration.json >/dev/null
./scripts/run-sweep.sh --bench-status
./scripts/run-sweep.sh --name <compliance-run-name> --skip-bench
rg -n "stage|checklist|post-cbe|Post-CBE|\.agents|\.ralph|Ralph|loop" docs README.md
git diff --check
```

## Final acceptance criteria for one accepted iteration

One iteration is complete when it has:

- selected the best current candidate from a ranked cross-layer backlog, after
  considering any relevant research seed rows;
- recorded a hypothesis and expected signal;
- preserved baseline and post-change artifacts under matching axes;
- made an explicit accept/reject/defer decision;
- for accepted changes, added tests, a rollback path with per-optimization
  configuration only when warranted, calibration updates when inputs changed, and
  docs when behavior or operations changed;
- passed relevant local validation and compliance; and
- recorded what the next ranking pass should consider.
