# Provenance: promshim optimization ideas

- **Date:** 2026-04-25
- **Rounds:** 1 planning round, 1 evidence-gathering round, 1 citation pass, 1 reviewer pass, 1 revision pass.
- **Sources consulted:** 36 cited sources in the final brief, plus four research-note files.
- **Sources accepted:** Official ClickHouse, Prometheus, Grafana Mimir, Thanos, Cortex, DataFusion, Calcite, VictoriaMetrics, arXiv/PromSketch, local promshim docs/plans/source files, and local external checkouts listed in the final Sources section.
- **Sources rejected:** No external URL was rejected as dead by the verifier. The delegated optimizer researcher failed while attempting PDF parsing; PDF parsing output was rejected and replaced with direct HTML/docs/local-source research.
- **Verification:** PASS WITH NOTES. The verifier completed URL/citation work and the reviewer found no fatal issues. Major reviewer concerns about overstated ranking, ClickHouse telemetry caveats, subtree-cache scope, condition-cache wording, binary filter-pushdown specificity, and hash-sharding analogies were revised in the final output. T3 subagent research ran in degraded mode due a PDF parser failure (`Promise.try is not a function` from `unpdf/pdfjs`).
- **Plan:** `.pi/plans/promshim-optimization-ideas.md`
- **Research files:**
  - `.pi/feynman/drafts/promshim-optimization-ideas-research-clickhouse.md`
  - `.pi/feynman/drafts/promshim-optimization-ideas-research-promql-engines.md`
  - `.pi/feynman/drafts/promshim-optimization-ideas-research-optimizers.md`
  - `.pi/feynman/drafts/promshim-optimization-ideas-research-local.md`
- **Drafts and verification files:**
  - `.pi/feynman/drafts/promshim-optimization-ideas-draft.md`
  - `.pi/feynman/drafts/promshim-optimization-ideas-cited.md`
  - `.pi/feynman/drafts/promshim-optimization-ideas-verification.md`
  - `.pi/feynman/drafts/promshim-optimization-ideas-revised.md`
