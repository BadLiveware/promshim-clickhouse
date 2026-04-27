#!/usr/bin/env bash
# native-gap-report.sh — summarize a native-mode compliance report by gap category.
set -euo pipefail
CALLER_PWD=$(pwd)

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
report="${1:-}"
if [[ -z "$report" ]]; then
  report=$(ls -t "$ROOT/artifacts"/compliance-report-native-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no native-mode report found in $ROOT/artifacts/" >&2
    exit 2
  fi
fi
if [[ -n "$report" && ! -f "$report" && -f "$CALLER_PWD/$report" ]]; then
  report="$CALLER_PWD/$report"
fi
if [[ ! -f "$report" ]]; then
  echo "report not found: $report" >&2
  exit 2
fi

REPO_ROOT=$(cd "$ROOT/../.." && pwd)
BIN="$(mktemp -d)/promshim-compliance-report"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/promshim-compliance-report)
"$BIN" --action native-gap --report "$report"
