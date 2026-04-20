# 10 — Phase 7 rollout and shadow comparison

## Goal
Promote the native lowering feature safely.

## Scope
Add request / config controls such as:
- `nativeLoweringMode = off`
- `nativeLoweringMode = explain`
- `nativeLoweringMode = shadow`
- `nativeLoweringMode = prefer`
- `nativeLoweringMode = force_supported`

Shadow mode should:
- execute the selected native subtree
- compare against delegated/local result for supported comparison cases
- log divergence details
- avoid serving native results until confidence is high

## Distinct tasks

1. **Add native lowering mode controls**
   - support off / explain / shadow / prefer / force-supported operation modes

2. **Implement shadow execution**
   - execute native lowering alongside delegated/local execution for supported comparisons
   - do not serve native results by default in shadow mode

3. **Log and summarize divergences**
   - record mismatch shape, timing, and likely semantic category
   - make this visible enough to drive promotion decisions

4. **Use explain as rollout tooling**
   - surface selected strategy, rules applied, pushed predicates, and rendered SQL when appropriate

## Validation
- service-level tests for explain and mode behavior
- controlled corpus promotion based on comparison results
