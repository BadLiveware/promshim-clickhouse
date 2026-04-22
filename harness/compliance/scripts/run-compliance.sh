#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
SUBMODULE="${ROOT}/prom-compliance/promql"
TESTER="${SUBMODULE}/promql-compliance-tester"

if [[ ! -x "$TESTER" ]]; then
  echo ">> Building compliance tester..."
  (cd "$SUBMODULE" && go build ./cmd/promql-compliance-tester)
fi

OUTPUT_DIR="${ROOT}/artifacts"
mkdir -p "$OUTPUT_DIR"

# The upstream corpus was last updated for Prom 2.26. A few `should_fail`
# entries no longer fail under Prom 3.x, and the tester hard-aborts at
# comparer.go:95 when that happens. Generate a patched copy for each run.
PATCHED_QUERIES="${OUTPUT_DIR}/patched-queries.yml"
echo ">> Patching upstream corpus for Prom 3.x compatibility → ${PATCHED_QUERIES}"
python3 "${ROOT}/scripts/patch-queries-for-prom3.py" \
  --input "${SUBMODULE}/promql-test-queries.yml" \
  --output "${PATCHED_QUERIES}"

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUTPUT_FILE="${OUTPUT_DIR}/compliance-report-${STAMP}.json"

echo ">> Running compliance tester → ${OUTPUT_FILE}"
"$TESTER" \
  -config-file="${PATCHED_QUERIES}" \
  -config-file="${ROOT}/test-promshim.yml" \
  -output-format=json \
  -output-passing \
  > "$OUTPUT_FILE" || echo ">> Tester exited non-zero (expected when failures exist)"

echo ">> Report written to: ${OUTPUT_FILE}"

python3 "${ROOT}/scripts/apply-query-tolerances.py" \
  --report "${OUTPUT_FILE}" \
  --expected "${ROOT}/expected-failures.json" \
  --reference-url "http://localhost:29090" \
  --test-url "http://localhost:29091"

echo ">> Summary:"
jq '{
  total: .totalResults,
  passed: [.results[] | select(.diff == "" and (.unexpectedFailure // "") == "" and (.unexpectedSuccess // false) == false and (.unsupported // false) == false)] | length,
  diff_failure: [.results[] | select(.diff != "")] | length,
  unexpected_failure: [.results[] | select((.unexpectedFailure // "") != "")] | length,
  unexpected_success: [.results[] | select(.unexpectedSuccess == true)] | length,
  unsupported: [.results[] | select(.unsupported == true)] | length
}' < "$OUTPUT_FILE"

echo
echo ">> Reconciling against expected-failures allowlist"
"${ROOT}/scripts/reconcile-expected.sh" "$OUTPUT_FILE" || echo ">> Reconcile flagged drift (exit 1) — see above"
