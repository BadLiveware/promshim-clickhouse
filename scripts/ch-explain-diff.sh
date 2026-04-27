#!/usr/bin/env bash
# ch-explain-diff.sh — run ch-explain against the same PromQL on two git
# refs and diff the EXPLAIN SYNTAX output (and optionally PLAN/PIPELINE).
#
# Key use case: bisect whether a claimed optimizer rewrite actually changes
# what ClickHouse executes. If EXPLAIN SYNTAX is byte-identical between two
# commits, the rewrite is text-only — ClickHouse folded it.
#
# Runtime: roughly 2 × (rebuild shim + 1 bench-query time) + 4 × EXPLAIN.
# On a warm docker cache that's ~30–60s; worst case (cold build) ~2–3min.
#
# IMPORTANT: this script checks out each ref into a temporary git worktree
# so it does not touch your current working tree or branch. Still, it
# rebuilds and restarts the shim container, so don't run it in parallel
# with a live bench.
#
# Usage:
#   ./scripts/ch-explain-diff.sh <sha-before> <sha-after> '<promql>' [flags...]
# Flags are passed through to ch-explain.sh.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"
# shellcheck source=lib/artifacts.sh
source "${REPO_ROOT}/scripts/lib/artifacts.sh"
acquire_run_lock "stack"

if [[ $# -lt 3 ]]; then
  echo "Usage: $0 <sha-before> <sha-after> '<promql>' [ch-explain flags...]" >&2
  exit 64
fi

BEFORE_REF="$1"; shift
AFTER_REF="$1"; shift
PROMQL="$1"; shift
EXTRA=("$@")

STAMP=$(date +%Y%m%d-%H%M%S)
OUT_ROOT="$(artifact_abs "explain-diff/${STAMP}")"
mkdir -p "$OUT_ROOT"

run_one() {
  local ref="$1" label="$2"
  local wt="${OUT_ROOT}/wt-${label}"
  local art="${OUT_ROOT}/${label}"

  echo "[explain-diff] worktree for ${ref} → ${wt}"
  git -C "$REPO_ROOT" worktree add -f --detach "$wt" "$ref" >/dev/null

  echo "[explain-diff] rebuild + restart shim for ${ref}"
  (cd "${wt}/harness/compliance" && docker compose build promshim >/dev/null)
  (cd "${wt}/harness/compliance" && docker compose up -d promshim >/dev/null)
  # Wait for shim readiness — the existing run-bench pattern.
  for _ in $(seq 1 60); do
    curl -sf http://localhost:29091/-/ready >/dev/null && break
    sleep 1
  done

  "${wt}/scripts/ch-explain.sh" "$PROMQL" --output "$art" "${EXTRA[@]}"

  git -C "$REPO_ROOT" worktree remove -f "$wt" >/dev/null || true
}

run_one "$BEFORE_REF" before
run_one "$AFTER_REF"  after

echo
echo "[explain-diff] diffs (use --color=always for terminal):"
for f in explain-syntax.sql explain-plan.txt explain-pipeline.json explain-estimate.json query.sql; do
  for qdir in "$OUT_ROOT/before"/q*; do
    [[ -d "$qdir" ]] || continue
    q=$(basename "$qdir")
    BEFORE_F="${OUT_ROOT}/before/${q}/${f}"
    AFTER_F="${OUT_ROOT}/after/${q}/${f}"
    [[ -f "$BEFORE_F" && -f "$AFTER_F" ]] || continue

    DIFF_OUT="${OUT_ROOT}/${q}-${f%.*}.diff"
    if diff -u "$BEFORE_F" "$AFTER_F" >"$DIFF_OUT"; then
      echo "  ${q}/${f}: identical"
    else
      DELTA=$(wc -l <"$DIFF_OUT")
      echo "  ${q}/${f}: CHANGED (${DELTA} lines in diff → ${DIFF_OUT})"
    fi
  done
done

# Summarize verdict per shape.
IDENTICAL_SYNTAX=1
for qdir in "$OUT_ROOT/before"/q*; do
  [[ -d "$qdir" ]] || continue
  q=$(basename "$qdir")
  BF="${OUT_ROOT}/before/${q}/explain-syntax.sql"
  AF="${OUT_ROOT}/after/${q}/explain-syntax.sql"
  [[ -f "$BF" && -f "$AF" ]] || { IDENTICAL_SYNTAX=0; continue; }
  cmp -s "$BF" "$AF" || IDENTICAL_SYNTAX=0
done

echo
if (( IDENTICAL_SYNTAX == 1 )); then
  echo "verdict: EXPLAIN SYNTAX is byte-identical across ${BEFORE_REF} and ${AFTER_REF}."
  echo "         ClickHouse's syntactic rewriter produces the same query tree,"
  echo "         so any 'CSE alias' textual change between these SHAs is cosmetic."
else
  echo "verdict: EXPLAIN SYNTAX differs — the rewrite is reaching the executor."
  echo "         Now check EXPLAIN PLAN / PIPELINE diffs to see if it changes"
  echo "         bytes read or processor shape."
fi
echo
echo "artifacts: ${OUT_ROOT}"
