# 06 — Phase 3 selector lowering

## Goal
Make the native path truly repo-owned at the leaf level.

## Scope
- add native selector source generation using:
  - `timeSeriesData(...)`
  - `timeSeriesTags(...)`
  - or an equivalent repo-owned selector relation
- implement matcher lowering:
  - metric name
  - equality / inequality
  - regex / negative regex
- implement time-bound pushdown based on evaluation-range analysis
- define selector result contracts for instant and range modes

## Distinct tasks

1. **Implement repo-owned selector source generation**
   - stop building native fragments on top of delegated PromQL sources
   - mirror the lowering shape from [fromSelector.cpp](file:///home/fl/code/external/ClickHouse/src/Storages/TimeSeries/PrometheusQueryToSQL/fromSelector.cpp)

2. **Lower label and metric matchers**
   - support metric name, equality, inequality, regex, and negative regex
   - compile them to typed predicate structures before SQL rendering where possible

3. **Use evaluation-range metadata to tighten fetches**
   - fetch only the required selector interval instead of coarse query bounds

4. **Define selector output contracts**
   - specify instant and range selector fragment shapes explicitly
   - keep optional columns late-materialized

## Validation
- unit tests for matcher-to-SQL lowering
- integration tests against seeded ClickHouse fixture data
- delegated-vs-native differential comparison for supported selectors
