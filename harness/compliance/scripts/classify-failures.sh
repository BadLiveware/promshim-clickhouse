#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: classify-failures.sh [REPORT] [--bucket NAME] [--limit N]

Bucket compliance-report failures by shape. If REPORT is omitted, the latest
report in PROM_SHIM_COMPLIANCE_ARTIFACT_DIR is used; when unset, the default is
harness/artifacts/compliance/latest.

Options:
  -b, --bucket NAME  Show only one bucket.
  -n, --limit N      Limit rows printed by the report command.
  -h, --help         Show this help.
EOF
}

CALLER_PWD=$(pwd)

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
REPO_ROOT=$(cd "$ROOT/../.." && pwd)
DEFAULT_ARTIFACT_DIR="${PROM_SHIM_COMPLIANCE_ARTIFACT_DIR:-${REPO_ROOT}/harness/artifacts/compliance/latest}"
bucket=""
limit=0
report=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -b|--bucket) bucket="$2"; shift 2 ;;
    -n|--limit)  limit="$2"; shift 2 ;;
    -h|--help)   usage; exit 0 ;;
    -*)          echo "unknown flag: $1" >&2; exit 2 ;;
    *)           report="$1"; shift ;;
  esac
done

if [[ -z "$report" ]]; then
  report=$(ls -t "$DEFAULT_ARTIFACT_DIR"/compliance-report-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no report found in $DEFAULT_ARTIFACT_DIR/" >&2
    exit 1
  fi
fi

if [[ -n "$report" && ! -f "$report" && -f "$CALLER_PWD/$report" ]]; then
  report="$CALLER_PWD/$report"
fi

BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
args=(--action classify --report "$report")
[[ -n "$bucket" ]] && args+=(--bucket "$bucket")
[[ "$limit" != "0" ]] && args+=(--limit "$limit")
"$BIN" "${args[@]}"
