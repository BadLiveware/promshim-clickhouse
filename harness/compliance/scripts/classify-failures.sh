#!/usr/bin/env bash
# classify-failures.sh - bucket compliance-report failures by shape.
set -euo pipefail
CALLER_PWD=$(pwd)

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
bucket=""
limit=0
report=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -b|--bucket) bucket="$2"; shift 2 ;;
    -n|--limit)  limit="$2"; shift 2 ;;
    -h|--help)   sed -n '2,8p' "$0"; exit 0 ;;
    -*)          echo "unknown flag: $1" >&2; exit 2 ;;
    *)           report="$1"; shift ;;
  esac
done

if [[ -z "$report" ]]; then
  report=$(ls -t "$ROOT/artifacts"/compliance-report-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no report found in $ROOT/artifacts/" >&2
    exit 1
  fi
fi

if [[ -n "$report" && ! -f "$report" && -f "$CALLER_PWD/$report" ]]; then
  report="$CALLER_PWD/$report"
fi

REPO_ROOT=$(cd "$ROOT/../.." && pwd)
BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
args=(--action classify --report "$report")
[[ -n "$bucket" ]] && args+=(--bucket "$bucket")
[[ "$limit" != "0" ]] && args+=(--limit "$limit")
"$BIN" "${args[@]}"
