# Path 2 native SQL compliance refresh — 2026-04-22

## Why this refresh exists

The previous Path 2 plan assumed the compliance story was effectively closed apart from the explicitly documented accepted deviations. That is no longer true under the corrected setup:

- reference Prometheus is now **3.5.2 LTS**
- the compliance harness now forces **native SQL to run alongside `prefer` mode** instead of silently validating only the fallback-served path
- `harness/compliance/expected-failures.json` is now the fixed accepted-deviation set

So this document replaces the old “100% compliance” framing with a fresh plan based on the actual current harness output.

## Command run

```bash
./scripts/run-compliance.sh --ready-timeout 120
```

Reports produced:

- `harness/compliance/artifacts/compliance-report-prefer-20260422T175019Z.json`
- `harness/compliance/artifacts/compliance-report-native-20260422T175025Z.json`

## Execution update — 2026-04-22 late rerun status

The plan below has now been executed through the original Priority 4 reruns.

Latest validation reports:

- prefer-only: `harness/compliance/artifacts/compliance-report-prefer-20260422T185556Z.json`
- native-only: `harness/compliance/artifacts/compliance-report-native-20260422T185631Z.json`
- full two-pass rerun:
  - `harness/compliance/artifacts/compliance-report-prefer-20260422T185701Z.json`
  - `harness/compliance/artifacts/compliance-report-native-20260422T185710Z.json`

Latest outcome:

### Prefer mode

- total: **539**
- passed: **538**
- diff failures: **1**
- unexpected failures: **0**
- unsupported: **0**
- reconcile: **CLEAN**

The only remaining prefer-mode diff is the accepted `topk-tie-break-ordering` entry.

### Native-only mode

- total: **539**
- passed: **538**
- diff failures: **1**
- unexpected failures: **0**
- unsupported: **0**

The only remaining native-mode diff is the same accepted `topk-tie-break-ordering` deviation. The modulo tolerance rule still exists for tiny `%` float drift, but no material native modulo bug remains after the runtime correction for vector/scalar-expression modulo.

### What was fixed

- Prometheus 3 UTF-8 label-name compatibility for `label_replace` / `label_join`
- native negative-offset selector correctness
- native scalar-literal and scalar-only root planning
- native scalar-expression and bool-comparison composition support
- native modulo semantics for vector/scalar-expression lowering
- native `time()` root and comparison/composition support

### Current conclusion

Under the accepted-deviation policy in `harness/compliance/expected-failures.json`, Path 2 is now effectively complete again:

- prefer mode is clean except the accepted `topk` deviation
- native mode is clean except the same accepted `topk` deviation
- there are no remaining unexpected failures
- there are no remaining unsupported native roots

## Current outcome

### Prefer-mode pass

- total: **539**
- passed: **536**
- diff failures: **1**
- unexpected failures: **2**
- unexpected success: **0**

Reconcile result:

- **1 expected failure matched** the allowlist
- **2 unexpected failures remain**

### Native-only pass

- total: **539**
- passed: **431**
- diff failures: **4**
- unexpected failures: **104**
- unexpected success: **0**

Native gap summary:

- **4 diff failures**
- **102 unsupported-root failures**
- **2 other errors**

## Accepted deviations that remain accepted

These stay out of scope for the implementation work below unless the policy changes:

1. **`topk` tie-break ordering**
   - allowlisted as `topk-tie-break-ordering`
   - storage-order artifact, not a native SQL feature gap
2. **native modulo small-float drift tolerance**
   - handled by the tolerance rule in `expected-failures.json`
   - did not produce a surviving failure in this run

## Failure categories from the corrected setup

## 1. Prefer-mode regressions introduced by Prometheus 3.x label-name rules

### Count

- **2 unexpected failures** in prefer mode
- the same **2** also appear as “other errors” in native mode

### Queries

- `label_replace(demo_num_cpus, "~invalid", "", "src", "(.*)")`
- `label_join(demo_num_cpus, "~invalid", "-", "instance")`

### Current behavior

promshim still rejects the destination label name `~invalid` with:

- `bad_data: invalid destination label name in label_replace(): ~invalid`
- `bad_data: invalid destination label name in label_join(): ~invalid`

### Why this matters

This is now a **real compatibility bug** against the Prometheus 3.5 reference, not an accepted deviation and not a native-only gap. It breaks the gated prefer-mode pass.

### Likely code areas

- `internal/promshim/model/label_transform.go`
- local logical-builder call paths that construct label mutation configs

### Required outcome

Prefer mode must go green again with only the accepted `topk` tie-break deviation remaining.

---

## 2. Native correctness bug: negative offset selectors

### Count

- **3 real native diff failures**
- plus **1 accepted diff** (`topk` tie-break)

### Queries

- `demo_memory_usage_bytes offset -1m`
- `demo_memory_usage_bytes offset -5m`
- `demo_memory_usage_bytes offset -10m`

### Current behavior

These queries do lower natively, but the returned values diverge heavily from Prometheus. This is not a small precision issue; the sample values are materially wrong across the range output.

### Why this matters

This is the only currently visible **native-lowered correctness bug family** in the corrected run. Until this is fixed, native-only parity is false even where root lowering succeeds.

### Likely problem area

Future-looking offset handling for selector/range materialization is still wrong somewhere in the native execution path. The likely hotspots are:

- selector time-window analysis in `internal/promshim/native/analysis.go`
- native SQL rendering / range shaping for offset selectors
- any shared range-grid materialization path used by offset selectors in the renderer/storage layer

### Required outcome

All three negative-offset compliance cases must match Prometheus in native-only mode.

---

## 3. Native root-plan coverage gaps: scalar-root and scalar-composition support

### Count

- **102 unsupported-root failures**
- all failed with the same planner/service-level shape:
  - `native lowering mode "force_supported" requires a native_sql root plan ...`

### Main observation

The remaining native-only gap is no longer a broad function-family problem like range functions, joins, histograms, label mutation, or aggregations. The corrected harness shows the open surface is now concentrated in **scalar roots and scalar/vector compositions that do not currently become a native root plan**, even though many of the building blocks already exist in analysis.

### Grouped failure inventory

#### A. Scalar roots and scalar-only expressions — 11 queries

- scalar literals / constants: **9**
  - `42`
  - `1.234`
  - `.123`
  - `1.23e-3`
  - `0x3d`
  - `Inf`
  - `+Inf`
  - `-Inf`
  - `NaN`
- scalar-only arithmetic roots: **2**
  - `1 * 2 + 4 / 6 - 10 % 2 ^ 2`
  - `-1 ^ 2`

#### B. Vector/scalar bool-comparison composition gaps — 18 queries

- vector + scalar-bool child expression: **6**
  - `demo_num_cpus + (1 == bool 2)`
  - same shape for `!=`, `<`, `>`, `<=`, `>=`
- vector-scalar bool comparisons: **6**
  - `demo_memory_usage_bytes == bool 1.2345`
  - same shape for `!=`, `<`, `>`, `<=`, `>=`
- scalar-vector bool comparisons: **6**
  - `1.2345 == bool demo_memory_usage_bytes`
  - same shape for `!=`, `<`, `>`, `<=`, `>=`

#### C. Scalar-expression with vector binary/comparison composition — 25 queries

- scalar-expression op/comparison vector: **12**
  - `(1 * 2 + 4 / 6 - (10%7)^2) <op> demo_memory_usage_bytes`
  - for `+ - * / % ^ == != < > <= >=`
- vector op/comparison scalar-expression: **12**
  - `demo_memory_usage_bytes <op> (1 * 2 + 4 / 6 - 10)`
  - for `+ - * / % ^ == != < > <= >=`
- vector with unary-negated scalar literal: **1**
  - `demo_memory_usage_bytes + -(1)`

#### D. `time()` scalar-root and scalar-composition gaps — 48 queries

- scalar arithmetic with `time()` and a numeric literal: **12**
  - `1 <op> time()` and `time() <op> 1`
  - for `+ - * / % ^`
- scalar bool comparisons with `time()` and a numeric literal: **12**
  - `time() <cmp> bool 1`
  - `1 <cmp> bool time()`
  - for `== != < > <= >=`
- scalar arithmetic with `time()` on both sides: **6**
  - `time() <op> time()` for `+ - * / % ^`
- scalar bool comparisons with `time()` on both sides: **6**
  - `time() <cmp> bool time()` for `== != < > <= >=`
- vector comparisons against `time()`: **12**
  - `time() <cmp> demo_memory_usage_bytes`
  - `demo_memory_usage_bytes <cmp> time()`
  - for `== != < > <= >=`

### Why this matters

This is the real remaining native-SQL work queue. The prior matrix said Path 2 was effectively complete, but the corrected compliance harness proves the matrix is still missing an important surface area:

- scalar literal roots
- scalar-only native expression trees
- scalar roots built from synthetic scalars like `time()`
- bool-returning scalar comparisons as native subexpressions
- vector/scalar compositions whose scalar side is itself an expression rather than only a plain literal

### Likely code areas

- native analysis and fragment construction in `internal/promshim/native/analysis.go`
- native fragment/types support for scalar-root rendering
- local/native planner root selection paths
- service `force_supported` behavior is already correctly surfacing the gap in `internal/promshim/service.go`
- compliance inventory generation under `.pi/path2-compliance-matrix*` and `.pi/path2-promql-compliance-alignment*`

### Required outcome

All 102 unsupported-root queries must become real native roots in `force_supported` mode.

---

## What this means about the previous plan

The old plan is stale in two important ways:

1. it overstates completion because the corrected harness now shows open scalar-root coverage and a real negative-offset correctness bug
2. it predates the Prometheus 3.5 label-name compatibility change, so the prefer-mode gate is no longer green

So the next plan should be driven by the current harness evidence, not by the earlier all-green interpretation.

## Refreshed execution plan

## Priority 0 — Rebaseline the measurement/docs to the corrected harness reality

### Goal

Make the repo’s compliance accounting reflect the current Prometheus 3.5 + corrected-native-run truth.

### Work

- record this run as the new baseline
- update the Path 2 compliance docs/matrix so scalar-root gaps are explicitly represented instead of being silently absent
- keep `expected-failures.json` unchanged except for drift verification; it should remain reserved for true accepted deviations

### Acceptance

- `.pi/` planning/compliance docs acknowledge the 3 current buckets:
  - Prom 3 label-name compatibility
  - negative-offset correctness
  - scalar-root native coverage

## Priority 1 — Restore the gated prefer-mode pass

### Goal

Fix the Prometheus 3 UTF-8 label-name compatibility bug.

### Work

- replace legacy-only destination/source label validation where Prometheus 3 now allows UTF-8 label names
- update local/native label mutation tests to cover the Prom 3 cases from the compliance corpus
- confirm the harness no longer needs to surface these as unexpected failures

### Acceptance

- prefer-mode report shows only the accepted `topk` deviation
- `scripts/run-compliance.sh --skip-native` exits cleanly

## Priority 2 — Fix native negative-offset correctness

### Goal

Make native lowering of negative offset selectors match Prometheus exactly.

### Work

- trace how negative offsets affect analysis time requirements and rendered selector windows
- add focused tests for `offset -1m`, `offset -5m`, `offset -10m` in instant and range mode as applicable
- fix whichever range-grid / lookback / time-shifting step is using the wrong timestamp basis

### Acceptance

- all three offset queries go green in native-only compliance
- no regressions in existing offset / `@` / subquery coverage

## Priority 3 — Add true native scalar-root execution

### Goal

Close the 102 `requires a native_sql root plan` failures by allowing scalar-root native plans and scalar-expression composition to render as native roots.

### Recommended order

#### 3.1 Native scalar literals and scalar-only expression trees

Cover:

- numeric literals
- `Inf`, `-Inf`, `+Inf`, `NaN`
- scalar-only arithmetic trees
- unary scalar forms

Acceptance:

- `42`, `1.234`, `.123`, `1.23e-3`, `0x3d`, `Inf`, `+Inf`, `-Inf`, `NaN`, `1 * 2 + 4 / 6 - 10 % 2 ^ 2`, `-1 ^ 2` all get a native root plan

#### 3.2 Native bool-returning scalar comparisons

Cover:

- scalar-vs-scalar `bool` comparisons
- scalar expressions as comparison operands
- these results as embeddable native scalar children for outer vector arithmetic

Acceptance:

- `(1 == bool 2)`-style expressions can be rendered natively
- `demo_num_cpus + (1 == bool 2)` family goes green

#### 3.3 Native scalar/vector composition when the scalar side is an expression

Cover:

- scalar-expression vs vector arithmetic
- scalar-expression vs vector comparisons
- vector vs scalar-expression arithmetic/comparisons
- unary-negated scalar children

Acceptance:

- both 12-query binary/comparison families go green
- `demo_memory_usage_bytes + -(1)` goes green

#### 3.4 Native `time()` root and `time()` composition support

Cover:

- `time()` as a native scalar root
- `time()` with numeric literals
- `time()` with `time()`
- `time()` with vectors in comparison expressions
- bool forms for the scalar cases

Acceptance:

- all 48 `time()`-related unsupported-root queries get a native root plan and match Prometheus

## Priority 4 — Re-run and tighten the compliance accounting

### Goal

After fixes land, confirm that the harness and docs agree.

### Work

- rerun prefer + native compliance
- regenerate/update the `.pi` compliance inventory if needed
- verify no stale “all green” statements remain if the harness says otherwise

### Acceptance

- prefer mode: green except allowlisted accepted deviations
- native mode: green except allowlisted accepted deviations
- no remaining unexpected failures or unsupported-root failures

## Validation ladder

### Fast loop while fixing bucket 1

```bash
go test ./internal/promshim/...
go test ./internal/promshim/local/...
go test ./internal/promshim/native/...
```

### Prefer gate

```bash
./scripts/run-compliance.sh --skip-native --ready-timeout 120
```

### Native gate

```bash
./scripts/run-compliance.sh --skip-prefer --ready-timeout 120
```

### Full corrected harness checkpoint

```bash
./scripts/run-compliance.sh --ready-timeout 120
```

## Definition of done for this refreshed plan

This refresh is now satisfied:

- prefer-mode compliance is green except for the accepted deviations in `expected-failures.json`
- native-only compliance is green except for the same accepted deviations
- the three negative-offset queries no longer diff
- the former scalar-root / scalar-composition failures no longer report `requires a native_sql root plan`
- the Path 2 compliance docs in `.pi/` now reflect the corrected harness baseline and the completed fix set
