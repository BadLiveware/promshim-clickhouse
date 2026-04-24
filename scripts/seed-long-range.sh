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
#   ./scripts/seed-long-range.sh --density sparse|dense  (default: sparse)
#   ./scripts/seed-long-range.sh --target ch|prom|both  (default: both)
#   ./scripts/seed-long-range.sh [--duration 168h] [--step 15s]
#                                [--end-time 2026-03-22T21:45:42Z]
#                                [--instances-per-job 5]
#                                [--jobs demo-api,demo-worker]
#                                [--ch-url http://localhost:28123]
#                                [--prom-url http://localhost:29090]
#                                [--ch-endpoint http://localhost:29092/write]
#                                [--prom-endpoint http://localhost:29090/api/v1/write]
#
# Named profiles (recommended; they pin matching corpora):
#   7d   → 2026-03-22T21:45:42Z - 7d @ 15s step  (~5M samples, ~5s wall)
#   30d  → 2026-02-22T21:45:42Z - 30d @ 60s step (~5M samples, ~5s wall)
#   1y   → 2025-03-22T21:45:42Z - 1y @ 300s step (~14M samples, ~15s wall)
# Each profile's end-time is distinct so the three datasets don't overlap
# inside the same observability.prometheus table. Step grows with duration
# so bench-time sample counts stay comparable across profiles.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
acquire_run_lock "stack"

cd "$REPO_ROOT"

PROFILE=""
DENSITY="sparse"
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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)             PROFILE="$2"; shift 2 ;;
    --density)             DENSITY="$2"; shift 2 ;;
    --duration)            DURATION="$2"; shift 2 ;;
    --step)                STEP="$2"; shift 2 ;;
    --end-time)            END_TIME="$2"; shift 2 ;;
    --instances-per-job)   INSTANCES_PER_JOB="$2"; shift 2 ;;
    --jobs)                JOBS="$2"; shift 2 ;;
    --target)              TARGET="$2"; shift 2 ;;
    --ch-url)              CH_URL="$2"; shift 2 ;;
    --prom-url)            PROM_URL="$2"; shift 2 ;;
    --ch-endpoint)         CH_ENDPOINT="$2"; shift 2 ;;
    --prom-endpoint)       PROM_ENDPOINT="$2"; shift 2 ;;
    # --endpoint retained for backcompat; routes to CH.
    --endpoint)            CH_ENDPOINT="$2"; shift 2 ;;
    -h|--help)             sed -n '1,/^set -e/p' "$0" | head -n 30; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 64 ;;
  esac
done

case "$TARGET" in
  ch|prom|both) ;;
  *) echo "error: --target must be ch|prom|both (got: $TARGET)" >&2; exit 64 ;;
esac
case "$DENSITY" in
  sparse|dense) ;;
  *) echo "error: --density must be sparse|dense (got: $DENSITY)" >&2; exit 64 ;;
esac

if [[ "$PROFILE" == "all" ]]; then
  for P in 7d 30d 1y; do
    args=(--profile "$P" --density "$DENSITY" --target "$TARGET" --jobs "$JOBS" --ch-url "$CH_URL" --prom-url "$PROM_URL" --ch-endpoint "$CH_ENDPOINT" --prom-endpoint "$PROM_ENDPOINT")
    [[ -n "$INSTANCES_PER_JOB" ]] && args+=(--instances-per-job "$INSTANCES_PER_JOB")
    [[ -n "$DURATION" ]] && args+=(--duration "$DURATION")
    [[ -n "$STEP" ]] && args+=(--step "$STEP")
    [[ -n "$END_TIME" ]] && args+=(--end-time "$END_TIME")
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
COMMON_ARGS=(--jobs "$JOBS" --density "$DENSITY")
if [[ -n "$INSTANCES_PER_JOB" ]]; then COMMON_ARGS+=(--instances-per-job "$INSTANCES_PER_JOB"); fi
if [[ -n "$PROFILE" ]];  then COMMON_ARGS+=(--profile "$PROFILE"); fi
if [[ -n "$DURATION" ]]; then COMMON_ARGS+=(--duration "$DURATION"); fi
if [[ -n "$STEP" ]];     then COMMON_ARGS+=(--step "$STEP"); fi
if [[ -n "$END_TIME" ]]; then COMMON_ARGS+=(--end-time "$END_TIME"); fi

if [[ "$TARGET" == "ch" || "$TARGET" == "both" ]]; then
  echo "[seed-long] target=ch endpoint=${CH_ENDPOINT}"
  "$BIN" --endpoint "$CH_ENDPOINT" --username default --password otel "${COMMON_ARGS[@]}"
  # Nudge CH to merge parts so subsequent queries see dense data.
  echo "[seed-long] OPTIMIZE inner tables (best-effort)"
  curl -sfS --data-binary @- -u default:otel "${CH_URL}/?database=observability" <<<"SYSTEM FLUSH LOGS" >/dev/null || true
fi

if [[ "$TARGET" == "prom" || "$TARGET" == "both" ]]; then
  echo "[seed-long] target=prom endpoint=${PROM_ENDPOINT}"
  # Prom remote-write-receiver has no auth in the compliance stack; pass
  # empty username so withBasicAuth leaves the URL alone.
  "$BIN" --endpoint "$PROM_ENDPOINT" --username "" --password "" "${COMMON_ARGS[@]}"
fi

EFFECTIVE_PROFILE="${PROFILE:-custom}"
cat <<EOF
[seed-long] done (profile=${EFFECTIVE_PROFILE}, density=${DENSITY}).

Next steps:
  - bench this window:
      ./scripts/run-bench.sh --long-range ${EFFECTIVE_PROFILE}
  - deep-dive a PromQL (use the profile's end_time):
      ./scripts/ch-explain.sh '<promql>' --eval-time '<profile-end-time>'
  - capture ProfileEvents for the new data:
      ./scripts/ch-profile-capture.sh --corpus harness/corpus/bench-native-lowering-${EFFECTIVE_PROFILE}.json
EOF
