#!/usr/bin/env bash
# run-lock.sh — named flock guards for bench/profile tooling.
#
# Most measurement scripts share a single physical resource — the
# compliance stack (one promshim, one ClickHouse, one shared
# system.query_log time-window) — so any two concurrent stack-using
# runs silently corrupt each other's artifacts. This library lets each
# script name the resource it needs and refuses to race on that name.
#
# Locks are keyed by name. Each distinct name gets its own lockfile
# at ${TMPDIR:-/tmp}/ch-observability-<name>.lock, so two scripts
# taking different names can run in parallel; two scripts taking the
# same name serialize.
#
# Usage, from inside a script:
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/run-lock.sh"
#   acquire_run_lock "stack"              # most stack-using scripts
#   acquire_run_lock "harness"            # orchestrator-level
#   acquire_run_lock "stack" "harness"    # hold both (rare)
#
# Recommended names for this repo:
#   stack   — exclusive access to the compliance stack (promshim:29091,
#             CH:28123, Prom:29090, and the system.query_log window).
#             Taken by run-bench, run-compliance, seed-long-range,
#             ch-explain, ch-explain-diff, ch-profile-capture.
#   harness — orchestrator-level; held by run-harness so two full
#             harness runs can't interleave, but individual inner
#             phases only hold "stack" while they're running — so
#             external stack-users can grab the stack between phases.
#
# Inheritance: nested calls (e.g. run-harness -> run-compliance;
# run-bench --bring-up -> run-compliance) inherit their parent's held
# locks via CHO_RUN_LOCK_HELD_<uppercase-name>=1 and skip
# re-acquisition. Inherited FDs stay open in the child, so the OS
# releases the lock when the outermost holder dies — even on kill -9.

_cho_lock_fd_for() {
  # Map a name to a stable FD number in [10, 19]. Hash-derived so two
  # names can be held simultaneously without collision. We keep FDs 0-9
  # free for the caller's stdio and conventional redirections.
  local name="$1"
  local h
  h=$(printf '%s' "$name" | cksum | awk '{print $1}')
  echo $(( 10 + h % 10 ))
}

_cho_lock_holdvar_for() {
  local name="$1"
  local upper
  upper=$(printf '%s' "$name" | tr '[:lower:]-' '[:upper:]_')
  printf 'CHO_RUN_LOCK_HELD_%s' "$upper"
}

acquire_run_lock() {
  if (( $# == 0 )); then
    echo "error: acquire_run_lock requires at least one lock name" >&2
    exit 3
  fi
  if ! command -v flock >/dev/null 2>&1; then
    echo "error: flock(1) not available; refusing to run without a race guard" >&2
    exit 3
  fi
  local name holdvar lockfile fd
  for name in "$@"; do
    holdvar=$(_cho_lock_holdvar_for "$name")
    if [[ "${!holdvar:-0}" == "1" ]]; then
      continue  # inherited from a parent scope
    fi
    lockfile="${TMPDIR:-/tmp}/ch-observability-${name}.lock"
    fd=$(_cho_lock_fd_for "$name")
    eval "exec ${fd}>>\"\$lockfile\"" || {
      echo "error: cannot open lock file $lockfile" >&2
      exit 3
    }
    if ! flock -n "$fd"; then
      local holder
      holder=$(head -1 "$lockfile" 2>/dev/null || true)
      echo "error: another ${name}-locked run is active (lock: $lockfile)" >&2
      if [[ -n "$holder" ]]; then
        echo "  held by: $holder" >&2
      fi
      echo "  wait for it to finish, or — if you're certain nothing is running —" >&2
      echo "  rm $lockfile" >&2
      exit 3
    fi
    : >"$lockfile"
    printf 'pid=%d script=%s lock=%s started=%s host=%s\n' \
      "$$" "${0##*/}" "$name" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(hostname)" >&"$fd"
    export "$holdvar=1"
  done
}
