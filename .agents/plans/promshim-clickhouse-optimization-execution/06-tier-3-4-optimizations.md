# 06. Tier 3/4 local and subtree-pushdown optimizations

## Purpose and scope

Optimize tier 3 and tier 4 candidates where evidence shows they can beat native
SQL or provide the safe reference route for a family. This work is explicitly in
scope for CBE: the goal is to make local/subtree candidates cheaper and safer
when they are known-correct, not to add unrelated PromQL semantic coverage.

## Prerequisites

- CBE calibration identifies families where local or subtree-pushdown candidates
  are competitive or should serve under caps.
- Native SQL/IR optimization work has been attempted or rejected for the same
  family so tier 3/4 work is justified.
- Local/reference correctness is covered by compliance or focused differential
  tests.

## Affected areas

- `internal/promshim/local/`
- `internal/promshim/local/exec/`
- `internal/promshim/local/native_subtree.go`
- `internal/promshim/storage/`
- `internal/promshim/service.go`
- `internal/promshim/cbe_candidates.go`
- `internal/promshim/query_cost_class.go`
- `cmd/promshim-bench`
- pprof/benchmark helpers if local CPU/memory is the expected signal.

## Candidate queue

1. Reduce local round trips for range queries.
   - Current local query_range paths can issue many ClickHouse requests.
   - Expected signal: lower `X-Promshim-CH-Roundtrips`, CH millis, and wall-clock
     for local/subtree candidates.

2. Batch selector reads across steps or series where semantics allow.
   - Use exact input bounds and selector fingerprints to fetch once and evaluate
     multiple timestamps locally.
   - Expected signal: fewer ClickHouse requests and lower local decode overhead.

3. Reuse repeated selector/subtree buffers locally.
   - Use selector fingerprints and required-label metadata to avoid duplicate
     storage reads or duplicate range-function evaluation.
   - Expected signal: lower local CPU/heap or fewer storage calls for repeated
     subexpressions.

4. Optimize local range-function execution.
   - Profile `rate`, `increase`, and `avg_over_time` local execution over dense
     and long-range data.
   - Expected signal: lower CPU/allocations in Go benchmarks or pprof, stable
     output values.

5. Improve subtree pushdown boundaries.
   - Push only known-correct, high-value subtrees; keep vector matching,
     histograms, and subqueries conservative unless focused evidence exists.
   - Expected signal: lower transfer/round trips while preserving local semantics.

6. Add hard caps and explainable rejection reasons.
   - Local/subtree candidates must reject high-cardinality or high-output shapes
     before they harm production.
   - Expected signal: route decisions show cap reasons and dense controls do not
     accidentally serve bad candidates.

## Implementation tasks

1. Pick a CBE-justified family.
   - Use calibration artifacts to choose one family where tier 3/4 is already
     competitive or near-competitive.
   - Acceptance: artifact note names strict/native cost, local/subtree cost,
     expected signal, and caps.

2. Capture local/subtree profiles before changes.
   - Use bench reports for CH round trips and CH millis.
   - Use Go benchmarks or pprof when local CPU/heap is the expected signal.
   - Capture memory summaries for query-log correlation.
   - Acceptance: baseline identifies whether bottleneck is round trips, decode,
     local CPU, heap, transfer, or candidate selection.

3. Implement one local/subtree optimization.
   - Keep correctness logic separate from transport/storage plumbing.
   - Add tests for exact time bounds, label requirements, staleness/NaN behavior,
     and cap/rejection reasons relevant to the family.
   - Add targeted comments only for non-obvious compatibility or semantic
     constraints.
   - Acceptance: tests fail without the optimization or cap and pass with it.

4. Add or update CBE candidate metadata.
   - Ensure explain output identifies why tier 3/4 is eligible or rejected.
   - Include round-trip estimates, input/output caps, and family gate decisions.
   - Acceptance: explain output gives enough information to understand serving.

5. Validate against native and local reference.
   - Compare strict/native, optimized local/subtree, and full local reference.
   - Run dense/long-range controls before enabling served `cost_prefer`.
   - Acceptance: no unexpected divergence; route/candidate flips are explained.

6. Feed results back into calibration.
   - Regenerate calibration artifacts if serving decisions should change.
   - Keep low-confidence families strict/reference.
   - Acceptance: `.pi/cost-routing-calibration.*` and docs reflect the evidence.

## Validation tasks

Fast checks:

```bash
go test ./internal/promshim/...
go test ./internal/promharness ./cmd/promshim-bench
go test ./cmd/promshim-routing-calibrate/...
bash -n scripts/run-sweep.sh scripts/run-bench.sh scripts/bench-matrix.sh
git diff --check
```

Local/subtree family sweeps:

```bash
PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=<family> ./scripts/run-sweep.sh \
  --name tier34-<family>-7d-sparse \
  --profile 7d --density sparse --seed reuse \
  --skip-compliance --shim-modes prefer,off \
  --routing-policies strict,cost_shadow,cost_prefer \
  --warmup-routing-policies cost_shadow \
  --corpus-set native --memory summary
```

Dense or long-range controls:

```bash
PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES=<family> ./scripts/run-sweep.sh \
  --name tier34-<family>-dense-control \
  --profile 7d --density dense --seed reuse \
  --skip-compliance --shim-modes prefer,off \
  --routing-policies strict,cost_prefer \
  --corpus-set processing --memory summary
```

Correctness gate:

```bash
./scripts/run-sweep.sh --name tier34-<family>-compliance --skip-bench
```

If local CPU/heap was optimized, add targeted Go benchmarks or pprof captures and
record before/after allocation and CPU evidence in the artifact note.

## Exit criteria

- At least one tier 3/4 optimization is implemented with evidence, or a
  calibration-backed rejection explains why no tier 3/4 work is currently worth
  landing.
- Candidate explain metadata shows eligibility, caps, and rejection reasons.
- Dense/long-range controls prevent route cliffs.
- Calibration is updated if the tier 3/4 serving decision changes.
- Compliance remains clean except existing allowed deviances.

## Final handoff

After this file, continue optimization as a repeating measured workflow:

1. choose a family from calibration gaps;
2. define semantic risk and expected signal;
3. preserve a baseline;
4. change one variable;
5. validate with EXPLAIN/ProfileEvents/query-log or Go profiles;
6. update calibration only from named artifacts;
7. serve only behind family/profile gates; and
8. keep rollback configuration ready.
