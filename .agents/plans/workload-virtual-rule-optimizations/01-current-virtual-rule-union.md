# Phase 1 — Virtual-rule union and matcher pushdown

## Purpose

Finish the current targeted optimization pass for virtual recording-rule
expansion. This phase addresses the already-proven SQL blowup for
workload-owner dashboard queries while keeping the optimization structural and
safe for other virtual recording-rule unions.

## Evidence and priority

- `namespace_workload_pod:kube_pod_owner:relabel` appears 98 times in dashboard
  queries across 5 dashboards.
- Its rules include 8 definitions with static `workload_type` labels and complex
  dynamic branches using `group_left`, `label_replace`, `label_join`, and
  `topk`.
- Live validation before the first fixes produced roughly 6.5 MiB rendered SQL
  and ClickHouse parser backtracking.
- Current branch validation after matcher pushdown and static-label union reduced
  rendered SQL to roughly 234 KiB and returned HTTP 200 for the memory workload
  query.

## Scope

In scope:

- Keep static-label union collapse conservative.
- Preserve and harden selector matcher application after branch merge.
- Skip no-op match-all regex predicates.
- Push safe label-preserving matchers into rule leaves.
- Add explain diagnostics for applied and rejected virtual-rule optimizations.

Out of scope:

- Same-shape selector-variant grouping beyond the current static-label union.
- Native repeated-subtree factoring.
- Any rule-specific shortcut based on `namespace_workload_pod` by name.

## Implementation tasks

### 1. Stabilize current expansion behavior

Acceptance criteria:

- Query-time matchers are applied once after rule branch merge.
- `=~".*"` and equivalent match-all regexes do not generate predicate
  scaffolding.
- Safe matchers such as `cluster="..."` and `namespace="..."` are pushed to
  leaf selectors when every operator on the path preserves those labels.
- Unsafe shapes fall back to existing outer predicate scaffolding.

Validation:

- `go test ./internal/promshim/rules`
- Focused tests for applied pushdown, skipped match-all regex, and unsafe
  fallback.

### 2. Add virtual-rule optimization diagnostics

Acceptance criteria:

- Explain output can identify whether static-label union applied.
- Explain output includes compact counts for candidate branches, collapsed rows,
  and remaining branch groups where the data flow allows it.
- Rejected shapes have named skip reasons such as dynamic label mutation,
  incompatible static labels, unsupported vector matching, or unsafe selector
  overlap.

Validation:

- `go test ./internal/promshim/native/renderer ./internal/promshim`
- Live `/api/v1/query_range_explain` for the workload memory query shows an
  applied optimization reason and stable SQL length.

### 3. Revalidate the known workload query family

Representative queries:

- Namespace workload CPU usage/quota through
  `node_namespace_pod_container:container_cpu_usage_seconds_total:sum_rate5m`.
- Namespace workload memory usage/quota through container memory metrics or
  memory recording rules.
- Workload networking queries that enrich by workload owner.

Acceptance criteria:

- Memory and CPU workload queries return HTTP 200 in the KIND cluster through a
  local promshim with virtual rules enabled.
- Rendered SQL remains comfortably below the default ClickHouse
  `max_query_size` danger zone for the representative query shape.
- No ClickHouse parser backtracking error occurs.

Validation:

- `go test ./...`
- Live explain/run against `~/.kube/kind-ch-observability-poc.kubeconfig`.
- Confirm local port-forward and promshim processes are stopped after testing.

## Exit criteria

- Current workload-owner blowup remains fixed.
- The renderer and expansion layers expose enough diagnostic information to
  prioritize the next phase without manually inspecting multi-megabyte SQL.
- Full test suite passes.
