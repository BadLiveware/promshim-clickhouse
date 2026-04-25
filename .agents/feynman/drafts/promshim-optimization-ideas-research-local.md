# Local promshim research notes for optimization idea synthesis

Task status: done. These notes summarize local constraints and current levers from repository docs/plans.

## Local facts

| # | Source | Key local fact |
|---|---|---|
| L1 | `.pi/plans/layered-optimization-iteration/README.md` | Optimization should be an iterative loop: rank candidates across all layers, choose one best candidate, experiment, measure, accept/reject/defer/split, then repeat. Fundamental reusable optimizations are preferred over narrow special cases when evidence/risk are comparable. |
| L2 | `.pi/plans/layered-optimization-iteration/01-candidate-ranking.md` | Backlog rows should include benefit, breadth, evidence readiness, correctness risk, implementation cost, rollbackability, expected signal, and next action. |
| L3 | `.pi/plans/layered-optimization-iteration/05-hardening-and-repeat.md` | Accepted optimizations need final accepted measurements, explanatory commits, results ledger entries, and negative-result tracking for failed experiments. Per-optimization config is optional and should be used only when warranted. |
| L4 | `docs/optimizer-contracts.md` | Optimizer changes require stable rule names, exact preconditions, preserved invariants, expected physical signals, skipped reasons, high-risk exclusions, CBE interactions, and rollback controls when serving is affected. |
| L5 | `docs/optimizer-contracts.md` | Family labels include selector, rate, increase, range functions, aggregation, binary/vector matching, histograms, label mutation, subquery, repeated_subexpr, reference_required, and unknown. |
| L6 | `docs/optimization-rollout.md` | Served CBE changes require strict/reference safety, `cost_shadow` before `cost_prefer`, explicit local family gates, estimates, caps, confidence, and preserved artifacts. |
| L7 | `docs/clickhouse-tuning-inventory.md` | ClickHouse surfaces are separated into operator-owned deployment guidance, shim-owned session/query settings, SQL-shape alternatives, unsafe/out-of-scope settings, version-dependent settings, and distributed-only settings. |
| L8 | `.pi/skills/running-sweep/SKILL.md` | `run-sweep.sh` is the primary workflow, uses isolated benchmark ports/volumes, and rebuilds buildable benchmark services with Docker cache. |
| L9 | `.pi/skills/measuring-ch-optimizations/SKILL.md` | Optimization claims require signals such as EXPLAIN, ProfileEvents, SelectedRows/Bytes, FunctionExecute, pipeline stages, memory, or round trips; small p50 changes alone are noise. |
| L10 | `.pi/skills/running-compliance/SKILL.md` | Compliance allowlist is only for allowed deviances; shim bugs and missing feature coverage stay visible. |

## Local constraints for candidate ideas

- Ideas should be added as rows in `harness/artifacts/optimization-backlog.md`, not treated as a fixed roadmap.
- Negative results should be recorded in `harness/artifacts/optimization-negative-results.md` with retry conditions.
- Accepted ideas should update `harness/artifacts/optimization-results.md` with baseline and accepted artifacts.
- ClickHouse deployment/schema ideas should remain optional guidance unless promshim explicitly owns the schema or benchmark profile.
- Session settings must be allowlisted, query/session scoped, version-aware, visible in explain/query-log evidence, and rollbackable by profile or broader mode.
- IR/native SQL changes must avoid algebraic rewrites unless Prometheus semantics are proven; exact subtree reuse and projection/label requirements are safer first classes.
- CBE serving should not broaden by default; use shadow, negative prefer, and shadow-warmed prefer artifacts.
- Tier 3/4 work is in scope when tied to CBE safety, observability, or measured performance for already-correct semantics.

## Synthesis implications

1. Strong research candidates should be expressed as testable backlog rows with fields from the local plan.
2. Fundamental candidates should receive high breadth scores: e.g. proof-signature artifacts, IR dependency classifier, optimizer rule budget instrumentation, feature extraction for CBE, and exact range-window reuse primitives.
3. Narrow candidates can still be useful when they have strong evidence or unlock a broader primitive: e.g. a specific query-condition-cache experiment or hash-sharded `sum by` prototype.
4. Rejected ideas such as default result caching or approximate answers should still be recorded as negative/unsafe candidates if considered.

## Sources

- `.pi/plans/layered-optimization-iteration/README.md`
- `.pi/plans/layered-optimization-iteration/01-candidate-ranking.md`
- `.pi/plans/layered-optimization-iteration/05-hardening-and-repeat.md`
- `docs/optimizer-contracts.md`
- `docs/optimization-rollout.md`
- `docs/clickhouse-tuning-inventory.md`
- `.pi/skills/running-sweep/SKILL.md`
- `.pi/skills/measuring-ch-optimizations/SKILL.md`
- `.pi/skills/running-compliance/SKILL.md`
