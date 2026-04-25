# T3 brief: IR/query optimizer patterns for promshim

Research IR/query optimizer patterns relevant to PromQL lowering and cost-based execution.

Focus on:
- DataFusion optimizer patterns: projection pushdown, CSE, filter/predicate pushdown, physical planning, rule preconditions, explainability.
- Calcite optimizer patterns: rule guards, traits/costs, projection/filter planning, explainable transformations.
- General lessons for PromQL-specific IR: stable rule names, precondition/skipped reasons, semantic invariants, cost estimates, and calibration.

Output to `.pi/feynman/drafts/promshim-optimization-ideas-research-optimizers.md`.

For each idea, include:
- source-backed note with URL(s) or local source path;
- promshim layer candidate;
- expected proof signal;
- correctness risks;
- first experiment shape.

Frame findings as design inspiration only; PromQL semantics and local compliance remain authoritative.
