# Deep research plan: promshim optimization ideas

## Key questions

1. What reusable optimization ideas from Prometheus-compatible query engines,
   ClickHouse observability workloads, and analytical query optimizers are worth
   testing in promshim's iterative optimization loop?
2. Which ideas look fundamental across families or layers rather than narrow
   query-specific special cases?
3. What evidence signal should each idea move in promshim artifacts: EXPLAIN,
   ProfileEvents, round trips, transfer width, memory, Go CPU/allocations, or
   CBE route quality?
4. Which ideas are likely unsafe for Prometheus semantics, ClickHouse
   TimeSeries behavior, freshness, or multi-tenant operations?
5. Which ideas can be turned into concrete candidate rows for
   `harness/artifacts/optimization-backlog.md` without treating the research as
   exhaustive or authoritative?

## Evidence needed

- External patterns from Prometheus-compatible engines and observability systems:
  Prometheus, VictoriaMetrics/MetricsQL, Mimir/Thanos/Cortex where relevant, and
  ClickHouse-backed observability examples.
- ClickHouse docs or source-backed notes for settings, PREWHERE/predicate
  pruning, projections, query condition cache, ProfileEvents, EXPLAIN, and
  TimeSeries-related behavior where available.
- Query optimizer patterns from systems such as DataFusion and Calcite only as
  non-authoritative design inputs for IR/rewrite structure.
- Local repository facts from `.pi/plans/layered-optimization-iteration/`,
  `docs/optimizer-contracts.md`, `docs/optimization-rollout.md`, and
  `docs/clickhouse-tuning-inventory.md`.
- For every proposed idea: expected layer, why it might matter, primary proof
  signal, correctness risks, and first experiment shape.

## Scale decision

Use a broad but bounded multi-source survey with subagents. The topic spans
multiple domains: ClickHouse settings/deployment, PromQL engines, IR/query
optimizer patterns, CBE/routing, and local execution. Decomposition should help,
but the final brief will explicitly state that findings are idea candidates, not
an exhaustive or authoritative roadmap.

Available capabilities checked:

- `feynman-researcher`: available.
- `feynman-verifier`: available.
- `feynman-reviewer`: available.
- `web_search`, `fetch_content`, `code_search`, and local file tools are visible
  in the current tool set.

## Task ledger

| Task | Owner | Output | Status |
|---|---|---|---|
| T1: Research ClickHouse and ClickHouse-backed observability optimization surfaces: deployment/reference profile, session settings, EXPLAIN/ProfileEvents, caches, predicate/pruning, projections, and benchmark evidence patterns. | `feynman-researcher` | `.pi/feynman/drafts/promshim-optimization-ideas-research-clickhouse.md` | done |
| T2: Research Prometheus-compatible engine execution ideas: Prometheus engine behavior, VictoriaMetrics/MetricsQL, Mimir/Thanos/Cortex query optimizations, rollup/range evaluation, caching, downsampling, and semantic caveats. | `feynman-researcher` | `.pi/feynman/drafts/promshim-optimization-ideas-research-promql-engines.md` | done |
| T3: Research IR/query optimizer patterns relevant to PromQL lowering: DataFusion, Calcite, common subexpression elimination, projection pruning, predicate pushdown, cost models, and explainable rewrite rules. | `feynman-researcher` then lead fallback | `.pi/feynman/drafts/promshim-optimization-ideas-research-optimizers.md` | done in degraded mode |
| T4: Inspect local promshim plans/docs and synthesize repo-specific constraints, current levers, and candidate-backlog shape. | lead | `.pi/feynman/drafts/promshim-optimization-ideas-research-local.md` | done |
| T5: Draft cited brief with candidate ideas ranked by fundamentalness, evidence path, risk, and first experiment. | lead | `.pi/feynman/drafts/promshim-optimization-ideas-draft.md` | done |
| T6: Verify citations and reachable URLs. | `feynman-verifier` | `.pi/feynman/drafts/promshim-optimization-ideas-cited.md` | done |
| T7: Review for unsupported claims, overstated confidence, and unsafe recommendations. | `feynman-reviewer` | `.pi/feynman/drafts/promshim-optimization-ideas-verification.md` | done; major issues revised |
| T8: Write final output and provenance sidecar. | lead | `.pi/feynman/outputs/promshim-optimization-ideas.md` and `.pi/feynman/outputs/promshim-optimization-ideas.provenance.md` | done |

## Verification log

- Subagent availability checked with `subagent action=list`: required Feynman
  researcher/verifier/reviewer agents are available.
- Evidence gathering started after user confirmation.
- T1 and T2 researcher runs succeeded and wrote ClickHouse and PromQL engine notes.
- T3 researcher crashed in PDF parsing (`Promise.try is not a function` in `unpdf/pdfjs`); continued in degraded direct mode from HTML docs, web search, and local source checkouts.
- T4 local repo inspection notes written by lead.
- Verifier completed citation pass and wrote cited draft.
- Reviewer completed verification pass and found no fatal issues; major issues were addressed in revised draft.
- Final output and provenance sidecar written.
- Verification status: PASS WITH NOTES due degraded T3 subagent path.

## Decision log

- Chose slug: `promshim-optimization-ideas`.
- Chose multi-agent mode because the topic spans several distinct research
  domains and will feed a broad candidate-ranking loop.
- Will frame output as a non-authoritative idea brief for testing, not as a
  prescriptive roadmap.
- Will prefer fundamental/reusable optimization candidates over narrow special
  cases when evidence and risk are comparable.
- Recorded T3 subagent PDF parser failure as degraded mode rather than blocking the brief.
