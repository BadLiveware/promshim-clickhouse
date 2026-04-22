#!/usr/bin/env bash
# seed-long-range.sh — one-shot: pump a 7-day demo_* dataset directly into
# the running ClickHouse via its Prometheus remote-write endpoint (:29092).
#
# The data lives in the same observability.prometheus table as the 10m
# compliance fixture, but in a non-overlapping time window (default:
# ending 2026-03-22T21:45:42Z, 30 days before the fixture). That lets the
# long-range bench profile pin its own --eval-time and see meaningful
# SelectedRows / ReadCompressedBytes signal without disturbing correctness
# tests on the 10m fixture.
#
# Runtime: ~10–30s for 7 days of ~40 series at 15s step (~1.5M samples),
# dominated by CH insert + first merge. Safe to re-run; duplicate inserts
# are deduplicated at merge time by (id, timestamp).
#
# Usage:
#   ./scripts/seed-long-range.sh --profile 7d | 30d | 1y
#   ./scripts/seed-long-range.sh [--duration 168h] [--step 15s]
#                                [--end-time 2026-03-22T21:45:42Z]
#                                [--instances-per-job 5]
#                                [--jobs demo-api,demo-worker]
#                                [--endpoint http://localhost:29092/write]
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
cd "$REPO_ROOT"

PROFILE=""
DURATION=""
STEP=""
END_TIME=""
INSTANCES_PER_JOB=5
JOBS="demo-api,demo-worker"
ENDPOINT="http://localhost:29092/write"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)             PROFILE="$2"; shift 2 ;;
    --duration)            DURATION="$2"; shift 2 ;;
    --step)                STEP="$2"; shift 2 ;;
    --end-time)            END_TIME="$2"; shift 2 ;;
    --instances-per-job)   INSTANCES_PER_JOB="$2"; shift 2 ;;
    --jobs)                JOBS="$2"; shift 2 ;;
    --endpoint)            ENDPOINT="$2"; shift 2 ;;
    -h|--help)             sed -n '1,/^set -e/p' "$0" | head -n 30; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 64 ;;
  esac
done

# Default to 7d if neither --profile nor an explicit --duration/--end-time was given.
if [[ -z "$PROFILE" && -z "$DURATION" && -z "$END_TIME" ]]; then
  PROFILE="7d"
fi

# Sanity: ClickHouse has to be up on the native HTTP port before remote-write will accept.
if ! curl -sf -u default:otel -o /dev/null "http://localhost:28123/ping"; then
  echo "error: ClickHouse not reachable at http://localhost:28123" >&2
  echo "  Bring up the compliance stack first:" >&2
  echo "    ./scripts/run-compliance.sh --keep-up --skip-classify" >&2
  exit 2
fi

BIN="$(mktemp -d)/ch-seed-long"
echo "[seed-long] building cmd/ch-seed-long → ${BIN}"
go build -o "$BIN" ./cmd/ch-seed-long

# Assemble generator args. --profile overrides end-time/duration/step in the Go
# binary, so pass it when set and skip the explicit flags to avoid masking it.
GEN_ARGS=(--endpoint "$ENDPOINT" --jobs "$JOBS" --instances-per-job "$INSTANCES_PER_JOB")
if [[ -n "$PROFILE" ]]; then
  GEN_ARGS+=(--profile "$PROFILE")
fi
if [[ -n "$DURATION" ]]; then GEN_ARGS+=(--duration "$DURATION"); fi
if [[ -n "$STEP" ]];     then GEN_ARGS+=(--step "$STEP"); fi
if [[ -n "$END_TIME" ]]; then GEN_ARGS+=(--end-time "$END_TIME"); fi

echo "[seed-long] running generator"
"$BIN" "${GEN_ARGS[@]}"

# Nudge CH to merge parts so subsequent queries see dense data; not strictly
# required but makes a follow-up bench run more predictable.
echo "[seed-long] OPTIMIZE inner tables (best-effort)"
curl -sfS --data-binary @- -u default:otel "http://localhost:28123/?database=observability" <<<"SYSTEM FLUSH LOGS" >/dev/null || true

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
