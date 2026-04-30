#!/usr/bin/env bash
# seed-long-range.sh — one-shot: pump a 7-day demo_* dataset into the
# running ClickHouse *and* Prometheus via their respective Prometheus
# remote-write endpoints. Same generator, same series, same timestamps,
# so --long-range bench comparisons are apples-to-apples.
#
# The data lives in a pinned, non-overlapping benchmark time window in the
# selected target's observability.prometheus table. The isolated benchmark stack
# should be used for normal benchmark setup so long-range data does not
# contaminate the compliance fixture. Prometheus must have
# `storage.tsdb.out_of_order_time_window` set wide enough to accept
# backdated samples (the benchmark and compliance configs use 10y).
#
# Runtime: ~10–30s per target for 7 days of ~40 series at 15s step
# (~1.5M samples). Safe to re-run; CH dedupes at merge time by
# (id, timestamp); Prom OOO ingestion merges same-timestamp writes.
#
# Usage:
#   ./scripts/seed-long-range.sh --profile 7d | 30d | 1y | all
#   ./scripts/seed-long-range.sh --active-series-preset fast|profile-50k|profile-500k|dashboard-50k|envoy-heavy-50k|churn-50k
#   ./scripts/seed-long-range.sh --active-series 50000
#   ./scripts/seed-long-range.sh --density sparse|dense|stress-50k|stress-500k  (deprecated)
#   ./scripts/seed-long-range.sh --target ch|prom|both  (default: both)
#   ./scripts/seed-long-range.sh [--duration 168h] [--step 15s]
#                                [--end-time 2026-03-22T21:45:42Z]
#                                [--instances-per-job 5]
#                                [--jobs demo-api,demo-worker]
#                                [--workload-profile auto|legacy|dashboard|envoy-heavy|churn]
#                                [--ch-url http://localhost:28123]
#                                [--prom-url http://localhost:29090]
#                                [--ch-endpoint http://localhost:29092/write]
#                                [--prom-endpoint http://localhost:29090/api/v1/write]
#
# Adaptive seeder controls (parallel POSTs + AIMD regulator):
#   --batch-samples N         Approximate samples per POST (default 50000).
#                             Bigger batches → fewer round-trips, important
#                             for high-cardinality (stress-*) profiles.
#   --max-concurrency N       Hard ceiling on in-flight POSTs (default 8).
#   --initial-concurrency N   Regulator starting point (default 2).
#   --no-adaptive             Disable AIMD; run with fixed N=max-concurrency
#                             (deterministic, useful when benchmarking the
#                             seeder itself).
#   --probe-interval D        Health-probe poll cadence (default 5s).
#   --enable-probes           Turn on CH/Prom kill-switch probes (ch-probe-url
#                             and prom-probe-url default to --ch-url/--prom-url).
#   --max-host-load-pct N     Throttle when host CPU exceeds N%% (default 50,
#                             measured via /proc/stat sampling, reactive within
#                             one probe interval). Raise to 80–90 for CI or
#                             dedicated bench hosts. 0 disables.
#
# Named profiles (recommended; they pin matching corpora):
#   7d   → 2026-03-22T21:45:42Z - 7d @ 15s step  (~5M samples, ~5s wall)
#   30d  → 2026-02-22T21:45:42Z - 30d @ 60s step (~5M samples, ~5s wall)
#   1y   → 2025-03-22T21:45:42Z - 1y @ 300s step (~14M samples, ~15s wall)
# Each profile's end-time is distinct so the three datasets don't overlap
# inside the same observability.prometheus table. Step grows with duration
# so bench-time sample counts stay comparable across legacy dense profiles.
# Realistic workload profiles intentionally use 15s as the base step for 7d/30d
# so per-family sample intervals can model 15s, 60s, and 5m series.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
acquire_run_lock "stack"

cd "$REPO_ROOT"

PROFILE=""
DENSITY=""
ACTIVE_SERIES=""
ACTIVE_SERIES_PRESET=""
DURATION=""
STEP=""
END_TIME=""
INSTANCES_PER_JOB=""
JOBS="demo-api,demo-worker"
CH_URL="http://localhost:28123"
PROM_URL="http://localhost:29090"
CH_ENDPOINT="http://localhost:29092/write"
PROM_ENDPOINT="http://localhost:29090/api/v1/write"
TARGET="both"
BATCH_SAMPLES=""
MAX_CONCURRENCY=""
INITIAL_CONCURRENCY=""
NO_ADAPTIVE=""
PROBE_INTERVAL=""
ENABLE_PROBES=""
MAX_HOST_LOAD_PCT=""
WORKLOAD_PROFILE=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)             PROFILE="$2"; shift 2 ;;
    --active-series)       ACTIVE_SERIES="$2"; shift 2 ;;
    --active-series-preset|--named-active-series) ACTIVE_SERIES_PRESET="$2"; shift 2 ;;
    --density)             DENSITY="$2"; shift 2 ;;
    --duration)            DURATION="$2"; shift 2 ;;
    --step)                STEP="$2"; shift 2 ;;
    --end-time)            END_TIME="$2"; shift 2 ;;
    --instances-per-job)   INSTANCES_PER_JOB="$2"; shift 2 ;;
    --jobs)                JOBS="$2"; shift 2 ;;
    --workload-profile)    WORKLOAD_PROFILE="$2"; shift 2 ;;
    --target)              TARGET="$2"; shift 2 ;;
    --ch-url)              CH_URL="$2"; shift 2 ;;
    --prom-url)            PROM_URL="$2"; shift 2 ;;
    --ch-endpoint)         CH_ENDPOINT="$2"; shift 2 ;;
    --prom-endpoint)       PROM_ENDPOINT="$2"; shift 2 ;;
    # --endpoint retained for backcompat; routes to CH.
    --endpoint)            CH_ENDPOINT="$2"; shift 2 ;;
    --batch-samples)       BATCH_SAMPLES="$2"; shift 2 ;;
    --max-concurrency)     MAX_CONCURRENCY="$2"; shift 2 ;;
    --initial-concurrency) INITIAL_CONCURRENCY="$2"; shift 2 ;;
    --no-adaptive)         NO_ADAPTIVE="1"; shift ;;
    --probe-interval)      PROBE_INTERVAL="$2"; shift 2 ;;
    --enable-probes)       ENABLE_PROBES="1"; shift ;;
    --max-host-load-pct)   MAX_HOST_LOAD_PCT="$2"; shift 2 ;;
    -h|--help)             sed -n '1,/^set -e/p' "$0" | head -n 30; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 64 ;;
  esac
done

case "$TARGET" in
  ch|prom|both) ;;
  *) echo "error: --target must be ch|prom|both (got: $TARGET)" >&2; exit 64 ;;
esac
if [[ -z "$ACTIVE_SERIES" && -z "$ACTIVE_SERIES_PRESET" && -z "$DENSITY" ]]; then
  ACTIVE_SERIES_PRESET="fast"
fi

if [[ "$PROFILE" == "all" ]]; then
  for P in 7d 30d 1y; do
    args=(--profile "$P" --target "$TARGET" --jobs "$JOBS" --ch-url "$CH_URL" --prom-url "$PROM_URL" --ch-endpoint "$CH_ENDPOINT" --prom-endpoint "$PROM_ENDPOINT")
    [[ -n "$ACTIVE_SERIES" ]] && args+=(--active-series "$ACTIVE_SERIES")
    [[ -n "$ACTIVE_SERIES_PRESET" ]] && args+=(--active-series-preset "$ACTIVE_SERIES_PRESET")
    [[ -n "$DENSITY" ]] && args+=(--density "$DENSITY")
    [[ -n "$INSTANCES_PER_JOB" ]] && args+=(--instances-per-job "$INSTANCES_PER_JOB")
    [[ -n "$DURATION" ]] && args+=(--duration "$DURATION")
    [[ -n "$STEP" ]] && args+=(--step "$STEP")
    [[ -n "$END_TIME" ]] && args+=(--end-time "$END_TIME")
    [[ -n "$BATCH_SAMPLES" ]] && args+=(--batch-samples "$BATCH_SAMPLES")
    [[ -n "$MAX_CONCURRENCY" ]] && args+=(--max-concurrency "$MAX_CONCURRENCY")
    [[ -n "$INITIAL_CONCURRENCY" ]] && args+=(--initial-concurrency "$INITIAL_CONCURRENCY")
    [[ -n "$NO_ADAPTIVE" ]] && args+=(--no-adaptive)
    [[ -n "$PROBE_INTERVAL" ]] && args+=(--probe-interval "$PROBE_INTERVAL")
    [[ -n "$ENABLE_PROBES" ]] && args+=(--enable-probes)
    [[ -n "$MAX_HOST_LOAD_PCT" ]] && args+=(--max-host-load-pct "$MAX_HOST_LOAD_PCT")
    [[ -n "$WORKLOAD_PROFILE" ]] && args+=(--workload-profile "$WORKLOAD_PROFILE")
    "$0" "${args[@]}"
  done
  exit 0
fi

# Default to 7d if neither --profile nor an explicit --duration/--end-time was given.
if [[ -z "$PROFILE" && -z "$DURATION" && -z "$END_TIME" ]]; then
  PROFILE="7d"
fi

if [[ "$TARGET" == "ch" || "$TARGET" == "both" ]]; then
  if ! curl -sf -u default:otel -o /dev/null "${CH_URL}/ping"; then
    echo "error: ClickHouse not reachable at ${CH_URL}" >&2
    echo "  Bring up the compliance stack first:" >&2
    echo "    ./scripts/run-compliance.sh --keep-up --skip-classify" >&2
    exit 2
  fi
fi
if [[ "$TARGET" == "prom" || "$TARGET" == "both" ]]; then
  if ! curl -sf -o /dev/null "${PROM_URL}/-/ready"; then
    echo "error: Prometheus not reachable at ${PROM_URL}" >&2
    echo "  Bring up the compliance stack first:" >&2
    echo "    ./scripts/run-compliance.sh --keep-up --skip-classify" >&2
    exit 2
  fi
fi

BIN="$(mktemp -d)/ch-seed-long"
echo "[seed-long] building cmd/ch-seed-long → ${BIN}"
go build -o "$BIN" ./cmd/ch-seed-long

# Shared generator args; --profile overrides end-time/duration/step in the Go
# binary, so pass it when set and skip the explicit flags to avoid masking it.
COMMON_ARGS=(--jobs "$JOBS")
if [[ -n "$ACTIVE_SERIES" ]]; then COMMON_ARGS+=(--active-series "$ACTIVE_SERIES"); fi
if [[ -n "$ACTIVE_SERIES_PRESET" ]]; then COMMON_ARGS+=(--active-series-preset "$ACTIVE_SERIES_PRESET"); fi
if [[ -n "$DENSITY" ]]; then COMMON_ARGS+=(--density "$DENSITY"); fi
if [[ -n "$INSTANCES_PER_JOB" ]]; then COMMON_ARGS+=(--instances-per-job "$INSTANCES_PER_JOB"); fi
if [[ -n "$PROFILE" ]];  then COMMON_ARGS+=(--profile "$PROFILE"); fi
if [[ -n "$DURATION" ]]; then COMMON_ARGS+=(--duration "$DURATION"); fi
if [[ -n "$STEP" ]];     then COMMON_ARGS+=(--step "$STEP"); fi
if [[ -n "$END_TIME" ]]; then COMMON_ARGS+=(--end-time "$END_TIME"); fi
if [[ -n "$BATCH_SAMPLES" ]]; then COMMON_ARGS+=(--batch-samples "$BATCH_SAMPLES"); fi
if [[ -n "$MAX_CONCURRENCY" ]]; then COMMON_ARGS+=(--max-concurrency "$MAX_CONCURRENCY"); fi
if [[ -n "$INITIAL_CONCURRENCY" ]]; then COMMON_ARGS+=(--initial-concurrency "$INITIAL_CONCURRENCY"); fi
if [[ -n "$NO_ADAPTIVE" ]]; then COMMON_ARGS+=(--no-adaptive); fi
if [[ -n "$PROBE_INTERVAL" ]]; then COMMON_ARGS+=(--probe-interval "$PROBE_INTERVAL"); fi
if [[ -n "$MAX_HOST_LOAD_PCT" ]]; then COMMON_ARGS+=(--max-host-load-pct "$MAX_HOST_LOAD_PCT"); fi
if [[ -n "$WORKLOAD_PROFILE" ]]; then COMMON_ARGS+=(--workload-profile "$WORKLOAD_PROFILE"); fi

# CH probe URL: --enable-probes turns it on with the same CH_URL we ping.
CH_PROBE_ARGS=()
if [[ -n "$ENABLE_PROBES" ]]; then
  CH_PROBE_ARGS=(--ch-probe-url "$CH_URL" --prom-probe-url "$PROM_URL")
fi

if [[ "$TARGET" == "ch" || "$TARGET" == "both" ]]; then
  echo "[seed-long] target=ch endpoint=${CH_ENDPOINT}"
  "$BIN" --endpoint "$CH_ENDPOINT" --username default --password otel "${COMMON_ARGS[@]}" "${CH_PROBE_ARGS[@]}"
  # Nudge CH to merge parts so subsequent queries see dense data.
  echo "[seed-long] OPTIMIZE inner tables (best-effort)"
  curl -sfS --data-binary @- -u default:otel "${CH_URL}/?database=observability" <<<"SYSTEM FLUSH LOGS" >/dev/null || true
fi

if [[ "$TARGET" == "prom" || "$TARGET" == "both" ]]; then
  echo "[seed-long] target=prom endpoint=${PROM_ENDPOINT}"
  # Prom remote-write-receiver has no auth in the compliance stack; pass
  # empty username so withBasicAuth leaves the URL alone.
  "$BIN" --endpoint "$PROM_ENDPOINT" --username "" --password "" "${COMMON_ARGS[@]}" "${CH_PROBE_ARGS[@]}"
fi

EFFECTIVE_PROFILE="${PROFILE:-custom}"
cat <<EOF
[seed-long] done (profile=${EFFECTIVE_PROFILE}).

Next steps:
  - bench this window:
      ./scripts/run-bench.sh --long-range ${EFFECTIVE_PROFILE}
  - deep-dive a PromQL (use the profile's end_time):
      ./scripts/ch-explain.sh '<promql>' --eval-time '<profile-end-time>'
  - capture ProfileEvents for the new data:
      ./scripts/ch-profile-capture.sh --corpus harness/corpus/bench-native-lowering-${EFFECTIVE_PROFILE}.json
EOF
