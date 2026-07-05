#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: run-compliance.sh [--mode MODE] [--suffix NAME]

Run the compliance tester against whatever promshim is currently up.
Produces a tester report under harness/artifacts/compliance/ and reconciles
against the expected-failures allowlist.

Options:
  --mode MODE      Mode name to record on the report; also passed to
                   reconcile-expected.sh so mode-tagged allowlist
                   entries apply correctly. Default: unset (applies
                   only entries with no `modes` field).
  --suffix NAME    Suffix for the report filename, used to keep two
                   back-to-back passes distinguishable.
                   Default: "" (report named compliance-report-<stamp>.json).
  -h, --help       Show this help.
EOF
}

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
REPO_ROOT=$(cd "${ROOT}/../.." && pwd)
SUBMODULE="${ROOT}/prom-compliance/promql"
TESTER="${SUBMODULE}/promql-compliance-tester"

mode=""
suffix=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)    mode="$2"; shift 2 ;;
    --suffix)  suffix="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *)         echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ! -x "$TESTER" ]]; then
  echo ">> Building compliance tester..."
  (cd "$SUBMODULE" && go build ./cmd/promql-compliance-tester)
fi

OUTPUT_DIR="${PROM_SHIM_COMPLIANCE_ARTIFACT_DIR:-${REPO_ROOT}/harness/artifacts/compliance/latest}"
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
name_parts=( "compliance-report" )
[[ -n "$suffix" ]] && name_parts+=( "$suffix" )
name_parts+=( "$STAMP" )
IFS=- OUTPUT_FILE="${OUTPUT_DIR}/$(printf '%s' "${name_parts[*]}").json"; unset IFS

mode_banner=""
[[ -n "$mode" ]] && mode_banner=" [mode=${mode}]"
echo ">> Running compliance tester${mode_banner} → ${OUTPUT_FILE}"
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

echo ">> Summary${mode_banner}:"
jq '{
  total: .totalResults,
  passed: [.results[] | select((.toleranceApplied // null) == null and .diff == "" and (.unexpectedFailure // "") == "" and (.unexpectedSuccess // false) == false and (.unsupported // false) == false)] | length,
  accepted_tolerance: [.results[] | select((.toleranceApplied // null) != null)] | length,
  diff_failure: [.results[] | select(.diff != "")] | length,
  unexpected_failure: [.results[] | select((.unexpectedFailure // "") != "")] | length,
  unexpected_success: [.results[] | select(.unexpectedSuccess == true)] | length,
  unsupported: [.results[] | select(.unsupported == true)] | length
}' < "$OUTPUT_FILE"

# Instant-mode differential coverage. The upstream tester only issues
# query_range requests, so the instant fast paths went differentially
# unvalidated (this is how the instant-rate counter-reset bug hid). Compare a
# curated instant corpus at the same pinned end_time on both targets. Gate in
# prefer mode (a divergence is a real bug, never allowlisted); informational in
# native mode like the range gap report.
INSTANT_EVAL_TIME="$(awk -F"'" '/end_time:/ {print $2; exit}' "${ROOT}/test-promshim.yml")"
INSTANT_CORPUS="${ROOT}/instant-queries.yml"
if [[ -z "$INSTANT_EVAL_TIME" ]]; then
  echo ">> WARNING: could not read end_time from test-promshim.yml; skipping instant differential coverage." >&2
elif [[ ! -f "$INSTANT_CORPUS" ]]; then
  echo ">> WARNING: instant corpus not found (${INSTANT_CORPUS}); skipping instant differential coverage." >&2
else
  instant_parts=( "compliance-report-instant" )
  [[ -n "$suffix" ]] && instant_parts+=( "$suffix" )
  instant_parts+=( "$STAMP" )
  IFS=- INSTANT_OUTPUT_FILE="${OUTPUT_DIR}/$(printf '%s' "${instant_parts[*]}").json"; unset IFS
  instant_args=(
    --corpus "$INSTANT_CORPUS"
    --reference-url "http://localhost:29090"
    --test-url "http://localhost:29091"
    --eval-time "$INSTANT_EVAL_TIME"
    --json-out "$INSTANT_OUTPUT_FILE"
  )
  [[ -n "$mode" ]] && instant_args+=( --mode "$mode" )
  if [[ "$mode" == "native" ]]; then
    echo
    echo ">> Instant differential coverage${mode_banner} (informational)"
    (cd "$REPO_ROOT" && go run ./cmd/promshim-instant-compliance "${instant_args[@]}") || true
  else
    echo
    echo ">> Instant differential coverage${mode_banner} (gated)"
    (cd "$REPO_ROOT" && go run ./cmd/promshim-instant-compliance "${instant_args[@]}" --gate)
  fi
fi

if [[ "$mode" == "native" ]]; then
  # Native-mode gaps are tracked openly, not allowlisted. The outer
  # runner calls scripts/native-gap-report.sh for a categorized view;
  # reconcile-expected.sh would just print "REGRESSION" against the
  # gap count, which is confusing in this mode.
  echo
  echo ">> Skipping allowlist reconcile for native-mode pass."
  echo "   Run scripts/native-gap-report.sh for the gap breakdown."
else
  echo
  echo ">> Reconciling against expected-failures allowlist${mode_banner}"
  reconcile_args=( "$OUTPUT_FILE" )
  [[ -n "$mode" ]] && reconcile_args+=( --mode "$mode" )
  "${ROOT}/scripts/reconcile-expected.sh" "${reconcile_args[@]}"
fi
