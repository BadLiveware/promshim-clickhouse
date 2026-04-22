#!/usr/bin/env bash
# ch-profile-diff.sh — diff two ch-profile.json captures.
#
# Emits a per-normalized-query delta table sorted by p50_ms change.
# Highlights:
#   - latency (p50_ms/min_ms) changes
#   - I/O (read_rows/bytes) changes — the real signal for "did we prune?"
#   - ProfileEvents deltas for keys that changed (FunctionExecute, ArraySort, etc.)
#   - queries present in one capture but not the other
#   - queries whose SQL *shape* (normalized_query) changed — that's either a
#     real rewrite win or a silent regression. Either way, worth attention.
#
# Usage:
#   ./scripts/ch-profile-diff.sh before.json after.json [--format table|json]
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "Usage: $0 <before.json> <after.json> [--format table|json] [--min-delta-ms N] [--events K1,K2,...]" >&2
  exit 64
fi

BEFORE="$1"; shift
AFTER="$1"; shift

FORMAT=table
MIN_DELTA_MS=0
EVENTS_CSV="RealTimeMicroseconds,UserTimeMicroseconds,SelectedRows,SelectedBytes,ReadCompressedBytes,FunctionExecute,ArraySort,NetworkSendBytes,OSReadChars"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --format)        FORMAT="$2"; shift 2 ;;
    --min-delta-ms)  MIN_DELTA_MS="$2"; shift 2 ;;
    --events)        EVENTS_CSV="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 64 ;;
  esac
done

[[ -f "$BEFORE" ]] || { echo "missing: $BEFORE" >&2; exit 2; }
[[ -f "$AFTER"  ]] || { echo "missing: $AFTER"  >&2; exit 2; }

# jq program: join by normalized_query, compute deltas, filter by
# min-delta-ms, and output either a markdown table or a JSON document.
JQ=$(cat <<'JQ'
def to_num:       if . == null then 0 else . end;
def pct(before; after):
  if (before | to_num) == 0 then
    (if (after | to_num) == 0 then 0 else null end)
  else
    ((after - before) * 100.0 / before)
  end;

# Index by normalized_query for fast lookup.
def as_map: (.queries // []) | map({(.normalized_query): .}) | add // {};

. as [$before, $after]
| ($before | as_map) as $b
| ($after  | as_map) as $a
| (($b | keys) + ($a | keys) | unique) as $all_keys
| $events as $event_names
| $all_keys
| map(
    . as $k
    | ($b[$k] // null) as $bq
    | ($a[$k] // null) as $aq
    | {
        normalized_query: $k,
        example_query:    ($aq.example_query // $bq.example_query),
        present_before:   ($bq != null),
        present_after:    ($aq != null),
        p50_ms_before:    ($bq.p50_ms     // null),
        p50_ms_after:     ($aq.p50_ms     // null),
        p50_ms_delta:     (($aq.p50_ms // 0) - ($bq.p50_ms // 0)),
        p50_ms_pct:       pct($bq.p50_ms; $aq.p50_ms),
        min_ms_delta:     (($aq.min_ms // 0) - ($bq.min_ms // 0)),
        read_rows_before: ($bq.p50_read_rows  // null),
        read_rows_after:  ($aq.p50_read_rows  // null),
        read_rows_delta:  (($aq.p50_read_rows  // 0) - ($bq.p50_read_rows  // 0)),
        read_bytes_delta: (($aq.p50_read_bytes // 0) - ($bq.p50_read_bytes // 0)),
        memory_delta:     (($aq.p50_memory_bytes // 0) - ($bq.p50_memory_bytes // 0)),
        events: (
          $event_names
          | map(
              . as $ev
              | ($bq.profile_events_sum[$ev] // 0) as $before_v
              | ($aq.profile_events_sum[$ev] // 0) as $after_v
              | {key: $ev, before: $before_v, after: $after_v, delta: ($after_v - $before_v)}
            )
            | map(select(.delta != 0))
        )
      }
  )
| map(select((.p50_ms_delta | fabs) >= $mindelta or (.present_before | not) or (.present_after | not) or (.events | length) > 0))
| sort_by(.p50_ms_delta)
| reverse
JQ
)

IFS=',' read -ra EVENT_ARR <<<"$EVENTS_CSV"
EVENTS_JSON=$(printf '%s\n' "${EVENT_ARR[@]}" | jq -R . | jq -s .)

DELTAS=$(jq -n \
  --slurpfile before "$BEFORE" \
  --slurpfile after "$AFTER" \
  --argjson mindelta "$MIN_DELTA_MS" \
  --argjson events "$EVENTS_JSON" \
  "[\$before[0], \$after[0]] | $JQ")

if [[ "$FORMAT" == "json" ]]; then
  echo "$DELTAS"
  exit 0
fi

# Markdown table.
BEFORE_SHA=$(jq -r '.git.sha // "?"' "$BEFORE")
AFTER_SHA=$(jq -r '.git.sha // "?"' "$AFTER")

echo "## ClickHouse profile diff — ${BEFORE_SHA} → ${AFTER_SHA}"
echo
echo "_Events tracked: ${EVENTS_CSV}_"
echo "_Rows filtered to |Δp50_ms| ≥ ${MIN_DELTA_MS}ms, or present-in-one-only, or non-zero event delta._"
echo
echo "| Δp50 ms | Δ% | p50 before→after | Δ read_rows | Δ read_bytes | Δ memory B | status | query (head) |"
echo "|---:|---:|---|---:|---:|---:|---|---|"

jq -r '
  .[]
  | ([
      (.p50_ms_delta     | tostring),
      (if .p50_ms_pct == null then "—" else (.p50_ms_pct * 10 | round / 10 | tostring + "%") end),
      ((if .p50_ms_before == null then "—" else (.p50_ms_before|tostring) end) + "→" + (if .p50_ms_after == null then "—" else (.p50_ms_after|tostring) end)),
      (.read_rows_delta  | tostring),
      (.read_bytes_delta | tostring),
      (.memory_delta     | tostring),
      (if .present_before and .present_after then "both"
        elif .present_before then "**gone**"
        else "**new**" end),
      ((.example_query // "") | gsub("[\\n\\t ]+"; " ") | .[0:110])
    ] | join(" | "))
  | "| " + . + " |"
' <<<"$DELTAS"

echo
echo "### ProfileEvents deltas (non-zero)"
echo
jq -r '
  .[]
  | select(.events | length > 0)
  | "#### " + ((.example_query // "") | gsub("[\\n\\t ]+"; " ") | .[0:90]),
    "",
    "| event | before | after | Δ |",
    "|---|---:|---:|---:|",
    (.events[] | "| " + .key + " | " + (.before|tostring) + " | " + (.after|tostring) + " | " + (.delta|tostring) + " |"),
    ""
' <<<"$DELTAS"
