# PromQL coverage


### Supported in tier 2 native SQL

Tier 2 native SQL is a complete PromQL execution path for the float-sample and
classic-histogram surface this repo targets. In `force_supported` mode, the full
upstream compliance suite and repo-owned harness suites run with **no unsupported
native roots**.

Within that scalar/classic-histogram scope, this means the full PromQL
expression surface: selectors, matchers, `offset`, `@`, subqueries,
aggregations, selection aggregations, scalar and vector arithmetic, comparisons,
vector matching, set operators, range functions, counter functions, classic
histogram bucket queries and histogram helper functions, label mutation, sort
functions, scalar roots, `absent`, `absent_over_time`, `info`, and the rest of
the PromQL feature set exercised by the upstream parser/compliance suite and our
repo-owned harnesses.

### Not supported

- **Prometheus native histogram samples.** Promshim supports classic Prometheus
  histograms represented as `_bucket`, `_sum`, and `_count` time series. Native
  histogram sample payloads are outside the current scope because the ClickHouse
  `TimeSeries` remote-write/read path used by this repo does not currently store
  or round-trip those native histogram payloads.

### Accepted deviations

Known accepted deviations are intentionally narrow and live in
`harness/compliance/expected-failures.json`:

- **`topk` exact-tie ordering:** Prometheus's tie-break depends on TSDB series
  iteration order, which is a storage-engine implementation detail and is not
  derivable from labels alone.
- **ClickHouse-vs-Go modulo float drift:** absolute error must stay within
  `1e-6`, with labels and timestamps still matching exactly. This accounts for
  two different floating-point remainder algorithms. ClickHouse computes modulo
  as `x - trunc(x / y) * y`, which performs a division, truncation,
  multiplication, and subtraction. Go/Prometheus uses Go's `math.Mod`, whose
  portable implementation repeatedly subtracts scaled powers-of-two multiples of
  `abs(y)` using `Frexp`/`Ldexp`, then reapplies the sign of `x`. Both implement
  the same remainder semantics, but they round at different intermediate steps,
  so very large operands can differ by tiny amounts.

Anything else is treated as a visible bug or coverage gap, not something to
hide in the allowlist.

### Validation

The compatibility claim is checked continuously against Prometheus, not inferred
from a few examples:

- `go run ./cmd/promshim-matrix` generates
  `harness/artifacts/matrices/path2-compliance-matrix.md` and
  `harness/artifacts/matrices/path2-compliance-matrix.json` from the
  parser-visible feature surface.
- `./scripts/run-compliance.sh` runs the full upstream
  `prometheus/compliance` PromQL suite against reference Prometheus and promshim
  on the same frozen fixture.
- `./scripts/run-harness.sh` runs repo-owned differential corpora,
  dashboard-focused corpora, compliance, and the benchmark tripwire.

Current compatibility matrix, refreshed after the native-lowering IR migration:

| Surface | Matrix rows | Tier-2 native SQL status | Notes |
|---|---:|---|---|
| Selectors and matchers | 5 | 5/5 supported | Instant selectors plus equality, inequality, regex, and negative-regex matchers. |
| Time modifiers | 5 | 5/5 supported | `offset`, literal `@`, `@ start()`, `@ end()`, and selector subqueries. |
| Aggregations | 14 | 14/14 supported | Includes ordinary aggregations plus `topk`, `bottomk`, `count_values`, `limitk`, and `limit_ratio`. |
| Binary and set operators | 16 | 16/16 supported | Arithmetic, comparison, bool comparison, `and`, `or`, and `unless`. |
| Vector matching | 5 | 5/5 supported | `on`, `ignoring`, `group_left`, `group_right`, and `fill`. |
| Functions | 83 | 83/83 supported | Range functions, counter functions, scalar/math functions, label mutation, sort family, `absent*`, `info`, and classic-histogram helpers. |
| **Total parser-visible matrix** | **128** | **128/128 supported** | Generated from Prometheus parser version `v0.311.2`. |

Latest upstream compliance gate run:

| Mode | Total queries | Passed exactly or within tolerance | Expected diff | Unsupported roots | Unexpected failures |
|---|---:|---:|---:|---:|---:|
| `prefer` | 539 | 538 | 1 `topk` exact-tie ordering case | 0 | 0 |
| `force_supported` | 539 | 538 | 1 `topk` exact-tie ordering case | 0 | 0 |

The single diff is the documented `topk` TSDB-order tie-break. The modulo drift
case is handled by the explicit `1e-6` tolerance and matched during the run.
