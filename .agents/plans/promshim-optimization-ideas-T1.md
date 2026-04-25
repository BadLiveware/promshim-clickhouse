# T1 brief: ClickHouse optimization surfaces for promshim

Research ClickHouse and ClickHouse-backed observability optimization surfaces that could produce testable ideas for promshim's iterative optimization loop.

Focus on:
- ClickHouse EXPLAIN, ProfileEvents, query_log, query settings visibility, and evidence patterns.
- Predicate pruning, PREWHERE behavior, projections, primary key/order, data skipping indexes, query condition cache, and result query cache caveats.
- Session/profile settings that may be safe as promshim-owned query/session settings versus operator-owned deployment guidance.
- ClickHouse-backed observability examples if useful.

Output to `.pi/feynman/drafts/promshim-optimization-ideas-research-clickhouse.md`.

For each idea, include:
- short source-backed note with URL(s) or local source path;
- possible promshim layer;
- expected proof signal;
- correctness/freshness/operational risks;
- first experiment shape.

Frame findings as candidate ideas, not authoritative recommendations.
