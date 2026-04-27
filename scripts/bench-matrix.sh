#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/bench-matrix.sh [options] [profile:path]...

Render a cross-profile Markdown matrix from benchmark reports, or a sweep matrix
from harness/artifacts/sweeps/<run-name>/manifest.json.

If no positional args are given, defaults to:
  7d:harness/artifacts/bench-report-7d.json
  30d:harness/artifacts/bench-report-30d.json
  1y:harness/artifacts/bench-report-1y.json

Options:
  --sweep PATH    Read a sweep manifest and render a v2 sweep matrix across
                  report/mode dimensions.
  --sort-by {np|fn|category|cat}
                  Sort cross-profile rows. np = max native/prom ratio
                  (default), fn = max fallback/native ratio, category = name.
  --per-query     Emit one row per query instead of collapsing by category.
  -h, --help      Show this help text.
EOF
}

ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    *) ARGS+=("$1"); shift ;;
  esac
done

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

BIN="$(mktemp -d)/promshim-bench-matrix"
go build -o "$BIN" ./cmd/promshim-bench-matrix
"$BIN" "${ARGS[@]}"
