#!/usr/bin/env bash
# reconcile-expected.sh - reconcile a compliance report against an allowlist
# of known-expected failures (harness/compliance/expected-failures.json).
#
# A report is "clean" iff:
#   - every non-passing result is covered by an allowlist entry, AND
#   - every applicable allowlist entry matches at least one result.
#
# The second rule catches drift in either direction: if an expected failure
# disappears (e.g. the shim starts mirroring Prom's tie-break ordering),
# reconciliation fails so we notice and can retire the allowlist entry.
#
# Allowlist entries can restrict themselves to a mode via `modes: [...]`,
# so native-only expected failures are ignored when reconciling a prefer
# mode run (and vice versa).
#
# Allowlist entries match a failure when:
#   - `query` matches exactly (optional — omit to match by error alone), AND
#   - every `must_match.err_contains` substring appears in unexpectedFailure, AND
#   - every `must_match.diff_contains_all` substring appears in the diff, AND
#   - no `must_match.diff_contains_none` substring appears in the diff.
#
# Usage:
#   reconcile-expected.sh                       # latest artifacts report
#   reconcile-expected.sh REPORT_PATH
#   reconcile-expected.sh --mode native [...]   # only apply entries tagged "native"
#   reconcile-expected.sh --allowlist PATH      # override allowlist file
#
# Exits 0 on clean, 1 on regression, 2 on usage error.

set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

allowlist="${ROOT}/expected-failures.json"
report=""
mode=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --allowlist) allowlist="$2"; shift 2 ;;
    --mode)      mode="$2"; shift 2 ;;
    -h|--help)   sed -n '2,30p' "$0"; exit 0 ;;
    -*)          echo "unknown flag: $1" >&2; exit 2 ;;
    *)           report="$1"; shift ;;
  esac
done

if [[ -z "$report" ]]; then
  report=$(ls -t "$ROOT/artifacts"/compliance-report-*.json 2>/dev/null | head -1 || true)
  if [[ -z "$report" ]]; then
    echo "no report found in $ROOT/artifacts/" >&2
    exit 2
  fi
fi

if [[ ! -f "$allowlist" ]]; then
  echo "allowlist not found: $allowlist" >&2
  exit 2
fi

echo "report:    $report"
echo "allowlist: $allowlist"
if [[ -n "$mode" ]]; then
  echo "mode:      $mode"
fi
echo

summary=$(jq -n \
  --arg mode "$mode" \
  --slurpfile report "$report" \
  --slurpfile allow "$allowlist" '
  ($report[0].results // []) as $results
  | ($allow[0].entries // []) as $entries
  | [$entries[] | . as $e | select(
      # Entry applies when it has no `modes` field, or the current mode is
      # in the list. `modes: []` explicitly disables the entry.
      ($e.modes // null) as $m
      | ($m == null) or ($m | any(. == $mode))
    )] as $applicable
  | [$results[] | select(
      (.diff // "") != ""
      or (.unexpectedFailure // "") != ""
      or (.unexpectedSuccess // false) == true
      or (.unsupported // false) == true
    )] as $failures
  | [$failures[] | . as $f |
      {
        result: $f,
        matched: ([
          $applicable[] | . as $e
          | select(
              (($e.query // null) == null or $f.testCase.query == $e.query)
              and (
                ($e.must_match.err_contains // [])
                | all(. as $n | (($f.unexpectedFailure // "") | contains($n)))
              )
              and (
                ($e.must_match.diff_contains_all // [])
                | all(. as $n | (($f.diff // "") | contains($n)))
              )
              and (
                ($e.must_match.diff_contains_none // [])
                | all(. as $n | (($f.diff // "") | contains($n)) | not)
              )
            )
        ] | .[0])
      }
    ] as $annotated
  | {
      total_failures: ($failures | length),
      expected_count:   ([$annotated[] | select(.matched != null)] | length),
      unexpected_count: ([$annotated[] | select(.matched == null)] | length),
      expected: [$annotated[]
        | select(.matched != null)
        | {id: .matched.id, query: .result.testCase.query, reason: .matched.reason}],
      unexpected: [$annotated[]
        | select(.matched == null)
        | {
            query: .result.testCase.query,
            has_diff: ((.result.diff // "") | length > 0),
            err: (.result.unexpectedFailure // ""),
            diff_head: ((.result.diff // "") | .[0:240])
          }],
      allowlist_unused: [$applicable[] | . as $e
        | select(([$annotated[] | select((.matched.id // "") == $e.id)] | length) == 0)
        | {id: .id, query: ($e.query // "(no query — matches by error)")}]
    }
')

total=$(jq -r '.total_failures' <<<"$summary")
expected=$(jq -r '.expected_count' <<<"$summary")
unexpected=$(jq -r '.unexpected_count' <<<"$summary")
unused=$(jq -r '.allowlist_unused | length' <<<"$summary")

printf 'failures: %d total / %d expected / %d unexpected\n' "$total" "$expected" "$unexpected"
printf 'allowlist: %d applicable entr(y|ies) never matched\n' "$unused"
echo

if [[ "$expected" -gt 0 ]]; then
  echo '== expected (matched allowlist) =='
  jq -r '
    .expected
    | group_by(.id)
    | map({id: .[0].id, reason: .[0].reason, count: length, sample: .[0].query})
    | .[]
    | "[\(.id)] \(.count) match(es) — sample query: \(.sample)\n  reason: \(.reason)\n"
  ' <<<"$summary"
fi

if [[ "$unexpected" -gt 0 ]]; then
  echo '== UNEXPECTED failures (potential regressions) =='
  jq -r '.unexpected[] |
    "QUERY: \(.query)" +
    (if .err != ""       then "\n  ERR : \(.err)" else "" end) +
    (if .has_diff        then "\n  DIFF: \(.diff_head)..." else "" end) +
    "\n"' <<<"$summary"
fi

if [[ "$unused" -gt 0 ]]; then
  echo '== UNMATCHED allowlist entries (stale allowlist -> regression of its own) =='
  jq -r '.allowlist_unused[] | "[\(.id)] \(.query)"' <<<"$summary"
  echo
fi

if [[ "$unexpected" -gt 0 ]] || [[ "$unused" -gt 0 ]]; then
  echo 'RECONCILE: REGRESSION'
  exit 1
fi

echo 'RECONCILE: CLEAN'
exit 0
