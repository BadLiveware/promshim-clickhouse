# Attempt 20260428-subquery-hotspot-tranche-closure

## Hypothesis

Given repeated low-signal runtime trials in the current `rate(sum(...)[5m:])` / nearby subquery hotspot family, explicitly closing this tranche now should maximize expected value by preventing further noise-driven micro-iterations and freeing scope for a new higher-headroom family.

## Evidence considered

Recent accepted/rejected attempts showed:

- multiple prototype branches reverted (no convincing gains)
- corroborating metrics (memory/functionExecute/queryDuration) mostly flat or noisy
- targeted one-query and small-corpus runs failed to produce stable improvement beyond noise thresholds

## Decision rule applied

Per iteration-41 reflection scope tightening:

- pursue one explicit high-EV branch with thresholds, **or**
- explicitly close the hotspot tranche and pivot.

Current branch did not clear the expected-value threshold from available evidence.

## Decision

Keep (scope decision): **close current subquery hotspot tranche for now** and pivot to the next family.

## Next family

Pivot to the loop hypothesis on **estimate inputs for later CBE** (instrumentation-first, no routing change), where explain-visible estimate plumbing can unlock better future candidate selection and reduce blind runtime probing.
