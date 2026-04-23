#!/usr/bin/env bash
# run-lock.sh — shared lockfile guard for bench/profile tooling.
#
# Two concurrent bench or capture runs will silently corrupt each other:
# they share the compliance stack (latencies interleave), the query_log
# time-window (normalizeQuery aggregation merges both runs into one row),
# and harness/artifacts/*.json (last writer wins). We refuse to race —
# second caller exits non-zero with a pointer at whoever holds the lock.
#
# Usage, from inside a script:
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/run-lock.sh"
#   acquire_run_lock "run-bench"
#
# Nested scripts (e.g. ch-profile-capture.sh -> run-bench.sh) inherit the
# lock via CHO_RUN_LOCK_HELD=1 and skip re-acquisition. That's what makes
# the lock compose; without it the parent would deadlock waiting on
# itself. The inherited FD stays open in the child, so the OS releases
# the lock when the outermost holder dies — even on kill -9.

CHO_RUN_LOCK_FILE="${CHO_RUN_LOCK_FILE:-${TMPDIR:-/tmp}/ch-observability-run.lock}"

acquire_run_lock() {
  local name="${1:-run}"
  if [[ "${CHO_RUN_LOCK_HELD:-0}" == "1" ]]; then
    return 0
  fi
  if ! command -v flock >/dev/null 2>&1; then
    echo "error: flock(1) not available; refusing to run without a race guard" >&2
    exit 3
  fi
  exec 9>>"$CHO_RUN_LOCK_FILE" || {
    echo "error: cannot open lock file $CHO_RUN_LOCK_FILE" >&2
    exit 3
  }
  if ! flock -n 9; then
    local holder
    holder=$(head -1 "$CHO_RUN_LOCK_FILE" 2>/dev/null || true)
    echo "error: another bench/profile run is active (lock: $CHO_RUN_LOCK_FILE)" >&2
    if [[ -n "$holder" ]]; then
      echo "  held by: $holder" >&2
    fi
    echo "  wait for it to finish, or — if you're certain nothing is running —" >&2
    echo "  rm $CHO_RUN_LOCK_FILE" >&2
    exit 3
  fi
  : >"$CHO_RUN_LOCK_FILE"
  printf 'pid=%d script=%s started=%s host=%s\n' \
    "$$" "$name" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(hostname)" >&9
  export CHO_RUN_LOCK_HELD=1
  export CHO_RUN_LOCK_FILE
}
