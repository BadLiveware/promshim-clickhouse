# T2 brief: PromQL engine optimization ideas for promshim

Research Prometheus-compatible engine execution ideas that could inspire testable promshim candidates.

Focus on:
- Prometheus query engine semantics and performance-relevant behavior.
- VictoriaMetrics/MetricsQL execution ideas, rollup/range evaluation, caches, stream aggregation, or query optimization notes.
- Mimir/Thanos/Cortex query frontend, caching, splitting, sharding, downsampling, partial response, or query scheduling ideas where relevant.
- Semantic caveats for Prometheus compatibility: staleness, NaN, histograms, vector matching, ordering, range/subquery grids.

Output to `.pi/feynman/drafts/promshim-optimization-ideas-research-promql-engines.md`.

For each idea, include:
- source-backed note with URL(s) or local source path;
- promshim layer candidate;
- expected proof signal;
- correctness risks;
- first experiment shape.

Frame findings as candidate ideas, not authoritative recommendations.
