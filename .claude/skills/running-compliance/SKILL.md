---
name: running-compliance
description: Use when running the PromQL compliance suite in ch-observability, when a compliance test fails, when triaging the native-mode gap report, or when deciding whether to add an entry to expected-failures.json
---

# Running Compliance

## Overview

The compliance suite is a two-pass differential run between promshim and reference Prometheus 3.x against a frozen scraped fixture. It is the primary signal that a branch has not regressed PromQL semantics.

**Core principle — gaps stay visible.** Any shape the shim cannot handle yet is a *gap*, not an "expected failure." Gaps must fail loudly so we keep pressure on ourselves to close them.

## Running the suite

```bash
scripts/run-compliance.sh
```

Foreground run. **~15s warm, ~60s cold** (image build + stack up). Do not wrap in minute-long timeouts — the script explicitly warns against it.

What happens:
1. Brings up ClickHouse/Prometheus/promshim via docker compose.
2. **Pass #1 — prefer mode (gated)**: shim picks its best tier; failures reconciled against `harness/compliance/expected-failures.json`. Non-zero exit on drift.
3. **Pass #2 — native-only mode (informational)**: shim forced to tier 2 or fail; emits a categorized gap report. Never gates.
4. Tears the stack down (add `--keep-up` to iterate).

Reports land in `harness/compliance/artifacts/compliance-report-{prefer,native}-<stamp>.json`. Useful flags: `--no-build`, `--skip-native`, `--skip-prefer`, `--keep-up`. See `scripts/run-compliance.sh --help` for the full list.

## The allowlist — `expected-failures.json`

**Only three kinds of entry are ever valid. Everything else is a bug to fix, not a line to add.**

1. **Impossible-to-replicate reference-side behavior.** Current example: `topk-tie-break-ordering` — Prom's tie-break is TSDB postings / scrape-discovery order, not derivable from labels. Reproducing it means mirroring Prom's storage layer. Each entry must match a specific query and diff shape so unrelated drift surfaces as a regression.
2. **Fundamental CH-vs-Prom primitive differences with bounded numeric impact.** Go in `tolerances[]` with a bounded float margin. Current example: `native-modulo-small-float-drift` (sub-1e-6 drift on large operands). Labels and timestamps must still match exactly.
3. **Small deviances that significantly simplify or speed up the native SQL path.** Allowed *only* after explicit user approval — stop, describe the deviance, quantify the speedup, explain why a compliant alternative is infeasible. Do not add preemptively.

Anything else — a shim bug, a missing feature, a planner error — stays a visible failure. Do not expand the allowlist to make the compliance run green.

## Native-mode gap report

Run `harness/compliance/scripts/native-gap-report.sh` against the latest native report for a categorized view.

| Category | Meaning | Allowlistable? |
|---|---|---|
| `diff_failure` | Native lowered the query but returned **wrong values** | No — real correctness bug |
| `unsupported_root` | Planner refused to lower | No — missing coverage / work queue |
| `other` | bad_data, timeouts, etc. | No — investigate |

**`diff_failure` is especially nasty.** Pass #1 "passes" because the local fallback re-evaluates on buffered data, masking that the native path returned silently wrong numbers. If anything ever trusts that native result downstream, bad data ships. Prioritize these.

Each number should trend down over time. None are allowlistable.

## New work lives in tiers 1 and 2 only

Execution priority is strict: tier 1 (whole-query delegation) > tier 2 (native SQL lowering) > tier 3 (local exec with subtree pushdown) > tier 4 (full local). **New coverage, pushdown shapes, and refactors are allowed only in tiers 1 and 2.** Tiers 3 and 4 are frozen — fix correctness regressions there, but do not expand.

If a compliance fix can live in tier 1 or 2, put it there. A compliance failure is never justification to grow tier 3/4.

## Common rationalizations

| Excuse | Reality |
|---|---|
| "Just allowlist it to unblock the PR" | The allowlist is reserved for the three categories above. Shim gaps are not one of them. |
| "It's only a small value diff" | A value diff means wrong numbers. Tolerances cover bounded float drift, not structural disagreement. |
| "Pass #1 is green, so diff_failure in pass #2 doesn't matter" | It does — prefer fell back after the native path computed wrong numbers. Silent correctness bug. |
| "Pass #2 failed so the suite is broken" | Pass #2 is informational. Prefer-mode exit code is the only gate. |
| "I'll wrap it in a 10-minute timeout to be safe" | Warm run is ~15s. Run it in the foreground. |
| "This deviance simplifies the SQL massively, I'll just add it" | Category 3 requires explicit user approval in the current conversation. Stop and ask. |

## Red flags — stop and re-read the policy

- Adding an entry to `expected-failures.json` without user approval
- Wrapping `scripts/run-compliance.sh` in a minute-long timeout
- Expanding tier 3 or tier 4 to close a compliance gap
- Writing an allowlist `reason` that amounts to "shim does not yet support X"

## Reference

- `harness/compliance/README.md` — full harness layout, fixture window, "gaps stay visible" policy.
- `AGENTS.md` § "Execution priority" — strict tier ordering and frozen-tier rule.
- `scripts/run-compliance.sh --help` — flag reference.
- `harness/compliance/expected-failures.json` — the live allowlist (keep it short).
